// Package middleware provides HTTP and gRPC middleware for the server.
package middleware

import (
	"context"
	"fmt"
	"runtime/debug"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/calypr/funnel/internal/logger"
	"github.com/calypr/funnel/version"
	"google.golang.org/grpc"
)

// DebugInterceptor logs each unary RPC and recovers downstream interceptor and handler panics.
func DebugInterceptor(log *logger.Logger) grpc.UnaryServerInterceptor {
	// Return a function that is the interceptor.
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler) (resp interface{}, err error) {
		defer func() {
			if value := recover(); value != nil {
				log.Error("caught RPC panic", "panic_type", fmt.Sprintf("%T", value), "method", info.FullMethod, "version", version.Version, "commit", version.GitCommit, "stack", string(debug.Stack()))
				resp, err = nil, status.Error(codes.Internal, "internal server error")
			}
		}()
		log.Debug("received request", "method", info.FullMethod)
		resp, err = handler(ctx, req)
		log.Debug("completed request", "method", info.FullMethod, "status", status.Code(err).String())

		return resp, err
	}
}
