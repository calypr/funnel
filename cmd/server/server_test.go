package server

import (
	"context"
	"testing"

	"github.com/ohsu-comp-bio/funnel/cmd/util"
	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/logger"
)

func TestPersistentPreRun(t *testing.T) {
	host := "test"
	rpcport := "9999"
	serverAddress := host + ":" + rpcport
	backend := "test-backend"

	fileConf := config.DefaultConfig()
	tmp, cleanup := util.TempConfigFile(fileConf, "testconfig.yaml")
	defer cleanup()

	c, h := newCommandHooks()
	h.Run = func(ctx context.Context, conf *config.Config, log *logger.Logger) error {
		if conf.Server.RPCAddress() != serverAddress {
			t.Fatal("unexpected hostname or rpc port in server config")
		}
		if conf.Server.HTTPPort != fileConf.Server.HTTPPort {
			t.Fatal("unexpected http port in server config")
		}
		if conf.RPCClient.Timeout.GetDuration().AsDuration() != 1000000000 {
			t.Fatal("unexpected rpc client timeout in server config")
		}
		if conf.Scheduler.NodePingTimeout.GetDuration().AsDuration() != 60000000000 {
			t.Fatal("unexpected node ping timeout in scheduler config")
		}
		if conf.Compute != backend {
			t.Fatal("unexpected Backend in config")
		}
		return nil
	}

	c.SetArgs([]string{
		"run", "--config", tmp,
		"--Server.HostName", host, "--Server.RPCPort", rpcport,
		"--RPCClient.Timeout", "1s", "--Compute", backend,
	})
	err := c.Execute()
	if err != nil {
		t.Fatal(err)
	}
}
