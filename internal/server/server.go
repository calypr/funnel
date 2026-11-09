// Package server owns HTTP/gRPC serving, service registration, and listener lifecycle.
package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"

	serverauth "github.com/calypr/funnel/internal/server/auth"
	"github.com/calypr/funnel/internal/server/gateway"
	"github.com/calypr/funnel/internal/server/middleware"

	"time"

	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"

	"github.com/calypr/funnel/api/config"
	"github.com/calypr/funnel/internal/compute/scheduler"
	"github.com/calypr/funnel/internal/events"
	"github.com/calypr/funnel/internal/logger"
	taskpolicy "github.com/calypr/funnel/internal/task"
	"github.com/calypr/funnel/internal/tesrpc"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"

	"google.golang.org/grpc"
)

// Server represents a Funnel server. The server handles
// RPC traffic via gRPC, HTTP traffic for the TES API,
// and also serves the web dashboard.
type Server struct {
	// Listen defaults to net.Listen. Embedders can supply already-bound listeners.
	Listen           func(network, address string) (net.Listener, error)
	RPCAddress       string
	HTTPPort         string
	BasicAuth        []*config.BasicCredential
	OidcAuth         *config.OidcAuth
	TaskAccess       string
	Tasks            tesrpc.TaskServiceServer
	Events           events.EventServiceServer
	Nodes            scheduler.SchedulerServiceServer
	DisableHTTPCache bool
	MaxResponseBytes int
	Log              *logger.Logger
	Plugins          *config.Plugins
}

// Serve starts the server and blocks until its context is canceled or a listener stops.
func (s *Server) Serve(pctx context.Context) error {
	ctx, cancel := context.WithCancel(pctx)
	defer cancel()
	closeTasks := func() {}
	if service, ok := s.Tasks.(interface{ Close() }); ok {
		closeTasks = sync.OnceFunc(service.Close)
	}
	defer closeTasks()

	// Open TCP connection for RPC
	listen := s.Listen
	if listen == nil {
		listen = net.Listen
	}
	lis, err := listen("tcp", s.RPCAddress)
	if err != nil {
		return err
	}
	defer lis.Close() //nolint:errcheck // Listener cleanup is best effort after the server has stopped.
	// Bind both sockets before gateway setup can open outgoing connections and
	// claim an ephemeral port intended for HTTP.
	httpAddress := ":" + s.HTTPPort
	httpLis, err := listen("tcp", httpAddress)
	if err != nil {
		return err
	}
	defer httpLis.Close() //nolint:errcheck // Listener cleanup is best effort after HTTP shutdown.

	auth := serverauth.NewAuthentication(s.BasicAuth, s.OidcAuth, s.TaskAccess, s.Log)

	maxResponseBytes := s.MaxResponseBytes
	if maxResponseBytes == 0 {
		maxResponseBytes = taskpolicy.DefaultResponseBytes
	}
	if maxResponseBytes < 0 {
		return fmt.Errorf("Server.MaxResponseBytes must be positive")
	}
	grpcServer := grpc.NewServer(
		grpc.MaxSendMsgSize(maxResponseBytes),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             10 * time.Second, // min interval between client pings
			PermitWithoutStream: true,             // allow pings when no active RPCs
		}),
		grpc.ChainUnaryInterceptor(
			// Recovery must wrap authentication and all other interceptors.
			middleware.DebugInterceptor(s.Log),
			auth.Interceptor,
			serverauth.AuditInterceptor(s.Log),
			serverauth.EventWriteInterceptor(s.Tasks),
		),
	)

	// Retry service config: transparently retry transient gRPC stream errors
	// (e.g. UNAVAILABLE after idle connection is closed by the server).
	// This prevents grpc-gateway from dropping the HTTP connection on the first
	// failed attempt when the internal gRPC connection has gone idle.
	const grpcServiceConfig = `{
		"methodConfig": [{
			"name": [{"service": ""}],
			"retryPolicy": {
				"maxAttempts": 4,
				"initialBackoff": "0.1s",
				"maxBackoff": "1s",
				"backoffMultiplier": 2,
				"retryableStatusCodes": ["UNAVAILABLE", "RESOURCE_EXHAUSTED"]
			}
		}]
	}`

	dialOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maxResponseBytes)),
		// Keep the internal gateway→gRPC connection alive so idle periods
		// don't cause the server to send GOAWAY and drop the next request.
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                20 * time.Second, // send pings every 20s if idle
			Timeout:             5 * time.Second,  // wait 5s for ping ack
			PermitWithoutStream: true,             // ping even with no active RPCs
		}),
		// Retry on transient errors at the gRPC level before surfacing to HTTP.
		grpc.WithDefaultServiceConfig(grpcServiceConfig),
	}

	// Set up HTTP proxy of gRPC API

	marsh := gateway.NewMarshaler()
	grpcMux := runtime.NewServeMux(
		runtime.WithMarshalerOption(runtime.MIMEWildcard, marsh),
		runtime.WithForwardResponseRewriter(gateway.RewriteTaskViewResponse),
		runtime.WithErrorHandler(gateway.ErrorHandler),
	)

	mux := s.routes(auth, grpcMux)

	// Register TES service
	if s.Tasks != nil {
		tesrpc.RegisterTaskServiceServer(grpcServer, s.Tasks)
		err := tesrpc.RegisterTaskServiceHandlerFromEndpoint(
			ctx, grpcMux, s.RPCAddress, dialOpts,
		)
		if err != nil {
			return err
		}
	}

	// Register Events service
	if s.Events != nil {
		events.RegisterEventServiceServer(grpcServer, s.Events)
	}

	// Register Scheduler RPC service
	if s.Nodes != nil {
		scheduler.RegisterSchedulerServiceServer(grpcServer, s.Nodes)
		err := scheduler.RegisterSchedulerServiceHandlerFromEndpoint(
			ctx, grpcMux, s.RPCAddress, dialOpts,
		)
		if err != nil {
			return err
		}
	}

	httpServer := &http.Server{
		Addr:    httpAddress,
		Handler: mux,
	}

	serveErrs := make(chan error, 2)
	go func() {
		serveErrs <- grpcServer.Serve(lis)
		cancel()
	}()

	go func() {
		serveErrs <- httpServer.Serve(httpLis)
		cancel()
	}()

	s.Log.Info("Server listening",
		"httpPort", s.HTTPPort, "rpcAddress", s.RPCAddress,
	)

	<-ctx.Done()
	grpcServer.GracefulStop()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	shutdownErr := httpServer.Shutdown(shutdownCtx)
	shutdownCancel()
	// Wait for CreateTask's in-flight compute backend submissions.
	closeTasks()

	var serveErr error
	for range 2 {
		err := <-serveErrs
		if err == nil || errors.Is(err, grpc.ErrServerStopped) || errors.Is(err, http.ErrServerClosed) {
			continue
		}
		if serveErr == nil {
			serveErr = err
		}
	}
	if serveErr != nil {
		return serveErr
	}
	return shutdownErr
}
