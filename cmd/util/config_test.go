package util

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/calypr/funnel/config"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestHTTPStorageTimeoutDefaultsAndOverrides(t *testing.T) {
	defaults, err := MergeConfigFileWithFlags("", config.EmptyConfig())
	if err != nil || !defaults.HTTPStorage.Timeout.GetDisabled() {
		t.Fatalf("expected disabled default timeout, got %v, %v", defaults.HTTPStorage.Timeout, err)
	}
	fileConfig := config.EmptyConfig()
	fileConfig.HTTPStorage.Timeout.TimeoutOption = &config.TimeoutConfig_Duration{Duration: durationpb.New(3 * time.Minute)}
	file, cleanup := TempConfigFile(fileConfig, "config.yaml")
	defer cleanup()
	merged, err := MergeConfigFileWithFlags(file, config.EmptyConfig())
	if err != nil || merged.HTTPStorage.Timeout.GetDuration().AsDuration() != 3*time.Minute {
		t.Fatalf("expected configured timeout, got %v, %v", merged.HTTPStorage.Timeout, err)
	}
	flags := config.EmptyConfig()
	flags.HTTPStorage.Timeout.TimeoutOption = &config.TimeoutConfig_Duration{Duration: durationpb.New(4 * time.Minute)}
	merged, err = MergeConfigFileWithFlags(file, flags)
	if err != nil || merged.HTTPStorage.Timeout.GetDuration().AsDuration() != 4*time.Minute {
		t.Fatalf("expected flag timeout, got %v, %v", merged.HTTPStorage.Timeout, err)
	}
}

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
	if got, want := result.Kubernetes.ForbiddenPathPrefixes, []string{"/dev", "/proc", "/sys", "/run", "/var/run"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("expected runtime deny-list defaults %v, got %v", want, got)
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

func TestMergeConfigRejectsTildeWorkDir(t *testing.T) {
	for _, source := range []string{"file", "flag"} {
		for _, workDir := range []string{"~", "~/work", "~other/work", "./work", "/tmp/work"} {
			t.Run(source+"/"+workDir, func(t *testing.T) {
				flags := config.EmptyConfig()
				file := ""
				if source == "file" {
					conf := config.EmptyConfig()
					conf.Worker.WorkDir = workDir
					var cleanup func()
					file, cleanup = TempConfigFile(conf, "config.yaml")
					defer cleanup()
				} else {
					flags.Worker.WorkDir = workDir
				}
				got, err := MergeConfigFileWithFlags(file, flags)
				if strings.HasPrefix(workDir, "~") {
					if err == nil || !strings.Contains(err.Error(), "Worker.WorkDir") || !strings.Contains(err.Error(), "not expanded") {
						t.Fatalf("expected actionable error, got %v", err)
					}
				} else if err != nil || got.Worker.WorkDir != workDir {
					t.Fatalf("work directory = %q, error = %v", got.Worker.WorkDir, err)
				}
			})
		}
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

	if got, want := result.Kubernetes.ForbiddenPathPrefixes, []string{"/secret"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("expected configured deny list %v, got %v", want, got)
	}
}
