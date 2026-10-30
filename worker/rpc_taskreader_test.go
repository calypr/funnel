package worker

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/calypr/funnel/events"
	grpc_retry "github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/retry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type rpcExecutionClaimer struct {
	mu      sync.Mutex
	calls   int
	taskID  string
	owner   *events.ExecutionOwner
	claimed bool
	err     error
}

func (c *rpcExecutionClaimer) ClaimExecution(_ context.Context, taskID string, owner *events.ExecutionOwner) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	c.taskID = taskID
	c.owner = owner
	return c.claimed, c.err
}

func (c *rpcExecutionClaimer) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func newRPCClaimReader(t *testing.T, service events.EventServiceServer) *RPCTaskReader {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	events.RegisterEventServiceServer(server, service)
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.Serve(listener)
	}()
	var conn *grpc.ClientConn
	t.Cleanup(func() {
		if conn != nil {
			if err := conn.Close(); err != nil {
				t.Errorf("close gRPC test connection: %v", err)
			}
		}
		server.Stop()
		if err := <-serveErr; err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			t.Errorf("stop gRPC test server: %v", err)
		}
	})

	clientConn, err := grpc.NewClient(
		"passthrough:///bufconn",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(grpc_retry.UnaryClientInterceptor(
			grpc_retry.WithMax(3),
			grpc_retry.WithCodes(codes.Unavailable),
			grpc_retry.WithBackoff(func(context.Context, uint) time.Duration { return 0 }),
		)),
	)
	if err != nil {
		t.Fatalf("create gRPC connection: %v", err)
	}
	conn = clientConn

	return &RPCTaskReader{
		eventClient: events.NewEventServiceClient(conn),
		conn:        conn,
		taskID:      "task-1",
	}
}

func TestRPCTaskReaderClaimsExecutionOverGRPC(t *testing.T) {
	claimer := &rpcExecutionClaimer{claimed: true}
	reader := newRPCClaimReader(t, &events.Service{Writer: events.Noop{}, Claimer: claimer})
	owner := &events.ExecutionOwner{Namespace: "jobs", PodName: "worker-abc", PodUid: "pod-uid"}

	claimed, err := reader.ClaimExecution(context.Background(), "task-1", owner)
	if err != nil {
		t.Fatalf("ClaimExecution() error = %v", err)
	}
	if !claimed {
		t.Fatal("ClaimExecution() = false, want true")
	}
	if claimer.callCount() != 1 || claimer.taskID != "task-1" {
		t.Fatalf("server saw %d claims for task %q", claimer.callCount(), claimer.taskID)
	}
	if claimer.owner == nil || claimer.owner.GetNamespace() != owner.GetNamespace() || claimer.owner.GetPodName() != owner.GetPodName() || claimer.owner.GetPodUid() != owner.GetPodUid() {
		t.Fatalf("server saw owner %v, want %v", claimer.owner, owner)
	}
}

func TestRPCTaskReaderDoesNotRetryLostClaimResponse(t *testing.T) {
	claimer := &rpcExecutionClaimer{err: status.Error(codes.Unavailable, "claim committed but response was lost")}
	reader := newRPCClaimReader(t, &events.Service{Writer: events.Noop{}, Claimer: claimer})

	claimed, err := reader.ClaimExecution(context.Background(), "task-1", nil)
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("ClaimExecution() error code = %s, want %s", status.Code(err), codes.Unavailable)
	}
	if claimed {
		t.Fatal("ClaimExecution() = true after a lost response, want false")
	}
	if got := claimer.callCount(); got != 1 {
		t.Fatalf("server received %d claim attempts, want exactly one", got)
	}
}

func TestRPCTaskReaderFailsClosedWhenDatabaseDoesNotSupportClaims(t *testing.T) {
	reader := newRPCClaimReader(t, &events.Service{Writer: events.Noop{}})

	claimed, err := reader.ClaimExecution(context.Background(), "task-1", nil)
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("ClaimExecution() error code = %s, want %s", status.Code(err), codes.FailedPrecondition)
	}
	if claimed {
		t.Fatal("ClaimExecution() = true when the server has no claimer, want false")
	}
}

type oldEventService struct {
	events.UnimplementedEventServiceServer
}

func (oldEventService) WriteEvent(context.Context, *events.Event) (*events.WriteEventResponse, error) {
	return &events.WriteEventResponse{}, nil
}

func TestRPCTaskReaderFailsClosedWithOldServer(t *testing.T) {
	reader := newRPCClaimReader(t, oldEventService{})

	claimed, err := reader.ClaimExecution(context.Background(), "task-1", nil)
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("ClaimExecution() error code = %s, want %s", status.Code(err), codes.Unimplemented)
	}
	if claimed {
		t.Fatal("ClaimExecution() = true with an old server, want false")
	}
}
