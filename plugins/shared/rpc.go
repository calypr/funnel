package shared

import (
	"fmt"
	"net/rpc"

	funnelConfig "github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/plugins/proto"
	"github.com/ohsu-comp-bio/funnel/tes"
)

// RPCClient is an implementation of Authorize that talks over RPC.
type RPCClient struct {
	client *rpc.Client
}

type RPCServer struct {
	// This is the real implementation
	Impl Authorize
}

func (m *RPCClient) PluginAction(params map[string]string, headers map[string]*proto.StringList, config *funnelConfig.Config, task *tes.Task, actionType proto.Type) (*proto.JobResponse, error) {
	var resp proto.JobResponse
	err := m.client.Call("Plugin.PluginAction", &proto.Job{
		Params:  params,
		Headers: headers,
		Config:  config,
		Task:    task,
		Type:    actionType,
	}, &resp)
	return &resp, err
}

func (m *RPCServer) PluginAction(args *proto.Job, resp *proto.JobResponse) error {
	// Call the implementation's Get method with the arguments
	v, err := m.Impl.PluginAction(args.Params, args.Headers, args.Config, args.Task, args.Type)
	if err != nil {
		return fmt.Errorf("authorize implementation failed: %w", err)
	}
	*resp = *v
	return nil
}
