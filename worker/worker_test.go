package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	task  *tes.Task
	state func(context.Context) (tes.State, error)
}

func (r *startupTaskReader) Task(context.Context) (*tes.Task, error) { return r.task, nil }
func (r *startupTaskReader) State(ctx context.Context) (tes.State, error) {
	if r.state != nil {
		return r.state(ctx)
	}
	return r.task.GetState(), nil
}
func (r *startupTaskReader) Close() {}

type startupEventWriter struct {
	events []*events.Event
}

type orderedEventWriter struct {
	startupEventWriter
	mu            sync.Mutex
	orderPath     string
	rejectRunning error
}

func (w *orderedEventWriter) WriteEvent(ctx context.Context, event *events.Event) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if event.Type == events.Type_TASK_STATE {
		if event.GetState() == tes.State_RUNNING && w.rejectRunning != nil {
			return w.rejectRunning
		}
		if w.orderPath != "" {
			f, err := os.OpenFile(w.orderPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
			if err != nil {
				return err
			}
			_, writeErr := fmt.Fprintln(f, "state:"+event.GetState().String())
			closeErr := f.Close()
			if writeErr != nil {
				return writeErr
			}
			if closeErr != nil {
				return closeErr
			}
		}
	}
	return w.startupEventWriter.WriteEvent(ctx, event)
}

func (w *orderedEventWriter) snapshotEvents() []*events.Event {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]*events.Event(nil), w.events...)
}

type workerTestStorage struct {
	storage.Storage
	downloadStarted chan struct{}
	releaseDownload <-chan struct{}
}

func (s *workerTestStorage) Get(ctx context.Context, url, path string) (*storage.Object, error) {
	if s.downloadStarted != nil {
		close(s.downloadStarted)
	}
	if s.releaseDownload != nil {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-s.releaseDownload:
		}
	}
	if err := os.WriteFile(path, []byte("input"), 0600); err != nil {
		return nil, err
	}
	return &storage.Object{URL: url, Name: filepath.Base(path)}, nil
}

func (s *workerTestStorage) UnsupportedOperations(string) storage.UnsupportedOperations {
	return storage.AllSupported()
}

func writeWorkerTestDriver(t *testing.T) string {
	t.Helper()
	driver := filepath.Join(t.TempDir(), "executor-driver")
	script := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"version) printf '{\"Server\":\"1\",\"Client\":\"1\"}' ;;\n" +
		"run) printf 'launch\\n' >> \"$TASK_ORDER_FILE\" ;;\n" +
		"esac\n"
	if err := os.WriteFile(driver, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return driver
}

func workerTaskStates(evs []*events.Event) []tes.State {
	var states []tes.State
	for _, event := range evs {
		if event.Type == events.Type_TASK_STATE {
			states = append(states, event.GetState())
		}
	}
	return states
}

func workerOrder(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

type executionClaimFunc func(context.Context, string) (bool, error)

func (f executionClaimFunc) ClaimExecution(ctx context.Context, taskID string, _ *events.ExecutionOwner) (bool, error) {
	return f(ctx, taskID)
}

type grantExecutionClaim struct{}

func (grantExecutionClaim) ClaimExecution(context.Context, string, *events.ExecutionOwner) (bool, error) {
	return true, nil
}

func (w *startupEventWriter) WriteEvent(_ context.Context, event *events.Event) error {
	w.events = append(w.events, event)
	return nil
}
func (w *startupEventWriter) Close() {}

func (w *orderedEventWriter) Close() {}

func TestWorkerRemainsInitializingDuringInputDownload(t *testing.T) {
	dir := t.TempDir()
	orderPath := filepath.Join(dir, "order")
	t.Setenv("TASK_ORDER_FILE", orderPath)
	conf := config.DefaultConfig().Worker
	conf.WorkDir = dir
	conf.PollingRate = durationpb.New(time.Hour)
	conf.Container.DriverCommand = writeWorkerTestDriver(t)
	conf.Container.PullCommand = "pull {{.Image}}"
	conf.Container.RunCommand = "run {{.Image}}"
	task := &tes.Task{
		Id:    "preparing-task",
		State: tes.State_INITIALIZING,
		Inputs: []*tes.Input{{
			Url:  "test://input.txt",
			Path: "/inputs/sub/input.txt",
			Type: tes.FileType_FILE,
		}},
		Executors: []*tes.Executor{{Image: "test", Command: []string{"true"}}},
	}
	downloadStarted := make(chan struct{})
	releaseDownload := make(chan struct{})
	writer := &orderedEventWriter{orderPath: orderPath}
	worker := &DefaultWorker{
		Conf:             conf,
		Store:            &workerTestStorage{downloadStarted: downloadStarted, releaseDownload: releaseDownload},
		TaskReader:       &startupTaskReader{task: task},
		ExecutionClaimer: grantExecutionClaim{},
		EventWriter:      writer,
	}
	done := make(chan error, 1)
	go func() { done <- worker.Run(context.Background()) }()
	select {
	case <-downloadStarted:
	case err := <-done:
		t.Fatalf("Run() finished before input download started: %v", err)
	}
	state, err := worker.TaskReader.State(context.Background())
	if err != nil || state != tes.State_INITIALIZING {
		t.Fatalf("task state during input download = %s, error = %v, want INITIALIZING", state, err)
	}
	if got := workerTaskStates(writer.snapshotEvents()); len(got) != 0 {
		t.Fatalf("state events during input download = %v, want none", got)
	}
	close(releaseDownload)
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := workerTaskStates(writer.snapshotEvents()); len(got) != 2 || got[0] != tes.State_RUNNING || got[1] != tes.State_COMPLETE {
		t.Fatalf("task states = %v, want [RUNNING COMPLETE]", got)
	}
}

func TestWorkerMarksRunningOnceBeforeFirstExecutorDispatch(t *testing.T) {
	dir := t.TempDir()
	orderPath := filepath.Join(dir, "order")
	t.Setenv("TASK_ORDER_FILE", orderPath)
	conf := config.DefaultConfig().Worker
	conf.WorkDir = dir
	conf.PollingRate = durationpb.New(time.Hour)
	conf.Container.DriverCommand = writeWorkerTestDriver(t)
	conf.Container.PullCommand = "pull {{.Image}}"
	conf.Container.RunCommand = "run {{.Image}}"
	task := &tes.Task{
		Id:    "two-executor-task",
		State: tes.State_UNKNOWN,
		Executors: []*tes.Executor{
			{Image: "first", Command: []string{"true"}},
			{Image: "second", Command: []string{"true"}},
		},
	}
	writer := &orderedEventWriter{orderPath: orderPath}
	worker := &DefaultWorker{
		Conf:        conf,
		TaskReader:  &FileTaskReader{task: task},
		EventWriter: writer,
	}
	if err := worker.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	wantOrder := []string{"state:INITIALIZING", "state:RUNNING", "launch", "launch", "state:COMPLETE"}
	if got := workerOrder(t, orderPath); len(got) != len(wantOrder) {
		t.Fatalf("event and executor order = %v, want %v", got, wantOrder)
	} else {
		for i := range wantOrder {
			if got[i] != wantOrder[i] {
				t.Fatalf("event and executor order = %v, want %v", got, wantOrder)
			}
		}
	}
	if got := workerTaskStates(writer.snapshotEvents()); len(got) != 3 || got[0] != tes.State_INITIALIZING || got[1] != tes.State_RUNNING || got[2] != tes.State_COMPLETE {
		t.Fatalf("task states = %v, want [INITIALIZING RUNNING COMPLETE]", got)
	}
}

func TestWorkerDoesNotMarkRunningWhenExecutorLogPreparationFails(t *testing.T) {
	dir := t.TempDir()
	orderPath := filepath.Join(dir, "order")
	t.Setenv("TASK_ORDER_FILE", orderPath)
	conf := config.DefaultConfig().Worker
	conf.WorkDir = dir
	conf.PollingRate = durationpb.New(time.Hour)
	conf.Container.DriverCommand = writeWorkerTestDriver(t)
	conf.Container.PullCommand = "pull {{.Image}}"
	conf.Container.RunCommand = "run {{.Image}}"
	task := &tes.Task{
		Id:    "failed-preparation-task",
		State: tes.State_UNKNOWN,
		Inputs: []*tes.Input{{
			Url:  "test://input.txt",
			Path: "/inputs/sub/input.txt",
			Type: tes.FileType_FILE,
		}},
		Executors: []*tes.Executor{{Image: "test", Command: []string{"true"}, Stdout: "/inputs/sub"}},
	}
	writer := &orderedEventWriter{orderPath: orderPath}
	worker := &DefaultWorker{
		Conf:        conf,
		Store:       &workerTestStorage{},
		TaskReader:  &FileTaskReader{task: task},
		EventWriter: writer,
	}
	if err := worker.Run(context.Background()); err == nil {
		t.Fatal("Run() error = nil, want executor log preparation failure")
	}
	if states := workerTaskStates(writer.snapshotEvents()); len(states) != 2 || states[0] != tes.State_INITIALIZING || states[1] != tes.State_SYSTEM_ERROR {
		t.Fatalf("task states = %v, want [INITIALIZING SYSTEM_ERROR]", states)
	}
	if order, err := os.ReadFile(orderPath); err != nil && !os.IsNotExist(err) {
		t.Fatalf("reading executor order: %v", err)
	} else if strings.Contains(string(order), "launch") {
		t.Fatalf("executor launched after preparation failed: %q", order)
	}
}

func TestClaimedWorkerMarksRunningAtFirstExecutorDispatch(t *testing.T) {
	dir := t.TempDir()
	orderPath := filepath.Join(dir, "order")
	t.Setenv("TASK_ORDER_FILE", orderPath)
	conf := config.DefaultConfig().Worker
	conf.WorkDir = dir
	conf.PollingRate = durationpb.New(time.Hour)
	conf.Container.DriverCommand = writeWorkerTestDriver(t)
	conf.Container.PullCommand = "pull {{.Image}}"
	conf.Container.RunCommand = "run {{.Image}}"
	task := &tes.Task{Id: "claimed-dispatch-task", State: tes.State_INITIALIZING, Executors: []*tes.Executor{{Image: "test"}}}
	writer := &orderedEventWriter{orderPath: orderPath}
	worker := &DefaultWorker{
		Conf:             conf,
		TaskReader:       &startupTaskReader{task: task},
		ExecutionClaimer: grantExecutionClaim{},
		EventWriter:      writer,
	}
	if err := worker.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	wantOrder := []string{"state:RUNNING", "launch", "state:COMPLETE"}
	if got := workerOrder(t, orderPath); len(got) != len(wantOrder) {
		t.Fatalf("event and executor order = %v, want %v", got, wantOrder)
	} else {
		for i := range wantOrder {
			if got[i] != wantOrder[i] {
				t.Fatalf("event and executor order = %v, want %v", got, wantOrder)
			}
		}
	}
}

func TestWorkerDoesNotMarkRunningWithoutExecutors(t *testing.T) {
	dir := t.TempDir()
	conf := config.DefaultConfig().Worker
	conf.WorkDir = dir
	conf.PollingRate = durationpb.New(time.Hour)
	writer := &startupEventWriter{}
	worker := &DefaultWorker{
		Conf:        conf,
		TaskReader:  &FileTaskReader{task: &tes.Task{Id: "empty-task", State: tes.State_UNKNOWN}},
		EventWriter: writer,
	}
	if err := worker.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if states := workerTaskStates(writer.events); len(states) != 2 || states[0] != tes.State_INITIALIZING || states[1] != tes.State_COMPLETE {
		t.Fatalf("task states = %v, want [INITIALIZING COMPLETE]", states)
	}
}

func TestWorkerDoesNotLaunchWhenRunningStateWriteFails(t *testing.T) {
	dir := t.TempDir()
	orderPath := filepath.Join(dir, "order")
	t.Setenv("TASK_ORDER_FILE", orderPath)
	conf := config.DefaultConfig().Worker
	conf.WorkDir = dir
	conf.PollingRate = durationpb.New(time.Hour)
	conf.Container.DriverCommand = writeWorkerTestDriver(t)
	conf.Container.PullCommand = "pull {{.Image}}"
	conf.Container.RunCommand = "run {{.Image}}"
	stateErr := errors.New("running state write failed")
	writer := &orderedEventWriter{orderPath: orderPath, rejectRunning: stateErr}
	worker := &DefaultWorker{
		Conf:        conf,
		TaskReader:  &FileTaskReader{task: &tes.Task{Id: "state-write-task", State: tes.State_UNKNOWN, Executors: []*tes.Executor{{Image: "test"}}}},
		EventWriter: writer,
	}
	if err := worker.Run(context.Background()); !errors.Is(err, stateErr) {
		t.Fatalf("Run() error = %v, want %v", err, stateErr)
	}
	if order, err := os.ReadFile(orderPath); err != nil && !os.IsNotExist(err) {
		t.Fatalf("reading executor order: %v", err)
	} else if strings.Contains(string(order), "launch") {
		t.Fatalf("executor launched after RUNNING state write failed: %q", order)
	}
}

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
			taskReader := &startupTaskReader{task: task}
			if tt.state == tes.State_RUNNING {
				taskReader.state = func(context.Context) (tes.State, error) { return tes.State_COMPLETE, nil }
			}
			eventWriter := &startupEventWriter{}
			worker := &DefaultWorker{
				Conf:             conf,
				Store:            &storage.Mux{},
				TaskReader:       taskReader,
				ExecutionClaimer: grantExecutionClaim{},
				EventWriter:      eventWriter,
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

func TestWorkerClaimFailureHasNoSideEffects(t *testing.T) {
	claimErr := errors.New("claim response was lost")
	claimFailure := errors.New("claim failed")
	tests := []struct {
		name      string
		claim     func(context.Context, string) (bool, error)
		wantError error
	}{
		{name: "denied", claim: func(context.Context, string) (bool, error) { return false, nil }},
		{name: "claim error", claim: func(context.Context, string) (bool, error) { return false, claimFailure }, wantError: claimFailure},
		{name: "lost response", claim: func(context.Context, string) (bool, error) { return false, claimErr }, wantError: claimErr},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			workDir := filepath.Join(dir, "work")
			taskDir := filepath.Join(workDir, "admission-task")
			if err := os.MkdirAll(taskDir, 0755); err != nil {
				t.Fatal(err)
			}
			preserved := filepath.Join(taskDir, "winner-data")
			if err := os.WriteFile(preserved, []byte("winner"), 0600); err != nil {
				t.Fatal(err)
			}

			marker := filepath.Join(dir, "executor-ran")
			driver := filepath.Join(dir, "executor-driver")
			if err := os.WriteFile(driver, []byte("#!/bin/sh\nif [ \"$1\" = version ]; then printf '{\\\"Server\\\":\\\"1\\\",\\\"Client\\\":\\\"1\\\"}'; exit 0; fi\nif [ \"$1\" = run ]; then : > \"$TASK_RUN_MARKER\"; fi\nexit 0\n"), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("TASK_RUN_MARKER", marker)

			conf := config.DefaultConfig().Worker
			conf.WorkDir = workDir
			conf.PollingRate = durationpb.New(time.Hour)
			conf.Container.DriverCommand = driver
			conf.Container.PullCommand = "pull"
			conf.Container.RunCommand = "run {{.Image}} {{.Command}}"
			writer := &startupEventWriter{}
			reader := &startupTaskReader{
				task:  &tes.Task{Id: "admission-task", State: tes.State_INITIALIZING, Executors: []*tes.Executor{{Image: "test", Command: []string{"true"}}}},
				state: func(context.Context) (tes.State, error) { return tes.State_COMPLETE, nil },
			}
			worker := &DefaultWorker{
				Conf:             conf,
				TaskReader:       reader,
				ExecutionClaimer: executionClaimFunc(tt.claim),
				EventWriter:      writer,
			}

			err := worker.Run(context.Background())
			if tt.wantError == nil && err != nil {
				t.Fatalf("Run() error = %v, want nil", err)
			}
			if tt.wantError != nil && !errors.Is(err, tt.wantError) {
				t.Fatalf("Run() error = %v, want %v", err, tt.wantError)
			}
			if len(writer.events) != 0 {
				t.Fatalf("claim failure emitted %d events, want none", len(writer.events))
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("executor marker stat error = %v, want not-exist", err)
			}
			if got, err := os.ReadFile(preserved); err != nil || string(got) != "winner" {
				t.Fatalf("preserved winner data = %q, error = %v", got, err)
			}
		})
	}
}

func TestDeniedWorkerObservesWinnerUntilTerminal(t *testing.T) {
	stateReads := 0
	reader := &startupTaskReader{
		task: &tes.Task{Id: "observed-task", State: tes.State_INITIALIZING},
		state: func(context.Context) (tes.State, error) {
			stateReads++
			switch stateReads {
			case 1:
				return tes.State_UNKNOWN, errors.New("temporary read error")
			case 2:
				return tes.State_RUNNING, nil
			default:
				return tes.State_COMPLETE, nil
			}
		},
	}
	writer := &startupEventWriter{}
	worker := &DefaultWorker{
		Conf:             &config.Worker{WorkDir: t.TempDir(), PollingRate: durationpb.New(time.Millisecond)},
		TaskReader:       reader,
		ExecutionClaimer: executionClaimFunc(func(context.Context, string) (bool, error) { return false, nil }),
		EventWriter:      writer,
	}

	if err := worker.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if stateReads != 3 {
		t.Fatalf("task state read count = %d, want 3", stateReads)
	}
	if len(writer.events) != 0 {
		t.Fatalf("denied worker emitted %d events, want none", len(writer.events))
	}
}

func TestDeniedWorkerStopsObservingOnContextCancellation(t *testing.T) {
	reader := &startupTaskReader{
		task:  &tes.Task{Id: "observed-task", State: tes.State_INITIALIZING},
		state: func(context.Context) (tes.State, error) { return tes.State_RUNNING, nil },
	}
	writer := &startupEventWriter{}
	worker := &DefaultWorker{
		Conf:             &config.Worker{WorkDir: t.TempDir(), PollingRate: durationpb.New(time.Millisecond)},
		TaskReader:       reader,
		ExecutionClaimer: executionClaimFunc(func(context.Context, string) (bool, error) { return false, nil }),
		EventWriter:      writer,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := worker.Run(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run() error = %v, want context deadline exceeded", err)
	}
	if len(writer.events) != 0 {
		t.Fatalf("denied worker emitted %d events, want none", len(writer.events))
	}
}

func TestWorkerRequiresClaimForGenericTaskReader(t *testing.T) {
	task := &tes.Task{Id: "generic-task", State: tes.State_QUEUED}
	writer := &startupEventWriter{}
	reader := NewGenericTaskReader(func(context.Context, *tes.GetTaskRequest) (*tes.Task, error) {
		return task, nil
	}, task.Id, func() {})
	worker := &DefaultWorker{
		Conf:        &config.Worker{WorkDir: t.TempDir()},
		TaskReader:  reader,
		EventWriter: writer,
	}

	if err := worker.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "execution claimer is required") {
		t.Fatalf("Run() error = %v, want missing execution claimer", err)
	}
	if len(writer.events) != 0 {
		t.Fatalf("missing claim emitted %d events, want none", len(writer.events))
	}
}

func TestClaimedWorkerSkipsRedundantStartupAndRunningEvents(t *testing.T) {
	task := &tes.Task{Id: "claimed-task", State: tes.State_INITIALIZING}
	writer := &startupEventWriter{}
	worker := &DefaultWorker{
		Conf:             &config.Worker{WorkDir: t.TempDir(), PollingRate: durationpb.New(time.Hour), LeaveWorkDir: true},
		TaskReader:       &startupTaskReader{task: task},
		ExecutionClaimer: grantExecutionClaim{},
		EventWriter:      writer,
	}

	if err := worker.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	sawComplete := false
	for _, event := range writer.events {
		if event.Type == events.Type_TASK_STATE && (event.GetState() == tes.State_INITIALIZING || event.GetState() == tes.State_RUNNING) {
			t.Fatalf("claimed worker emitted redundant state %s", event.GetState())
		}
		if event.Type == events.Type_TASK_STATE && event.GetState() == tes.State_COMPLETE {
			sawComplete = true
		}
	}
	if !sawComplete {
		t.Fatal("claimed worker did not emit COMPLETE")
	}
}
