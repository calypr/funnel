package rpc

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/calypr/funnel/config"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestExponentialBackoff(t *testing.T) {

	eb := newExponentialBackoff()

	getMinMax := func(val time.Duration, randomizationFactor float64) (min time.Duration, max time.Duration) {
		v := float64(val)
		delta := randomizationFactor * v
		min = time.Duration(v - delta)
		max = time.Duration(v + delta)
		return
	}

	maxBackoffMin, maxBackoffMax := getMinMax(eb.Max, eb.RandomizationFactor)
	t.Logf("maxBackoff jittered interval [%v, %v]", maxBackoffMin, maxBackoffMax)

	for i := 0; i < 10; i++ {
		attempt := uint(i)
		t.Log("attempt", attempt)

		nb := eb.Backoff(attempt)
		nbj := eb.BackoffWithJitter(context.Background(), attempt)
		min, max := getMinMax(nb, eb.RandomizationFactor)

		if nb > eb.Max {
			if nbj < maxBackoffMin || nbj > maxBackoffMax {
				t.Errorf("expected backoff to be limited by maxBackoff parameter. Got %v", nbj)
			}
		} else {
			if nbj < min || nbj > max {
				t.Errorf("expected backoff to be in interval [%v, %v]. Got %v", min, max, nbj)
			}
		}
	}
}

func TestDialRetriesUnavailableButNotPermissionDenied(t *testing.T) {
	listener := bufconn.Listen(1024 * 1024)
	defer listener.Close() //nolint:errcheck // Test server shutdown also closes this listener.
	grpcServer := grpc.NewServer()
	service := &retryTestService{}
	grpcServer.RegisterService(&retryTestServiceDesc, service)
	go func() {
		if err := grpcServer.Serve(listener); err != nil {
			t.Errorf("serve test RPC: %v", err)
		}
	}()
	defer grpcServer.Stop()

	conf := config.DefaultConfig().RPCClient
	conf.ServerAddress = "passthrough:///retry-test"
	conf.MaxRetries = 2

	clientCtx, cancelClient := context.WithCancel(context.Background())
	defer cancelClient()
	conn, err := Dial(clientCtx, conf, grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
		return listener.Dial()
	}))
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close() //nolint:errcheck // Best-effort cleanup of a test connection after assertions.

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	response := new(wrapperspb.StringValue)
	err = conn.Invoke(ctx, "/rpc.RetryService/Call", wrapperspb.String("retry"), response)
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if response.GetValue() != "retried" {
		t.Fatalf("Invoke() response = %q, want %q", response.GetValue(), "retried")
	}
	if got := service.calls.Load(); got != 2 {
		t.Fatalf("server calls after UNAVAILABLE = %d, want 2", got)
	}

	err = conn.Invoke(ctx, "/rpc.RetryService/Call", wrapperspb.String("denied"), new(wrapperspb.StringValue))
	if got, want := status.Code(err), codes.PermissionDenied; got != want {
		t.Fatalf("Invoke() status = %s, want %s", got, want)
	}
	if got := service.calls.Load(); got != 3 {
		t.Fatalf("server calls after PERMISSION_DENIED = %d, want 3", got)
	}
}

type retryTestRPCServer interface {
	Call(context.Context, *wrapperspb.StringValue) (*wrapperspb.StringValue, error)
}

type retryTestService struct {
	calls atomic.Int32
}

func (s *retryTestService) Call(_ context.Context, req *wrapperspb.StringValue) (*wrapperspb.StringValue, error) {
	call := s.calls.Add(1)
	if req.GetValue() == "denied" {
		return nil, status.Error(codes.PermissionDenied, "denied")
	}
	if call == 1 {
		return nil, status.Error(codes.Unavailable, "try again")
	}
	return wrapperspb.String("retried"), nil
}

var retryTestServiceDesc = grpc.ServiceDesc{
	ServiceName: "rpc.RetryService",
	HandlerType: (*retryTestRPCServer)(nil),
	Methods: []grpc.MethodDesc{{
		MethodName: "Call",
		Handler: func(srv any, ctx context.Context, decode func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
			req := new(wrapperspb.StringValue)
			if err := decode(req); err != nil {
				return nil, err
			}
			call := func(ctx context.Context, req any) (any, error) {
				return srv.(retryTestRPCServer).Call(ctx, req.(*wrapperspb.StringValue))
			}
			if interceptor == nil {
				return call(ctx, req)
			}
			return interceptor(ctx, req, &grpc.UnaryServerInfo{Server: srv, FullMethod: "/rpc.RetryService/Call"}, call)
		},
	}},
}
