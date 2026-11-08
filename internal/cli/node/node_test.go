package node

import (
	"context"
	"os"
	"path"
	"testing"

	"github.com/calypr/funnel/config"
	appconfig "github.com/calypr/funnel/internal/config"
	"github.com/calypr/funnel/internal/logger"
	"github.com/calypr/funnel/tests/configtest"
)

func TestPersistentPreRun(t *testing.T) {
	host := "test"
	rpcport := "9999"

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	workDir := path.Join(cwd, "funnel-work-dir")

	fileConf := appconfig.EmptyConfig()
	tmp := configtest.WriteFile(t, fileConf, "testconfig.yaml")

	c, h := newCommandHooks()
	h.Run = func(ctx context.Context, conf *config.Config, log *logger.Logger) error {
		if conf.Server.HostName != host {
			t.Fatal("unexpected Server.HostName in config", conf.Server.HostName)
		}
		if conf.Server.RPCPort != rpcport {
			t.Fatal("unexpected Server.RPCAddress in config", conf.Server.RPCPort)
		}
		if conf.Worker.WorkDir != workDir {
			t.Fatal("unexpected Worker.WorkDir in config", conf.Worker.WorkDir)
		}

		return nil
	}

	c.SetArgs([]string{"run", "--config", tmp, "--Server.HostName", host, "--Server.RPCPort", rpcport})
	err = c.Execute()
	if err != nil {
		t.Fatal(err)
	}
}
