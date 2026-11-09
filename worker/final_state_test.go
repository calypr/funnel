package worker_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/database/boltdb"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/storage"
	"github.com/calypr/funnel/tes"
	"github.com/calypr/funnel/worker"
)

type rejectFinalStateWriter struct {
	events.Writer
	err   error
	state tes.State
}

func (w *rejectFinalStateWriter) WriteEvent(ctx context.Context, event *events.Event) error {
	if event.Type == events.Type_TASK_STATE && event.GetState() == w.state {
		return w.err
	}
	return w.Writer.WriteEvent(ctx, event)
}

func TestWorkerReportsFinalStateWriteFailure(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	driver := filepath.Join(dir, "executor")
	if err := os.WriteFile(driver, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	conf := config.DefaultConfig()
	conf.Worker.WorkDir = dir
	conf.Worker.Container.DriverCommand = driver
	conf.Worker.Container.PullCommand = "pull"
	conf.Worker.Container.RunCommand = "run {{.Image}} {{.Command}}"
	conf.BoltDB.Path = filepath.Join(dir, "tasks.db")
	db, err := boltdb.NewBoltDB(conf.BoltDB)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Init(); err != nil {
		t.Fatal(err)
	}
	task := &tes.Task{Id: "final-state-task", State: tes.Queued, Executors: []*tes.Executor{{Image: "test", Command: []string{"true"}}}}
	if err := db.WriteEvent(ctx, events.NewTaskCreated(task)); err != nil {
		t.Fatal(err)
	}
	writeErr := errors.New("task state database unavailable")
	runner := &worker.DefaultWorker{Conf: conf.Worker, Store: &storage.Mux{}, TaskReader: worker.NewGenericTaskReader(db.GetTask, task.Id, func() {}), ExecutionClaimer: db, EventWriter: &rejectFinalStateWriter{Writer: db, err: writeErr, state: tes.Complete}}
	err = runner.Run(ctx)
	if !errors.Is(err, writeErr) || !strings.Contains(err.Error(), "COMPLETE") {
		t.Fatalf("worker error = %v, want COMPLETE write failure", err)
	}
	got, err := db.GetTask(ctx, &tes.GetTaskRequest{Id: task.Id, View: tes.View_FULL.String()})
	if err != nil {
		t.Fatal(err)
	}
	if got.State != tes.Running {
		t.Fatalf("stored task state = %s, want RUNNING after rejected update", got.State)
	}
}

func TestWorkerPreservesExecutorErrorWhenFinalStateWriteFails(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	driver := filepath.Join(dir, "executor")
	if err := os.WriteFile(driver, []byte("#!/bin/sh\nexit 7\n"), 0700); err != nil {
		t.Fatal(err)
	}
	conf := config.DefaultConfig()
	conf.Worker.WorkDir = dir
	conf.Worker.Container.DriverCommand = driver
	conf.Worker.Container.PullCommand = "pull"
	conf.Worker.Container.RunCommand = "run {{.Image}} {{.Command}}"
	conf.BoltDB.Path = filepath.Join(dir, "tasks.db")
	db, err := boltdb.NewBoltDB(conf.BoltDB)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Init(); err != nil {
		t.Fatal(err)
	}
	task := &tes.Task{Id: "executor-error-task", State: tes.Queued, Executors: []*tes.Executor{{Image: "test", Command: []string{"true"}}}}
	if err := db.WriteEvent(ctx, events.NewTaskCreated(task)); err != nil {
		t.Fatal(err)
	}
	writeErr := errors.New("task state database unavailable")
	runner := &worker.DefaultWorker{Conf: conf.Worker, Store: &storage.Mux{}, TaskReader: worker.NewGenericTaskReader(db.GetTask, task.Id, func() {}), ExecutionClaimer: db, EventWriter: &rejectFinalStateWriter{Writer: db, err: writeErr, state: tes.ExecutorError}}
	err = runner.Run(ctx)
	var exitErr *exec.ExitError
	if !errors.Is(err, writeErr) || !errors.As(err, &exitErr) || exitErr.ExitCode() != 7 {
		t.Fatalf("worker error = %v, want executor exit 7 and final state write failure", err)
	}
}
