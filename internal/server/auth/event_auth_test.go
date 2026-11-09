package auth

import (
	"context"
	"testing"

	"github.com/calypr/funnel/api/tes"
	"github.com/calypr/funnel/internal/events"
	"github.com/calypr/funnel/internal/identity"
	"github.com/calypr/funnel/internal/tesrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type ownerTaskService struct {
	tesrpc.UnimplementedTaskServiceServer
}

func (ownerTaskService) GetTask(ctx context.Context, req *tes.GetTaskRequest) (*tes.Task, error) {
	if !identity.GetUser(ctx).IsAccessible("alice") {
		return nil, status.Error(codes.PermissionDenied, "not task owner")
	}
	return &tes.Task{Id: req.Id}, nil
}

func TestEventWritesRespectTaskAccess(t *testing.T) {
	interceptor := EventWriteInterceptor(ownerTaskService{})
	for _, user := range []string{"alice", "bob"} {
		ctx := identity.WithUser(context.Background(), &identity.UserInfo{Username: user, AccessMode: identity.AccessOwner})
		called := false
		_, err := interceptor(ctx, events.NewSystemLog("task", 0, 0, "info", "synthetic", nil), &grpc.UnaryServerInfo{FullMethod: events.EventService_WriteEvent_FullMethodName}, func(context.Context, interface{}) (interface{}, error) { called = true; return nil, nil })
		if user == "alice" {
			if err != nil || !called {
				t.Fatalf("owner denied: %v", err)
			}
		} else if status.Code(err) != codes.PermissionDenied || called {
			t.Fatalf("cross-owner event accepted: %v called=%v", err, called)
		}
	}
}
