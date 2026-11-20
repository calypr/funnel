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
		{"owner with proxy bearer scheme", "bearer alice", codes.OK},
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
				case "Bearer alice", "bearer alice":
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

func TestPluginAuthenticationFollowsReportedCapabilities(t *testing.T) {
	for _, test := range []struct {
		name         string
		capabilities *pluginapi.JobResponse
		err          error
		oidc         bool
		wantErr      bool
		wantPlugin   bool
	}{
		{name: "reports authenticate", capabilities: &pluginapi.JobResponse{Code: 200, Capabilities: []pluginapi.Type{pluginapi.Type_AUTHENTICATE}}, wantPlugin: true},
		{name: "rejects failed capability response", capabilities: &pluginapi.JobResponse{Code: 503, Capabilities: []pluginapi.Type{pluginapi.Type_AUTHENTICATE}}},
		{name: "reports nothing", capabilities: &pluginapi.JobResponse{}},
		{name: "predates capabilities", err: errors.New("unsupported task type: CAPABILITIES")},
		{name: "conflicts with oidc", capabilities: &pluginapi.JobResponse{Code: 200, Capabilities: []pluginapi.Type{pluginapi.Type_AUTHENTICATE}}, oidc: true, wantErr: true},
		{name: "task-only plugin with oidc", capabilities: &pluginapi.JobResponse{}, oidc: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			auth := NewAuthentication(nil, nil, identity.AccessOwner, nil)
			if test.oidc {
				auth.oidc = &OidcConfig{}
			}
			plugin := testAuthorizer(func(params map[string]string, headers map[string]*pluginapi.StringList, conf *config.Config, task *tes.Task, action pluginapi.Type) (*pluginapi.JobResponse, error) {
				if action != pluginapi.Type_CAPABILITIES || params["key"] != "value" || headers != nil || conf != nil || task != nil {
					t.Fatalf("unexpected startup request: %v", action)
				}
				return test.capabilities, test.err
			})
			err := auth.UsePluginIfSupported(plugin, map[string]string{"key": "value"})
			if (err != nil) != test.wantErr {
				t.Fatalf("error = %v, want error %v", err, test.wantErr)
			}
			if (auth.ValidateTaskAccess() == nil) != (test.wantPlugin || test.oidc) {
				t.Fatalf("owner access validation = %v", auth.ValidateTaskAccess())
			}
			if (auth.plugin != nil) != test.wantPlugin {
				t.Fatalf("plugin enabled = %v, want %v", auth.plugin != nil, test.wantPlugin)
			}
		})
	}
}

func TestBasicCredentialsWorkAlongsidePluginAuthentication(t *testing.T) {
	auth := NewAuthentication([]*config.BasicCredential{{User: "worker", Password: "secret"}}, nil, identity.AccessOwner, nil)
	auth.UsePlugin(testAuthorizer(func(_ map[string]string, headers map[string]*pluginapi.StringList, _ *config.Config, _ *tes.Task, action pluginapi.Type) (*pluginapi.JobResponse, error) {
		if action != pluginapi.Type_AUTHENTICATE {
			t.Fatalf("unexpected action %v", action)
		}
		return &pluginapi.JobResponse{Code: 200, UserId: "alice"}, nil
	}), nil)
	for header, want := range map[string]string{
		"Basic d29ya2VyOnNlY3JldA==": "worker",
		"Bearer token":               "alice",
	} {
		ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", header))
		_, err := auth.Interceptor(ctx, nil, &grpc.UnaryServerInfo{FullMethod: tesrpc.TaskService_GetTask_FullMethodName}, func(ctx context.Context, _ any) (any, error) {
			if got := identity.GetUserID(ctx); got != want {
				t.Fatalf("%s authenticated as %q, want %q", header, got, want)
			}
			return nil, nil
		})
		if err != nil {
			t.Fatalf("%s: %v", header, err)
		}
	}
}

func TestOwnerAccessRequiresAuthentication(t *testing.T) {
	for _, mode := range []string{identity.AccessOwner, identity.AccessOwnerOrAdmin, identity.AccessAll} {
		auth := NewAuthentication(nil, nil, mode, nil)
		if got := auth.ValidateTaskAccess() != nil; got != (mode != identity.AccessAll) {
			t.Errorf("%s rejected without credentials = %t", mode, got)
		}
		auth = NewAuthentication([]*config.BasicCredential{{User: "worker", Password: "fixture"}}, nil, mode, nil)
		if err := auth.ValidateTaskAccess(); err != nil {
			t.Errorf("%s rejected with Basic credentials: %v", mode, err)
		}
	}
}
