package local

import (
	"context"
	"testing"

	"github.com/calypr/funnel/api/config"
	pluginproto "github.com/calypr/funnel/api/plugin"
	"google.golang.org/protobuf/proto"
)

func TestPluginConfigIsPerRequest(t *testing.T) {
	b := createBackend(nil)
	b.conf.Plugins = &config.Plugins{}
	b.conf.Worker.Container.Env = map[string]string{"TOKEN": "deployment"}
	complete := proto.Clone(b.conf).(*config.Config)
	complete.Worker.Container.Env["TOKEN"] = "request-one"
	ctx := context.WithValue(t.Context(), "pluginResponse", &pluginproto.JobResponse{Config: complete})
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
	next := context.WithValue(t.Context(), "pluginResponse", &pluginproto.JobResponse{Config: proto.Clone(b.conf).(*config.Config)})
	got, err = b.configForRequest(next)
	if err != nil {
		t.Fatal(err)
	}
	if got.Worker.Container.Env["TOKEN"] != "deployment" {
		t.Error("later request inherited earlier credentials")
	}
}

func TestPluginConfigClearsWorkerSettings(t *testing.T) {
	b := createBackend(nil)
	b.conf.Plugins = &config.Plugins{}
	b.conf.Worker.LeaveWorkDir = true
	b.conf.Worker.ScratchPath = "/scratch"
	b.conf.Worker.MaxParallelTransfers = 5
	b.conf.Worker.Container.Env = map[string]string{"KEEP": "value", "REMOVE": "secret"}
	complete := proto.Clone(b.conf).(*config.Config)
	complete.Worker.LeaveWorkDir = false
	complete.Worker.ScratchPath = ""
	complete.Worker.MaxParallelTransfers = 0
	delete(complete.Worker.Container.Env, "REMOVE")
	ctx := context.WithValue(t.Context(), "pluginResponse", &pluginproto.JobResponse{Config: complete})
	got, err := b.configForRequest(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Worker.LeaveWorkDir || got.Worker.ScratchPath != "" || got.Worker.MaxParallelTransfers != 0 {
		t.Fatal("complete plugin configuration did not clear worker settings")
	}
	if len(got.Worker.Container.Env) != 1 || got.Worker.Container.Env["KEEP"] != "value" {
		t.Fatal("complete plugin configuration did not remove the environment entry")
	}
	if !b.conf.Worker.LeaveWorkDir || b.conf.Worker.ScratchPath != "/scratch" || b.conf.Worker.MaxParallelTransfers != 5 || b.conf.Worker.Container.Env["REMOVE"] != "secret" {
		t.Fatal("complete plugin configuration changed deployment defaults")
	}
}
