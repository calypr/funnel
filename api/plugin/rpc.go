package plugin

import (
	"fmt"
	"net/rpc"

	hplugin "github.com/hashicorp/go-plugin"

	funnelConfig "github.com/calypr/funnel/api/config"
	"github.com/calypr/funnel/api/tes"
	googleproto "google.golang.org/protobuf/proto"
)

// RPCClient is an implementation of Authorize that talks over RPC.
type RPCClient struct {
	client *rpc.Client
}

type RPCServer struct {
	Impl Authorize
}

func (m *RPCClient) PluginAction(params map[string]string, headers map[string]*StringList, config *funnelConfig.Config, task *tes.Task, actionType Type) (*JobResponse, error) {
	var resp JobResponse
	err := m.client.Call("Plugin.PluginAction", &Job{
		Params:  params,
		Headers: headers,
		Config:  config,
		Task:    task,
		Type:    actionType,
	}, &resp)
	return &resp, err
}

func (m *RPCServer) PluginAction(args *Job, resp *JobResponse) error {
	v, err := m.Impl.PluginAction(args.Params, args.Headers, args.Config, args.Task, args.Type)
	if err != nil {
		return fmt.Errorf("authorize implementation failed: %w", err)
	}
	googleproto.Merge(resp, v)
	return nil
}

// AuthorizePlugin connects the authorizer to HashiCorp go-plugin over net/rpc.
type AuthorizePlugin struct {
	Impl Authorize
}

func (p *AuthorizePlugin) Server(*hplugin.MuxBroker) (any, error) {
	return &RPCServer{Impl: p.Impl}, nil
}

func (*AuthorizePlugin) Client(b *hplugin.MuxBroker, c *rpc.Client) (any, error) {
	return &RPCClient{client: c}, nil
}
