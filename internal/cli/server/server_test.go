package server

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/calypr/funnel/api/config"
	appconfig "github.com/calypr/funnel/internal/config"
	"github.com/calypr/funnel/internal/config/configtest"
	"github.com/calypr/funnel/internal/database/boltdb"
	"github.com/calypr/funnel/internal/logger"
)

func TestPersistentPreRun(t *testing.T) {
	host := "test"
	rpcport := "9999"
	serverAddress := host + ":" + rpcport
	backend := "test-backend"

	fileConf := appconfig.DefaultConfig()
	tmp := configtest.WriteFile(t, fileConf, "testconfig.yaml")

	c, h := newCommandHooks()
	h.Run = func(ctx context.Context, conf *config.Config, log *logger.Logger) error {
		if appconfig.RPCAddress(conf.Server) != serverAddress {
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

func TestServerCloseReleasesBoltDatabase(t *testing.T) {
	conf := appconfig.DefaultConfig()
	conf.BoltDB.Path = filepath.Join(t.TempDir(), "tasks.db")
	parentCtx, cancelParent := context.WithCancel(context.Background())
	defer cancelParent()

	s, err := NewServer(parentCtx, conf, logger.NewLogger("test", logger.DefaultConfig()))
	if err != nil {
		t.Fatal(err)
	}

	s.Close()
	s.Close()
	if err := parentCtx.Err(); err != nil {
		t.Fatalf("Close canceled the caller context: %v", err)
	}

	reopened, err := boltdb.NewBoltDB(conf.BoltDB)
	if err != nil {
		t.Fatalf("reopen database after Close: %v", err)
	}
	reopened.Close()
}

func TestNewServerFailureReleasesBoltDatabase(t *testing.T) {
	conf := appconfig.DefaultConfig()
	conf.BoltDB.Path = filepath.Join(t.TempDir(), "tasks.db")
	conf.Compute = "invalid-backend"

	_, err := NewServer(context.Background(), conf, logger.NewLogger("test", logger.DefaultConfig()))
	if err == nil {
		t.Fatal("NewServer succeeded with an unknown compute backend")
	}

	reopened, err := boltdb.NewBoltDB(conf.BoltDB)
	if err != nil {
		t.Fatalf("reopen database after NewServer failed: %v", err)
	}
	reopened.Close()
}
