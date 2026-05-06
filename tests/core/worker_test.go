package core

import (
	"context"
	"os"
	"path"
	"strings"
	"testing"
	"time"

	workerCmd "github.com/ohsu-comp-bio/funnel/cmd/worker"

	"github.com/ohsu-comp-bio/funnel/events"
	"github.com/ohsu-comp-bio/funnel/storage"
	"github.com/ohsu-comp-bio/funnel/tes"
	"github.com/ohsu-comp-bio/funnel/tests"
	"github.com/ohsu-comp-bio/funnel/worker"
	gcontext "golang.org/x/net/context"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestWorkerRun(t *testing.T) {
	tests.SetLogOutput(log, t)
	c := tests.DefaultConfig()
	c.Compute = "noop"
	f := tests.NewFunnel(c)
	f.StartServer()

	// this only writes the task to the DB since the 'noop'
	// compute backend is in use
	id := f.Run(`
    --sh 'echo hello world'
  `)

	ctx := context.Background()
	err := workerCmd.Run(ctx, c, log, &workerCmd.Options{TaskID: id})
	if err != nil {
		t.Fatal("unexpected error", err)
	}

	time.Sleep(5 * time.Second)

	task, err := f.HTTP.GetTask(ctx, &tes.GetTaskRequest{
		Id:   id,
		View: tes.View_FULL.String(),
	})
	if err != nil {
		t.Fatal("unexpected error", err)
	}

	if task.State != tes.State_COMPLETE {
		t.Fatal("unexpected state")
	}

	if task.Logs[0].Logs[0].Stdout != "hello world\n" {
		t.Fatal("missing stdout")
	}
}

func TestWorkDirCleanup(t *testing.T) {
	tests.SetLogOutput(log, t)
	c := tests.DefaultConfig()
	c.Compute = "noop"
	f := tests.NewFunnel(c)
	f.StartServer()

	// cleanup
	id := f.Run(`
    --sh 'echo hello world'
  `)
	workdir := path.Join(c.Worker.WorkDir, id)

	ctx := context.Background()
	err := workerCmd.Run(ctx, c, log, &workerCmd.Options{TaskID: id})
	if err != nil {
		t.Fatal("unexpected error", err)
	}

	task, err := f.HTTP.GetTask(ctx, &tes.GetTaskRequest{
		Id:   id,
		View: tes.View_FULL.String(),
	})
	if err != nil {
		t.Fatal("unexpected error", err)
	}

	if task.State != tes.State_COMPLETE {
		t.Fatal("unexpected state")
	}

	if _, err := os.Stat(workdir); !os.IsNotExist(err) {
		t.Error("expected worker to cleanup workdir")
	}

	// no cleanup
	id = f.Run(`
    --sh 'echo hello world'
  `)

	c.Worker.LeaveWorkDir = true
	workdir = path.Join(c.Worker.WorkDir, id)

	err = workerCmd.Run(ctx, c, log, &workerCmd.Options{TaskID: id})
	if err != nil {
		t.Fatal("unexpected error", err)
	}

	task, err = f.HTTP.GetTask(ctx, &tes.GetTaskRequest{
		Id:   id,
		View: tes.View_FULL.String(),
	})
	if err != nil {
		t.Fatal("unexpected error", err)
	}

	if task.State != tes.State_COMPLETE {
		t.Fatal("unexpected state")
	}

	if fi, err := os.Stat(workdir); err != nil {
		if !fi.IsDir() {
			t.Error("expected worker to leave workdir")
		}
	}
}

type eventCounter struct {
	stdout, stderr int
}

func (e *eventCounter) WriteEvent(ctx context.Context, ev *events.Event) error {
	switch ev.Type {
	case events.Type_EXECUTOR_STDOUT:
		e.stdout++
	case events.Type_EXECUTOR_STDERR:
		e.stderr++
	}
	return nil
}

func (e *eventCounter) Close() {}

type taskReader struct {
	task *tes.Task
}

func (r taskReader) Task(ctx gcontext.Context) (*tes.Task, error) {
	return r.task, nil
}

func (r taskReader) State(ctx gcontext.Context) (tes.State, error) {
	return r.task.State, nil
}

func (r taskReader) Close() {}

// Test that stdout generates events at an expected, consistent rate.
// The task dumps megabytes of random data to stdout. The test waits
// 10 seconds and checks how many stdout events were generated.
func TestLargeLogRate(t *testing.T) {
	tests.SetLogOutput(log, t)
	conf := tests.DefaultConfig()
	conf.Worker.LogUpdateRate = durationpb.New(time.Millisecond * 500)
	conf.Worker.LogTailSize = 1000
	task := tes.Task{
		Id: "test-task-" + tes.GenerateID(),
		Executors: []*tes.Executor{
			{
				Image:   "alpine",
				Command: []string{"dd", "if=/dev/urandom", "bs=5000000", "count=100"},
			},
		},
	}

	counts := &eventCounter{}
	logger := &events.Logger{Log: log}
	m := &events.MultiWriter{logger, counts}

	w := worker.DefaultWorker{
		Conf:        conf.Worker,
		Store:       &storage.Mux{},
		TaskReader:  taskReader{&task},
		EventWriter: m,
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()
	w.Run(ctx)

	// Given the difficulty of timing how long it task a task + docker container to start,
	// we just check that a small amount of events were generated.
	// 20 events is not too bad for dumping many megabytes of data.
	if counts.stdout > 20 {
		t.Error("unexpected stdout event count", counts.stdout)
	}
}

// Test that a log update rate of zero results in a single stdout event.
// The task dumps megabytes of random data to stdout. The test waits
// 10 seconds and checks how many stdout events were generated.
func TestZeroLogRate(t *testing.T) {
	tests.SetLogOutput(log, t)
	conf := tests.DefaultConfig()
	conf.Worker.LogUpdateRate = durationpb.New(0)
	conf.Worker.LogTailSize = 1000
	task := tes.Task{
		Id: "test-task-" + tes.GenerateID(),
		Executors: []*tes.Executor{
			{
				Image:   "alpine",
				Command: []string{"dd", "if=/dev/urandom", "bs=5000000", "count=5"},
			},
		},
	}

	counts := &eventCounter{}
	logger := &events.Logger{Log: log}
	m := &events.MultiWriter{logger, counts}

	w := worker.DefaultWorker{
		Conf:        conf.Worker,
		Store:       &storage.Mux{},
		TaskReader:  taskReader{&task},
		EventWriter: m,
	}

	w.Run(context.Background())

	time.Sleep(time.Second)

	// we expect a single event to be generated
	if counts.stdout != 1 {
		t.Error("unexpected stdout event count", counts.stdout)
	}
}

// Test that we can turn off stdout/err logging.
// The task dumps megabytes of random data to stdout. The test waits
// 10 seconds and checks how many stdout events were generated.
func TestZeroLogTailSize(t *testing.T) {
	tests.SetLogOutput(log, t)
	conf := tests.DefaultConfig()
	conf.Worker.LogUpdateRate = durationpb.New(time.Millisecond * 500)
	conf.Worker.LogTailSize = 0
	task := tes.Task{
		Id: "test-task-" + tes.GenerateID(),
		Executors: []*tes.Executor{
			{
				Image:   "alpine",
				Command: []string{"dd", "if=/dev/urandom", "bs=5000000", "count=100"},
			},
		},
	}

	counts := &eventCounter{}
	logger := &events.Logger{Log: log}
	m := &events.MultiWriter{logger, counts}

	w := worker.DefaultWorker{
		Conf:        conf.Worker,
		Store:       &storage.Mux{},
		TaskReader:  taskReader{&task},
		EventWriter: m,
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()
	err := w.Run(ctx)
	if err != nil {
		t.Log(err)
	}

	// we expect zero events to be generated
	if counts.stdout != 0 {
		t.Error("unexpected stdout event count", counts.stdout)
	}
}

// Test that the tail is stored
func TestLogTailContent(t *testing.T) {
	tests.SetLogOutput(log, t)
	conf := tests.DefaultConfig()
	conf.Worker.LogUpdateRate = durationpb.New(time.Millisecond * 10)
	conf.Worker.LogTailSize = 10
	task := tes.Task{
		Id: "test-task-" + tes.GenerateID(),
		Executors: []*tes.Executor{
			{
				Image:   "alpine",
				Command: []string{"sh", "-c", "for i in $(seq 0 10); do echo ${i}abc && sleep 0.1; done | tee /dev/stderr"},
			},
		},
	}

	builder := &events.TaskBuilder{Task: &task}
	logger := &events.Logger{Log: log}
	m := &events.MultiWriter{logger, builder}

	w := worker.DefaultWorker{
		Conf:        conf.Worker,
		Store:       &storage.Mux{},
		TaskReader:  taskReader{&task},
		EventWriter: m,
	}

	err := w.Run(context.Background())
	if err != nil {
		t.Error("unexpected worker.Run error", err)
	}

	if task.State != tes.Complete {
		t.Error("unexpected task state", task.State)
	}

	if builder.Task.Logs[0].Logs[0].Stdout != "abc\n10abc\n" {
		t.Error("unexpected stdout", builder.Task.Logs[0].Logs[0].Stdout)
	}
	if builder.Task.Logs[0].Logs[0].Stderr != "abc\n10abc\n" {
		t.Error("unexpected stderr", builder.Task.Logs[0].Logs[0].Stderr)
	}
}

// Test that docker container metadata is logged.
func TestDockerContainerMetadata(t *testing.T) {
	tests.SetLogOutput(log, t)
	conf := tests.DefaultConfig()
	conf.Worker.LogUpdateRate = durationpb.New(time.Millisecond * 10)
	conf.Worker.LogTailSize = 10
	task := tes.Task{
		Id: "test-task-" + tes.GenerateID(),
		Executors: []*tes.Executor{
			{
				Image:   "alpine",
				Command: []string{"sleep", "5"},
			},
		},
	}

	builder := &events.TaskBuilder{Task: &task}
	logger := &events.Logger{Log: log}
	m := &events.MultiWriter{logger, builder}

	w := worker.DefaultWorker{
		Conf:        conf.Worker,
		Store:       &storage.Mux{},
		TaskReader:  taskReader{&task},
		EventWriter: m,
	}

	err := w.Run(context.Background())
	if err != nil {
		t.Error("unexpected worker.Run error", err)
	}

	meta := ""
	for _, log := range builder.Task.Logs[0].SystemLogs {
		if strings.Contains(log, `msg='container metadata'`) {
			meta = log
		}
	}
	if meta == "" {
		t.Error("didn't find container metadata system log")
	}

	containerID := ""
	containerHash := ""
	for _, f := range strings.Fields(meta) {
		if strings.HasPrefix(f, "containerID") {
			containerID = f
		}
		if strings.HasPrefix(f, "containerImageHash") {
			containerHash = f
		}
	}

	if containerID == "" {
		t.Error("didn't find container ID metadata")
	}
	if containerHash == "" {
		t.Error("didn't find container image hash metadata")
	}
}

func TestWorkerRunFileTaskReader(t *testing.T) {
	tests.SetLogOutput(log, t)
	c := tests.DefaultConfig()
	ctx := context.Background()

	// Task builder collects events into a task view.
	task := &tes.Task{}
	b := events.TaskBuilder{Task: task}

	opts := &workerCmd.Options{
		TaskFile: "../../examples/hello-world.json",
	}

	worker, err := workerCmd.NewWorker(ctx, c, log, opts)
	if err != nil {
		t.Fatal("unexpected error", err)
	}
	worker.EventWriter = &events.MultiWriter{b, worker.EventWriter}

	err = worker.Run(ctx)
	if err != nil {
		t.Fatal("unexpected error", err)
	}

	if task.State != tes.Complete {
		t.Error("unexpected task state")
	}
}

func TestWorkerRunBase64TaskReader(t *testing.T) {
	tests.SetLogOutput(log, t)
	c := tests.DefaultConfig()
	ctx := context.Background()

	// Task builder collects events into a task view.
	task := &tes.Task{}
	b := events.TaskBuilder{Task: task}

	opts := &workerCmd.Options{
		TaskBase64: "ewogICJuYW1lIjogIkhlbGxvIHdvcmxkIiwKICAiZGVzY3JpcHRpb24iOiAiRGVtb25zdHJhdGVzIHRoZSBtb3N0IGJhc2ljIGVjaG8gdGFzay4iLAogICJleGVjdXRvcnMiOiBbCiAgICB7CiAgICAgICJpbWFnZSI6ICJhbHBpbmUiLAogICAgICAiY29tbWFuZCI6IFsiZWNobyIsICJoZWxsbyB3b3JsZCJdCiAgICB9CiAgXQp9Cg==",
	}

	worker, err := workerCmd.NewWorker(ctx, c, log, opts)
	if err != nil {
		t.Fatal("unexpected error", err)
	}
	worker.EventWriter = &events.MultiWriter{b, worker.EventWriter}

	err = worker.Run(ctx)
	if err != nil {
		t.Fatal("unexpected error", err)
	}

	if task.State != tes.Complete {
		t.Error("unexpected task state")
	}
}
