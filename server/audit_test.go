package server

import (
	"bytes"
	"strings"
	"testing"

	grpc_middleware "github.com/grpc-ecosystem/go-grpc-middleware"
	"github.com/ohsu-comp-bio/funnel/logger"
	"github.com/ohsu-comp-bio/funnel/tes"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// A failed CreateTask returns a typed-nil *tes.CreateTaskResponse alongside
// a non-nil error. Once boxed into the `resp interface{}` that the audit
// interceptor receives, that's a non-nil interface wrapping a nil pointer,
// so auditTaskID must not dereference it.
func TestAuditTaskIDNilCreateTaskResponse(t *testing.T) {
	var resp *tes.CreateTaskResponse

	id := auditTaskID(&tes.Task{}, resp)
	if id != "" {
		t.Errorf("expected empty taskID for nil CreateTaskResponse, got %q", id)
	}
}

func TestAuditTaskIDCreateTaskResponse(t *testing.T) {
	resp := &tes.CreateTaskResponse{Id: "task-123"}

	id := auditTaskID(&tes.Task{}, resp)
	if id != "task-123" {
		t.Errorf("expected task-123, got %q", id)
	}
}

func TestAuditTaskIDGetTaskRequest(t *testing.T) {
	req := &tes.GetTaskRequest{Id: "task-456"}

	id := auditTaskID(req, nil)
	if id != "task-456" {
		t.Errorf("expected task-456, got %q", id)
	}
}

// A panic in a handler is audited as Internal and then converted by the
// recovery interceptor, which wraps the audit interceptor in Server.Serve,
// into a generic gRPC Internal error; the panic itself is logged.
func TestAuditInterceptorPanic(t *testing.T) {
	var buf bytes.Buffer
	log := logger.NewLogger("test", logger.DefaultConfig())
	log.SetOutput(&buf)

	chain := grpc_middleware.ChainUnaryServer(
		newRecoveryInterceptor(log),
		newAuditInterceptor(log),
	)
	info := &grpc.UnaryServerInfo{FullMethod: "/tes.TaskService/GetTask"}
	panicking := func(ctx context.Context, req interface{}) (interface{}, error) {
		panic("boom")
	}

	ctx := context.WithValue(context.Background(), UserInfoKey, &UserInfo{Username: "alice"})
	_, err := chain(ctx, &tes.GetTaskRequest{Id: "task-789"}, info, panicking)
	if status.Code(err) != codes.Internal {
		t.Fatalf("expected codes.Internal, got %v", err)
	}
	if strings.Contains(err.Error(), "boom") {
		t.Errorf("the panic value must not be returned to the client: %v", err)
	}

	out := buf.String()
	for _, want := range []string{"AUDIT", "task-789", "Internal", "alice", "recovered from panic", "boom", "stack"} {
		if !strings.Contains(out, want) {
			t.Errorf("audit log %q does not contain %q", out, want)
		}
	}
}
