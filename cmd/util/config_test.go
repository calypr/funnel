package util

import (
	"testing"

	"github.com/ohsu-comp-bio/funnel/config"
)

func TestMergeConfigFileWithFlags(t *testing.T) {
	fileConfig := config.DefaultConfig()
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
	if result.Server == nil {
		t.Fatal("unexpected nil Server config")
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
	if len(result.Kubernetes.ForbiddenPathPrefixes) != 0 {
		t.Fatalf("expected no runtime deny-list defaults, got %v", result.Kubernetes.ForbiddenPathPrefixes)
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

func TestMergeConfigFileReplacesForbiddenPathPrefixes(t *testing.T) {
	fileConfig := config.DefaultConfig()
	fileConfig.Kubernetes.ForbiddenPathPrefixes = []string{"/secret"}
	tmp, cleanup := TempConfigFile(fileConfig, "testconfig.yaml")
	defer cleanup()

	result, err := MergeConfigFileWithFlags(tmp, config.EmptyConfig())
	if err != nil {
		t.Fatal("unexpected error", err)
	}

	if got, want := result.Kubernetes.ForbiddenPathPrefixes, []string{"/secret"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("expected configured deny list %v, got %v", want, got)
	}
}
