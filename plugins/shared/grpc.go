package shared

import (
	"context"

	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/plugins/proto"
	"github.com/ohsu-comp-bio/funnel/tes"
)

// GRPCClient is an implementation of KV that talks over RPC.
type GRPCClient struct{ client proto.AuthorizeClient }

type GRPCServer struct {
	// This is the real implementation
	Impl Authorize
}

func (m *GRPCClient) PluginAction(headers map[string]*proto.StringList, params map[string]string, config *config.Config, task *tes.Task, actionType proto.Type) (*proto.JobResponse, error) {
	return m.client.PluginAction(context.Background(), &proto.Job{
		Headers: headers,
		Params:  params,
		Config:  config,
		Task:    task,
		Type:    actionType,
	})
}

func (m *GRPCServer) PluginAction(ctx context.Context, req *proto.Job) (*proto.JobResponse, error) {
	return m.Impl.PluginAction(req.Params, req.Headers, req.Config, req.Task, req.Type)
}
