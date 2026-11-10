package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/calypr/funnel/api/config"
	pluginapi "github.com/calypr/funnel/api/plugin"
	"github.com/calypr/funnel/api/tes"
	"github.com/calypr/funnel/internal/identity"
	"github.com/calypr/funnel/internal/tesrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type testAuthorizer func(map[string]string, map[string]*pluginapi.StringList, *config.Config, *tes.Task, pluginapi.Type) (*pluginapi.JobResponse, error)

func (f testAuthorizer) PluginAction(params map[string]string, headers map[string]*pluginapi.StringList, config *config.Config, task *tes.Task, action pluginapi.Type) (*pluginapi.JobResponse, error) {
	return f(params, headers, config, task, action)
}

func TestPluginBearerIdentity(t *testing.T) {
	for _, test := range []struct {
		name  string
		token string
		code  codes.Code
	}{
		{"owner", "Bearer alice", codes.OK},
		{"another user", "Bearer bob", codes.PermissionDenied},
		{"missing token", "", codes.Unauthenticated},
		{"rejected token", "Bearer invalid", codes.Unauthenticated},
		{"missing subject", "Bearer empty", codes.Unauthenticated},
		{"plugin unavailable", "Bearer unavailable", codes.Unavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			auth := NewAuthentication(nil, nil, identity.AccessOwner, nil)
			auth.UsePlugin(testAuthorizer(func(_ map[string]string, headers map[string]*pluginapi.StringList, conf *config.Config, task *tes.Task, action pluginapi.Type) (*pluginapi.JobResponse, error) {
				if action != pluginapi.Type_AUTHENTICATE || conf != nil || task != nil {
					t.Fatal("unexpected authentication request")
				}
				switch headers["authorization"].GetValues()[0] {
				case "Bearer alice":
					return &pluginapi.JobResponse{Code: 200, UserId: "alice"}, nil
				case "Bearer bob":
					return &pluginapi.JobResponse{Code: 200, UserId: "bob"}, nil
				case "Bearer empty":
					return &pluginapi.JobResponse{Code: 200}, nil
				case "Bearer unavailable":
					return nil, errors.New("upstream failure")
				default:
					return &pluginapi.JobResponse{Code: 401}, nil
				}
			}), nil)
			ctx := metadata.NewIncomingContext(context.Background(), metadata.MD{})
			if test.token != "" {
				ctx = metadata.NewIncomingContext(ctx, metadata.Pairs("authorization", test.token, "x-user-id", "alice"))
			}
			_, err := auth.Interceptor(ctx, nil, &grpc.UnaryServerInfo{FullMethod: tesrpc.TaskService_GetTask_FullMethodName}, func(ctx context.Context, _ any) (any, error) {
				if !identity.GetUser(ctx).IsAccessible("alice") {
					return nil, status.Error(codes.PermissionDenied, "task belongs to another user")
				}
				return nil, nil
			})
			if status.Code(err) != test.code {
				t.Fatalf("status = %v, want %v", err, test.code)
			}
		})
	}
}
