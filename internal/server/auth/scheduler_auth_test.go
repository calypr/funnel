package auth

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/calypr/funnel/api/config"
	"github.com/calypr/funnel/internal/identity"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestSchedulerMutationsRequireAdministrator(t *testing.T) {
	for _, method := range []string{"/scheduler.SchedulerService/PutNode", "/scheduler.SchedulerService/DeleteNode"} {
		for _, admin := range []bool{false, true} {
			auth := NewAuthentication([]*config.BasicCredential{{User: "user", Password: "synthetic", Admin: admin}}, nil, identity.AccessAll, nil)
			ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("user:synthetic"))))
			called := false
			_, err := auth.Interceptor(ctx, nil, &grpc.UnaryServerInfo{FullMethod: method}, func(context.Context, interface{}) (interface{}, error) { called = true; return nil, nil })
			if admin {
				if err != nil || !called {
					t.Fatalf("administrator %s: called=%v err=%v", method, called, err)
				}
			} else if status.Code(err) != codes.PermissionDenied || called {
				t.Fatalf("non-admin %s: called=%v err=%v", method, called, err)
			}
		}
	}
}
