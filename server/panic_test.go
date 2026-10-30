package server

import (
	"context"
	"testing"

	"github.com/calypr/funnel/logger"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestRPCPanicReturnsInternal(t *testing.T) {
	interceptor := newDebugInterceptor(logger.NewLogger("panic-test", logger.DefaultConfig()))
	var err error
	var escaped any
	func() {
		defer func() { escaped = recover() }()
		_, err = interceptor(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: "/test/Panic"}, func(context.Context, any) (any, error) { panic("private panic detail") })
	}()
	if escaped != nil {
		t.Fatalf("panic escaped RPC boundary: %v", escaped)
	}
	if status.Code(err) != codes.Internal {
		t.Fatalf("got %v, want Internal", err)
	}
	if status.Convert(err).Message() != "internal server error" {
		t.Fatalf("unexpected public error: %v", err)
	}
	_, err = interceptor(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: "/test/Healthy"}, func(context.Context, any) (any, error) { return "ok", nil })
	if err != nil {
		t.Fatalf("subsequent request failed: %v", err)
	}
}
