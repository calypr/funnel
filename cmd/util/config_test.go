package util

import (
	"testing"

	"github.com/ohsu-comp-bio/funnel/config"
)

func TestMergeConfigFileWithFlags(t *testing.T) {
	fileConfig := config.EmptyConfig()
	flagConf := &config.Config{
		Server: &config.Server{
			HostName: "test",
			RPCPort:  "9999",
		},
	}
	serverAddress := flagConf.Server.RPCAddress()

	result, err := MergeConfigFileWithFlags("", flagConf)
	if err != nil {
		t.Error("unexpected error", err)
	}
	if result.Server.RPCAddress() != serverAddress {
		t.Error("unexpected server address")
	}
	if result.Server.HTTPPort != fileConfig.Server.HTTPPort {
		t.Error("expected Config.Server.HTTPPort to equal the value from from config.DefaultValue()")
	}
	if result.RPCClient.ServerAddress != serverAddress {
		t.Error("unexpected Config.RPCClient.ServerAddress")
	}
	if result.Compute != fileConfig.Compute {
		t.Error("expected Config.Compute to equal default value from config.DefaultValue()")
	}

	fileConfig.Server.HTTPPort = "8888"
	tmp, cleanup := TempConfigFile(fileConfig, "testconfig.yaml")
	defer cleanup()
	result, err = MergeConfigFileWithFlags(tmp, flagConf)
	if err != nil {
		t.Error("unexpected error", err)
	}
	if result.Server.RPCAddress() != serverAddress {
		t.Error("unexpected server address")
	}
	if result.RPCClient.ServerAddress != serverAddress {
		t.Error("unexpected Config.RPCClient.ServerAddress")
	}
	if result.Server.HTTPPort != fileConfig.Server.HTTPPort {
		t.Error("expected Config.Server.HTTPPort to equal the value from the config file")
	}
	if result.Compute != fileConfig.Compute {
		t.Error("expected Config.Compute to equal default value from config.DefaultValue()")
	}
}
