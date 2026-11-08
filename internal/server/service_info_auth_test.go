package server

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/calypr/funnel/config"
	appconfig "github.com/calypr/funnel/internal/config"
	"github.com/calypr/funnel/internal/tesrpc"
	"github.com/calypr/funnel/tes"
)

func TestServiceInfoAlwaysPublic(t *testing.T) {
	for _, mode := range []string{"none", "basic", "oidc", "combined"} {
		t.Run(mode, func(t *testing.T) {
			auth := NewAuthentication(nil, nil, AccessOwner)
			if mode == "basic" || mode == "combined" {
				auth = NewAuthentication([]*config.BasicCredential{{User: "alice", Password: "secret"}}, nil, AccessOwner)
			}
			if mode == "oidc" || mode == "combined" {
				auth.oidc = &OidcConfig{}
			}
			log, _ := serverCaptureLogger(t)
			auth.log = log
			listener := bufconn.Listen(1024 * 1024)
			rpc := grpc.NewServer(grpc.UnaryInterceptor(auth.Interceptor))
			tesrpc.RegisterTaskServiceServer(rpc, &TaskService{Name: "public-service", Config: appconfig.DefaultConfig()})
			done := make(chan struct{})
			go func() {
				defer close(done)
				if err := rpc.Serve(listener); err != nil {
					t.Errorf("serve RPC: %v", err)
				}
			}()
			t.Cleanup(func() { rpc.Stop(); <-done })
			conn, err := grpc.NewClient("passthrough:///service-info-test", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := conn.Close(); err != nil {
					t.Error(err)
				}
			})
			client := tesrpc.NewTaskServiceClient(conn)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			for _, token := range []string{"", "Basic invalid", "Bearer invalid"} {
				requestCtx := ctx
				if token != "" {
					requestCtx = metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", token))
				}
				info, err := client.GetServiceInfo(requestCtx, &tes.GetServiceInfoRequest{})
				if err != nil {
					t.Fatalf("anonymous service-info with %q: %v", token, err)
				}
				if info.Name != "public-service" {
					t.Fatalf("service name = %q", info.Name)
				}
			}
			gateway := runtime.NewServeMux()
			if err := tesrpc.RegisterTaskServiceHandlerClient(ctx, gateway, client); err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{"/service-info", "/v1/service-info", "/ga4gh/tes/v1/service-info"} {
				for _, token := range []string{"", "Bearer invalid"} {
					request := httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx)
					if token != "" {
						request.Header.Set("Authorization", token)
					}
					response := httptest.NewRecorder()
					gateway.ServeHTTP(response, request)
					if response.Code != http.StatusOK {
						t.Fatalf("GET %s = %d: %s", path, response.Code, response.Body.String())
					}
					var info map[string]any
					if err := json.Unmarshal(response.Body.Bytes(), &info); err != nil {
						t.Fatal(err)
					}
					if info["name"] != "public-service" {
						t.Fatalf("service-info response = %v", info)
					}
				}
			}
			if mode != "none" {
				if _, err := client.ListTasks(ctx, &tes.ListTasksRequest{}); status.Code(err) != codes.Unauthenticated {
					t.Fatalf("anonymous ListTasks = %v, want Unauthenticated", err)
				}
				response := httptest.NewRecorder()
				gateway.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/tasks", nil).WithContext(ctx))
				if response.Code != http.StatusUnauthorized {
					t.Fatalf("anonymous GET tasks = %d, want 401", response.Code)
				}
			}
		})
	}
}

func TestPublicServiceInfoReceivesAnonymousIdentity(t *testing.T) {
	auth := NewAuthentication([]*config.BasicCredential{{User: "admin", Password: "secret", Admin: true}}, nil, AccessOwner)
	_, err := auth.Interceptor(t.Context(), nil, &grpc.UnaryServerInfo{FullMethod: tesrpc.TaskService_GetServiceInfo_FullMethodName}, func(ctx context.Context, _ any) (any, error) {
		user := GetUser(ctx)
		if !user.IsPublic || user.IsAdmin || user.Username != "" {
			t.Errorf("public identity = %+v", user)
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
