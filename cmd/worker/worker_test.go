package worker

import (
	"context"
	"os"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/calypr/funnel/cmd/util"
	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/logger"
	"github.com/calypr/funnel/tes"
	funnelworker "github.com/calypr/funnel/worker"
)

func TestPersistentPreRun(t *testing.T) {
	host := "test"
	rpcport := "9999"

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
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
	err = c.Execute()
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
	if err := c.Execute(); err != nil {
		t.Fatal(err)
	}

	h.Run = func(ctx context.Context, conf *config.Config, log *logger.Logger, opts *Options) error {
		if opts.TaskFile != "test.task.json" {
			t.Fatal("unexpected task file option", opts.TaskFile)
		}
		return nil
	}

	c.SetArgs([]string{"run", "-f", "test.task.json"})
	if err := c.Execute(); err != nil {
		t.Fatal(err)
	}

	h.Run = func(ctx context.Context, conf *config.Config, log *logger.Logger, opts *Options) error {
		if opts.TaskBase64 != "abcd" {
			t.Fatal("unexpected task base64 option", opts.TaskBase64)
		}
		return nil
	}

	c.SetArgs([]string{"run", "--taskBase64", "abcd"})
	if err := c.Execute(); err != nil {
		t.Fatal(err)
	}

	h.Run = func(ctx context.Context, conf *config.Config, log *logger.Logger, opts *Options) error {
		if opts.TaskBase64 != "abcd" {
			t.Fatal("unexpected task base64 option", opts.TaskBase64)
		}
		return nil
	}

	c.SetArgs([]string{"run", "-b", "abcd"})
	if err := c.Execute(); err != nil {
		t.Fatal(err)
	}
}

func TestNewTaskReaderRejectsUnknownDatabase(t *testing.T) {
	_, err := newTaskReader(context.Background(), &config.Config{Database: "elastic"}, &Options{})
	if err == nil || !strings.Contains(err.Error(), "unknown database: 'elastic'") {
		t.Fatalf("newTaskReader error = %v, want unknown database error", err)
	}
}

type readOnlyDatabaseWithoutClaim struct{}

func (readOnlyDatabaseWithoutClaim) ListTasks(context.Context, *tes.ListTasksRequest) (*tes.ListTasksResponse, error) {
	return nil, nil
}

func (readOnlyDatabaseWithoutClaim) GetTask(context.Context, *tes.GetTaskRequest) (*tes.Task, error) {
	return &tes.Task{Id: "task"}, nil
}

func (readOnlyDatabaseWithoutClaim) Close() {}

type readOnlyDatabaseWithClaim struct {
	claimed bool
	taskID  string
}

func (db *readOnlyDatabaseWithClaim) ListTasks(context.Context, *tes.ListTasksRequest) (*tes.ListTasksResponse, error) {
	return nil, nil
}

func (db *readOnlyDatabaseWithClaim) GetTask(context.Context, *tes.GetTaskRequest) (*tes.Task, error) {
	return &tes.Task{Id: db.taskID}, nil
}

func (db *readOnlyDatabaseWithClaim) ClaimExecution(_ context.Context, taskID string, _ *events.ExecutionOwner) (bool, error) {
	return taskID == db.taskID && db.claimed, nil
}

func (db *readOnlyDatabaseWithClaim) Close() {}

func TestDatabaseTaskReaderFailsClosedWithoutClaims(t *testing.T) {
	reader, err := newDatabaseTaskReader("task", "datastore", readOnlyDatabaseWithoutClaim{}, nil)
	if err != nil {
		t.Fatalf("newDatabaseTaskReader() error = %v", err)
	}
	claimer, ok := reader.(funnelworker.ExecutionClaimer)
	if !ok {
		t.Fatal("database reader does not expose the execution claim boundary")
	}
	claimed, err := claimer.ClaimExecution(context.Background(), "task", nil)
	if err == nil || !strings.Contains(err.Error(), `database "datastore" does not support execution claims`) {
		t.Fatalf("ClaimExecution() error = %v, want explicit unsupported-backend error", err)
	}
	if claimed {
		t.Fatal("ClaimExecution() = true for unsupported database")
	}
}

func TestDatabaseTaskReaderDelegatesExecutionClaim(t *testing.T) {
	db := &readOnlyDatabaseWithClaim{taskID: "task", claimed: true}
	reader, err := newDatabaseTaskReader("task", "postgres", db, nil)
	if err != nil {
		t.Fatalf("newDatabaseTaskReader() error = %v", err)
	}
	claimer, ok := reader.(funnelworker.ExecutionClaimer)
	if !ok {
		t.Fatal("database reader does not expose its execution claimer")
	}
	claimed, err := claimer.ClaimExecution(context.Background(), "task", nil)
	if err != nil || !claimed {
		t.Fatalf("ClaimExecution() = %t, %v, want true, nil", claimed, err)
	}
}
