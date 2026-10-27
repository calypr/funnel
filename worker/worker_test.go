package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/storage"
	"github.com/calypr/funnel/tes"
	"google.golang.org/protobuf/types/known/durationpb"
)

type pollingTaskReader struct {
	state func(context.Context) (tes.State, error)
}

func (r *pollingTaskReader) Task(context.Context) (*tes.Task, error) {
	return &tes.Task{Id: "task"}, nil
}

func (r *pollingTaskReader) State(ctx context.Context) (tes.State, error) {
	return r.state(ctx)
}

func (r *pollingTaskReader) Close() {}

func TestPollingRecordsTaskCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		worker := &DefaultWorker{
			Conf: &config.Worker{PollingRate: durationpb.New(time.Second)},
			TaskReader: &pollingTaskReader{state: func(context.Context) (tes.State, error) {
				return tes.State_CANCELED, nil
			}},
		}
		ctx, stop := worker.pollForCancel(context.Background())
		defer stop()
		<-ctx.Done()
		if !errors.Is(context.Cause(ctx), errTaskCanceled) {
			t.Fatalf("cancellation cause = %v, want task canceled", context.Cause(ctx))
		}
	})
}

func TestPollingStopWaitsForStateRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered := make(chan struct{})
		release := make(chan struct{})
		finished := make(chan struct{})
		worker := &DefaultWorker{
			Conf: &config.Worker{PollingRate: durationpb.New(time.Second)},
			TaskReader: &pollingTaskReader{state: func(ctx context.Context) (tes.State, error) {
				close(entered)
				<-ctx.Done()
				<-release
				close(finished)
				return tes.State_RUNNING, ctx.Err()
			}},
		}
		ctx, stop := worker.pollForCancel(context.Background())
		<-entered
		stopped := make(chan struct{})
		go func() {
			stop()
			close(stopped)
		}()
		synctest.Wait()
		select {
		case <-stopped:
			t.Error("polling stopped before the state read finished")
		default:
		}
		close(release)
		<-stopped
		select {
		case <-finished:
		default:
			t.Error("state read is still running")
		}
		if !errors.Is(context.Cause(ctx), context.Canceled) {
			t.Fatalf("stop cause = %v, want context canceled", context.Cause(ctx))
		}
	})
}

type startupTaskReader struct {
	task *tes.Task
}

func (r *startupTaskReader) Task(context.Context) (*tes.Task, error) { return r.task, nil }
func (r *startupTaskReader) State(context.Context) (tes.State, error) {
	return r.task.GetState(), nil
}
func (r *startupTaskReader) Close() {}

type startupEventWriter struct {
	events []*events.Event
}

func (w *startupEventWriter) WriteEvent(_ context.Context, event *events.Event) error {
	w.events = append(w.events, event)
	return nil
}
func (w *startupEventWriter) Close() {}

func TestWorkerRunsOnlyTasksInStartupStates(t *testing.T) {
	tests := []struct {
		name    string
		state   tes.State
		wantRun bool
	}{
		{name: "unknown local task", state: tes.State_UNKNOWN, wantRun: true},
		{name: "queued task", state: tes.State_QUEUED, wantRun: true},
		{name: "assigned task", state: tes.State_INITIALIZING, wantRun: true},
		{name: "already running task", state: tes.State_RUNNING},
		{name: "complete task", state: tes.State_COMPLETE},
		{name: "executor error task", state: tes.State_EXECUTOR_ERROR},
		{name: "system error task", state: tes.State_SYSTEM_ERROR},
		{name: "canceled task", state: tes.State_CANCELED},
		{name: "paused task", state: tes.State_PAUSED},
		{name: "unrecognized task state", state: tes.State(99)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			marker := filepath.Join(dir, "executor-ran")
			driver := filepath.Join(dir, "executor-driver")
			if err := os.WriteFile(driver, []byte("#!/bin/sh\nif [ \"$1\" = version ]; then printf '{\"Server\":\"1\",\"Client\":\"1\"}'; exit 0; fi\nif [ \"$1\" = run ]; then : > \"$TASK_RUN_MARKER\"; fi\nexit 0\n"), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("TASK_RUN_MARKER", marker)

			conf := config.DefaultConfig().Worker
			conf.WorkDir = dir
			conf.PollingRate = durationpb.New(time.Hour)
			conf.Container.DriverCommand = driver
			conf.Container.PullCommand = "pull"
			conf.Container.RunCommand = "run {{.Image}} {{.Command}}"
			task := &tes.Task{
				Id:        "startup-state-task",
				State:     tt.state,
				Executors: []*tes.Executor{{Image: "test", Command: []string{"true"}}},
			}
			eventWriter := &startupEventWriter{}
			worker := &DefaultWorker{
				Conf:        conf,
				Store:       &storage.Mux{},
				TaskReader:  &startupTaskReader{task: task},
				EventWriter: eventWriter,
			}
			if err := worker.Run(context.Background()); err != nil {
				t.Fatalf("Run() error = %v", err)
			}

			_, err := os.Stat(marker)
			didRun := err == nil
			if err != nil && !os.IsNotExist(err) {
				t.Fatalf("checking executor marker: %v", err)
			}
			if didRun != tt.wantRun {
				t.Fatalf("executor ran = %t, want %t", didRun, tt.wantRun)
			}
			if !tt.wantRun && len(eventWriter.events) != 0 {
				t.Fatalf("non-startup task emitted %d events, want none", len(eventWriter.events))
			}
		})
	}
}
