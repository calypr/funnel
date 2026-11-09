package testenv

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	docker "github.com/moby/moby/client"
	"google.golang.org/grpc"
	"k8s.io/client-go/kubernetes"

	"github.com/calypr/funnel/api/config"
	tesclient "github.com/calypr/funnel/client/tes"
	servercmd "github.com/calypr/funnel/internal/cli/server"

	funnelkubernetes "github.com/calypr/funnel/internal/compute/kubernetes"
	"github.com/calypr/funnel/internal/compute/scheduler"
	appconfig "github.com/calypr/funnel/internal/config"
	"github.com/calypr/funnel/internal/logger"
	"github.com/calypr/funnel/internal/rpcclient"
	"github.com/calypr/funnel/internal/server"
	"github.com/calypr/funnel/internal/tesrpc"
)

var log = logger.NewLogger("e2e", LogConfig())

func init() {
	logger.SetGRPCLogger(log)
}

// Funnel provides a test server and RPC/HTTP clients
type Funnel struct {
	// Clients
	RPC        tesrpc.TaskServiceClient
	HTTP       *tesclient.Client
	Docker     *docker.Client
	Kubernetes *kubernetes.Clientset

	// Config
	Conf       *config.Config
	StorageDir string

	// Components
	Server    *server.Server
	Scheduler *scheduler.Scheduler
	Srv       *servercmd.Server

	// Internal
	cleanupOnce   sync.Once
	scope         CleanupScope
	rate          time.Duration
	conn          *grpc.ClientConn
	serverCancel  context.CancelFunc
	serverStopped <-chan struct{}
}

// NewFunnel creates a new funnel test server with some test
// configuration automatically set: random ports, temp work dir, etc.
func NewFunnel(scope CleanupScope, conf *config.Config) *Funnel {
	f := &Funnel{Conf: conf, StorageDir: conf.LocalStorage.AllowedDirs[0], scope: scope}
	scope.Cleanup(f.Cleanup)
	cli, err := tesclient.NewClient(appconfig.HTTPAddress(conf.Server))
	if err != nil {
		panic(err)
	}

	dcli, derr := newDockerClient()
	if derr != nil {
		panic(derr)
	}

	f.Docker = dcli

	var kcli *kubernetes.Clientset
	var kerr error
	if conf.Compute == "kubernetes" {
		kcli, kerr = funnelkubernetes.NewClient()
		if kerr != nil {
			panic(kerr)
		}
	}

	srv, err := servercmd.NewServer(context.Background(), conf, log)
	if err != nil {
		panic(err)
	}

	f.HTTP = cli
	f.Kubernetes = kcli
	f.Server = srv.Server
	f.Srv = srv
	f.Scheduler = srv.Scheduler
	f.rate = time.Millisecond * 500
	return f
}

// addRPCClient configures and connects the RPC client to the server.
func (f *Funnel) addRPCClient() {
	conn, err := rpcclient.Dial(context.Background(), f.Conf.RPCClient)
	if err != nil {
		panic(err)
	}
	f.RPC = tesrpc.NewTaskServiceClient(conn)
	f.conn = conn
}

// Cleanup cleans up test resources
func (f *Funnel) Cleanup() {
	f.cleanupOnce.Do(func() {
		if f.serverCancel != nil {
			f.serverCancel()
			if f.serverStopped != nil {
				<-f.serverStopped
			}
			f.serverCancel = nil
			f.serverStopped = nil
		}
		var cleanupErr error
		if f.conn != nil {
			cleanupErr = errors.Join(cleanupErr, f.conn.Close())
			f.conn = nil
		}
		if f.Srv != nil {
			f.Srv.Close()
		}
		if f.Docker != nil {
			cleanupErr = errors.Join(cleanupErr, f.Docker.Close())
			f.Docker = nil
		}
		if cleanupErr != nil {
			if f.scope != nil {
				f.scope.Errorf("close Funnel test resources: %v", cleanupErr)
			} else {
				panic(cleanupErr)
			}
		}
	})
}

// StartServer starts the server
func (f *Funnel) StartServer() {
	ctx, cancel := context.WithCancel(context.Background())
	serverDone := make(chan error, 1)
	serverStopped := make(chan struct{})
	f.serverCancel = cancel
	f.serverStopped = serverStopped
	go func() {
		defer close(serverStopped)
		serverDone <- f.Srv.Run(ctx)
	}()

	err := f.pollForServerStart(serverDone)
	if err != nil {
		cancel()
		log.Error("failed to start funnel server", err)
		panic(err)
	}

	f.addRPCClient()
	log.Info("Funnel server started")
}

// PollForServerStart polls the server http address to check if the server is running.
func (f *Funnel) PollForServerStart() error {
	return f.pollForServerStart(nil)
}

func (f *Funnel) pollForServerStart(serverDone <-chan error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	client := &http.Client{Timeout: time.Second}

	for {
		select {
		case err := <-serverDone:
			return serverRunStoppedError(err)
		case <-ctx.Done():
			return fmt.Errorf("timeout error - server didn't start within 30 seconds")
		case <-ticker.C:
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, appconfig.HTTPAddress(f.Conf.Server)+"/healthz", nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if resp != nil {
			closeErr := resp.Body.Close()
			if closeErr != nil {
				return fmt.Errorf("close health check response body: %w", closeErr)
			}
		}
		if err != nil {
			continue
		}

		log.Info(fmt.Sprintf("Server at %s found", appconfig.HTTPAddress(f.Conf.Server)))
		grace := time.NewTimer(time.Second)
		select {
		case err := <-serverDone:
			grace.Stop()
			return serverRunStoppedError(err)
		case <-ctx.Done():
			grace.Stop()
			return fmt.Errorf("timeout error - server didn't start within 30 seconds")
		case <-grace.C:
			return nil
		}
	}
}

func serverRunStoppedError(err error) error {
	if err == nil {
		return errors.New("server Run returned before startup completed")
	}
	return fmt.Errorf("server failed before startup completed: %w", err)
}
