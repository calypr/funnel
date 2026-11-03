package local

import (
	"context"
	"testing"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/logger"
	plugin "github.com/calypr/funnel/plugins/proto"
)

func TestPluginCredentialsDoNotChangeSharedBackendConfiguration(t *testing.T) {
	conf := config.DefaultConfig()
	conf.Plugins = &config.Plugins{Path: "test-plugin"}
	conf.RPCClient.Credential = &config.BasicCredential{User: "worker", Password: "deployment-secret"}
	backend, err := NewBackend(context.Background(), conf, logger.NewLogger("test", logger.DefaultConfig()))
	if err != nil {
		t.Fatal(err)
	}
	response := &plugin.JobResponse{Config: &config.Config{RPCClient: &config.RPCClient{Credential: &config.BasicCredential{User: "alice", Password: "alice-secret"}}}}
	ctx := context.WithValue(context.Background(), "pluginResponse", response)
	err = backend.WriteEvent(ctx, events.NewSystemLog("task", 0, 0, "info", "synthetic", nil))
	if err != nil {
		t.Fatal(err)
	}
	if conf.RPCClient.Credential.Password != "deployment-secret" {
		t.Fatal("plugin credentials changed shared backend configuration")
	}
}
