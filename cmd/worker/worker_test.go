package worker

import (
	"context"
	"os"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/ohsu-comp-bio/funnel/cmd/util"
	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/logger"
)

func TestPersistentPreRun(t *testing.T) {
	host := "test"
	rpcport := "9999"

	cwd, _ := os.Getwd()
	workDir := path.Join(cwd, "funnel-work-dir")

	fileConf := config.DefaultConfig()
	tmp, cleanup := util.TempConfigFile(fileConf, "testconfig.yaml")
	defer cleanup()

	c, h := newCommandHooks()
	h.Run = func(ctx context.Context, conf *config.Config, log *logger.Logger, opts *Options) error {
		if conf.Server.HostName != host {
			t.Fatal("unexpected Server.HostName in config", conf.Server.HostName)
		}
		if conf.Server.RPCPort != rpcport {
			t.Fatal("unexpected Server.RPCAddress in config", conf.Server.RPCPort)
		}
		if duration := conf.RPCClient.Timeout.GetDuration(); duration == nil || duration.AsDuration() != 10*time.Millisecond {
			t.Fatalf("unexpected rpc client timeout in server config: got %v, want 10ms", duration)
		}
		if conf.Worker.WorkDir != workDir {
			t.Fatal("unexpected Worker.WorkDir in config", conf.Worker.WorkDir)
		}
		return nil
	}

	c.SetArgs([]string{
		"run", "--config", tmp, "--Server.HostName", "test",
		"--Server.RPCPort", "9999", "--RPCClient.Timeout", "10ms",
		"--taskID", "test1234",
	})
	err := c.Execute()
	if err != nil {
		// since there is no server the rpc event writer will fail to connect
		// within its context deadline. We can ignore this error
		if !strings.Contains(err.Error(), "failed to instantiate EventWriter: context deadline exceeded") {
			t.Fatal(err)
		}
	}
}

func TestTaskFileOption(t *testing.T) {
	c, h := newCommandHooks()
	h.Run = func(ctx context.Context, conf *config.Config, log *logger.Logger, opts *Options) error {
		if opts.TaskFile != "test.task.json" {
			t.Fatal("unexpected task file option", opts.TaskFile)
		}
		return nil
	}

	c.SetArgs([]string{"run", "--taskFile", "test.task.json"})
	c.Execute()

	h.Run = func(ctx context.Context, conf *config.Config, log *logger.Logger, opts *Options) error {
		if opts.TaskFile != "test.task.json" {
			t.Fatal("unexpected task file option", opts.TaskFile)
		}
		return nil
	}

	c.SetArgs([]string{"run", "-f", "test.task.json"})
	c.Execute()

	h.Run = func(ctx context.Context, conf *config.Config, log *logger.Logger, opts *Options) error {
		if opts.TaskBase64 != "abcd" {
			t.Fatal("unexpected task base64 option", opts.TaskBase64)
		}
		return nil
	}

	c.SetArgs([]string{"run", "--taskBase64", "abcd"})
	c.Execute()

	h.Run = func(ctx context.Context, conf *config.Config, log *logger.Logger, opts *Options) error {
		if opts.TaskBase64 != "abcd" {
			t.Fatal("unexpected task base64 option", opts.TaskBase64)
		}
		return nil
	}

	c.SetArgs([]string{"run", "-b", "abcd"})
	c.Execute()
}
