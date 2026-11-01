package local

import (
	"context"
	"testing"

	"github.com/calypr/funnel/config"
	pluginproto "github.com/calypr/funnel/plugins/proto"
)

func TestPluginConfigIsPerRequest(t *testing.T) {
	b := createBackend(nil)
	b.conf.Plugins = &config.Plugins{}
	b.conf.Worker.Container.Env = map[string]string{"TOKEN": "deployment"}
	ctx := context.WithValue(t.Context(), "pluginResponse", &pluginproto.JobResponse{Config: &config.Config{Worker: &config.Worker{Container: &config.ContainerConfig{Env: map[string]string{"TOKEN": "request-one"}}}}})
	got, err := b.configForRequest(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Worker.Container.Env["TOKEN"] != "request-one" {
		t.Error("request override was not applied")
	}
	if b.conf.Worker.Container.Env["TOKEN"] != "deployment" {
		t.Error("request override mutated deployment configuration")
	}
	next := context.WithValue(t.Context(), "pluginResponse", &pluginproto.JobResponse{})
	got, err = b.configForRequest(next)
	if err != nil {
		t.Fatal(err)
	}
	if got.Worker.Container.Env["TOKEN"] != "deployment" {
		t.Error("later request inherited earlier credentials")
	}
}
