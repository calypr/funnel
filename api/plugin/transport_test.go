package plugin

import (
	"context"
	"errors"
	"net"
	"net/rpc"
	"strings"
	"testing"

	"github.com/calypr/funnel/api/config"
	"github.com/calypr/funnel/api/tes"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
)

type recordingAuthorizer struct {
	requests chan *Job
	response *JobResponse
	err      error
}

func (a *recordingAuthorizer) PluginAction(params map[string]string, headers map[string]*StringList, conf *config.Config, task *tes.Task, action Type) (*JobResponse, error) {
	a.requests <- &Job{Params: params, Headers: headers, Config: conf, Task: task, Type: action}
	return a.response, a.err
}

func TestAuthorizeTransports(t *testing.T) {
	for _, transport := range []string{"grpc", "netrpc"} {
		for _, rejected := range []bool{false, true} {
			name := transport + "/success"
			if rejected {
				name = transport + "/rejected"
			}
			t.Run(name, func(t *testing.T) {
				request := &Job{
					Params:  map[string]string{"task_id": "task-123"},
					Headers: map[string]*StringList{"X-User": {Values: []string{"alice", "bob"}}},
					Config:  &config.Config{Server: &config.Server{HTTPPort: "8000"}},
					Task:    &tes.Task{Id: "task-123"}, Type: Type_GET,
				}
				expected := &JobResponse{Code: 200, Message: "authorized", UserId: "alice", Config: request.Config, Task: request.Task}
				impl := &recordingAuthorizer{requests: make(chan *Job, 1), response: expected}
				if rejected {
					impl.err = errors.New("permission denied by plugin")
				}
				var client Authorize
				if transport == "grpc" {
					client = grpcAuthorizer(t, impl)
				} else {
					client = rpcAuthorizer(t, impl)
				}
				response, err := client.PluginAction(request.Params, request.Headers, request.Config, request.Task, request.Type)
				if rejected {
					if err == nil || !strings.Contains(err.Error(), impl.err.Error()) {
						t.Fatalf("expected plugin rejection, got %v", err)
					}
				} else {
					if err != nil {
						t.Fatal(err)
					}
					if !proto.Equal(response, expected) {
						t.Fatalf("response changed: got %v, want %v", response, expected)
					}
				}
				select {
				case actual := <-impl.requests:
					if !proto.Equal(actual, request) {
						t.Fatalf("request changed: got %v, want %v", actual, request)
					}
				default:
					t.Fatal("authorizer was not called")
				}
			})
		}
	}
}

func grpcAuthorizer(t *testing.T, impl Authorize) Authorize {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	adapter := &AuthorizeGRPCPlugin{Impl: impl}
	if err := adapter.GRPCServer(nil, server); err != nil {
		t.Fatal(err)
	}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, grpc.ErrServerStopped) && !errors.Is(err, net.ErrClosed) {
			t.Errorf("serve plugin gRPC: %v", err)
		}
	}()
	t.Cleanup(func() {
		server.Stop()
		if err := listener.Close(); err != nil {
			t.Errorf("close plugin listener: %v", err)
		}
	})
	conn, err := grpc.NewClient("passthrough:///plugin", grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close plugin gRPC client: %v", err)
		}
	})
	client, err := adapter.GRPCClient(t.Context(), nil, conn)
	if err != nil {
		t.Fatal(err)
	}
	return client.(Authorize)
}

func rpcAuthorizer(t *testing.T, impl Authorize) Authorize {
	t.Helper()
	adapter := &AuthorizePlugin{Impl: impl}
	handler, err := adapter.Server(nil)
	if err != nil {
		t.Fatal(err)
	}
	server := rpc.NewServer()
	if err := server.RegisterName("Plugin", handler); err != nil {
		t.Fatal(err)
	}
	hostConn, pluginConn := net.Pipe()
	go server.ServeConn(pluginConn)
	rpcClient := rpc.NewClient(hostConn)
	t.Cleanup(func() {
		if err := rpcClient.Close(); err != nil && !errors.Is(err, rpc.ErrShutdown) {
			t.Errorf("close plugin RPC client: %v", err)
		}
		if err := pluginConn.Close(); err != nil {
			t.Errorf("close plugin RPC connection: %v", err)
		}
	})
	client, err := adapter.Client(nil, rpcClient)
	if err != nil {
		t.Fatal(err)
	}
	return client.(Authorize)
}
