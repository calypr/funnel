package server

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/calypr/funnel/logger"
	"github.com/calypr/funnel/tes"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type panicTaskService struct {
	tes.UnimplementedTaskServiceServer
}

func (panicTaskService) GetTask(_ context.Context, req *tes.GetTaskRequest) (*tes.Task, error) {
	if req.Id == "panic" {
		panic("private integration panic")
	}
	return &tes.Task{Id: req.Id, State: tes.State_COMPLETE}, nil
}

func TestGRPCServerSurvivesHandlerPanic(t *testing.T) {
	listener := bufconn.Listen(1024 * 1024)
	t.Cleanup(func() { _ = listener.Close() })
	server := grpc.NewServer(grpc.ChainUnaryInterceptor(
		NewAuthentication(nil, nil, AccessAll).Interceptor,
		newDebugInterceptor(logger.NewLogger("panic-integration", logger.DefaultConfig())),
	))
	tes.RegisterTaskServiceServer(server, panicTaskService{})
	stopped := make(chan struct{})
	go func() { defer close(stopped); _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); <-stopped })
	connection, err := grpc.NewClient("passthrough:///panic-test",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	client := tes.NewTaskServiceClient(connection)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = client.GetTask(ctx, &tes.GetTaskRequest{Id: "panic"})
	if status.Code(err) != codes.Internal {
		t.Fatalf("panic request: %v, want Internal", err)
	}
	if status.Convert(err).Message() != "internal server error" {
		t.Fatalf("private panic leaked: %v", err)
	}
	task, err := client.GetTask(ctx, &tes.GetTaskRequest{Id: "healthy"})
	if err != nil {
		t.Fatalf("server failed after panic: %v", err)
	}
	if task.Id != "healthy" || task.State != tes.State_COMPLETE {
		t.Fatalf("unexpected response: %v", task)
	}
}
