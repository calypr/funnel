package plugin

import (
	"context"
	"errors"
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
	ctx, cancel := context.WithTimeout(context.Background(), taskPluginTimeout)
	defer cancel()
	return m.PluginActionContext(ctx, &Job{
		Params:  params,
		Headers: headers,
		Config:  config,
		Task:    task,
		Type:    actionType,
	})
}

func (m *RPCClient) PluginActionContext(ctx context.Context, req *Job) (*JobResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var resp JobResponse
	done := make(chan error, 1)
	go func() {
		done <- m.client.Call("Plugin.PluginAction", req, &resp)
	}()
	select {
	case err := <-done:
		return &resp, err
	case <-ctx.Done():
		// Essential: net/rpc cannot cancel one call. Closing the connection also releases a blocked transport write.
		return nil, errors.Join(ctx.Err(), m.client.Close())
	}
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
