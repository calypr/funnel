package plugin

import (
	"encoding/gob"
	"fmt"
	"net/rpc"

	hplugin "github.com/hashicorp/go-plugin"

	funnelConfig "github.com/calypr/funnel/api/config"
	"github.com/calypr/funnel/api/tes"
	googleproto "google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

// Gob needs concrete protobuf oneof types registered in both host and plugin
// processes for the net/rpc transport.
func init() {
	gob.Register(&funnelConfig.TimeoutConfig_Duration{})
	gob.Register(&funnelConfig.TimeoutConfig_Disabled{})
	gob.Register(&funnelConfig.S3MountedRead_SubPath{})
	gob.Register(&funnelConfig.S3MountedWrite_SubPath{})
	gob.Register(&structpb.Value_NullValue{})
	gob.Register(&structpb.Value_NumberValue{})
	gob.Register(&structpb.Value_StringValue{})
	gob.Register(&structpb.Value_BoolValue{})
	gob.Register(&structpb.Value_StructValue{})
	gob.Register(&structpb.Value_ListValue{})
}

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
