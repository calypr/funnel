package plugin

import (
	"context"

	hplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/calypr/funnel/api/config"
	"github.com/calypr/funnel/api/tes"
)

// GRPCClient invokes an authorizer over gRPC.
type GRPCClient struct{ client AuthorizeClient }

type GRPCServer struct {
	Impl Authorize
}

func (m *GRPCClient) PluginAction(params map[string]string, headers map[string]*StringList, config *config.Config, task *tes.Task, actionType Type) (*JobResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), taskPluginTimeout)
	defer cancel()
	return m.PluginActionContext(ctx, &Job{
		Headers: headers,
		Params:  params,
		Config:  config,
		Task:    task,
		Type:    actionType,
	})
}

func (m *GRPCClient) PluginActionContext(ctx context.Context, req *Job) (*JobResponse, error) {
	resp, err := m.client.PluginAction(ctx, req)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if status.Code(err) == codes.DeadlineExceeded {
		return nil, context.DeadlineExceeded
	}
	return resp, err
}

func (m *GRPCServer) PluginAction(ctx context.Context, req *Job) (*JobResponse, error) {
	return m.Impl.PluginAction(req.Params, req.Headers, req.Config, req.Task, req.Type)
}

// AuthorizeGRPCPlugin connects the authorizer to HashiCorp go-plugin over gRPC.
type AuthorizeGRPCPlugin struct {
	// GRPCPlugin must still implement the Plugin interface
	hplugin.Plugin
	Impl Authorize
}

func (p *AuthorizeGRPCPlugin) GRPCServer(broker *hplugin.GRPCBroker, s *grpc.Server) error {
	RegisterAuthorizeServer(s, &GRPCServer{Impl: p.Impl})
	return nil
}

func (p *AuthorizeGRPCPlugin) GRPCClient(ctx context.Context, broker *hplugin.GRPCBroker, c *grpc.ClientConn) (any, error) {
	return &GRPCClient{client: NewAuthorizeClient(c)}, nil
}
