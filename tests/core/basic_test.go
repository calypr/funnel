package core

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/go-multierror"
	workerCmd "github.com/ohsu-comp-bio/funnel/cmd/worker"
	"github.com/ohsu-comp-bio/funnel/events"
	"github.com/ohsu-comp-bio/funnel/tes"
	"github.com/ohsu-comp-bio/funnel/tests"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestHelloWorld(t *testing.T) {
	tests.SetLogOutput(log, t)
	id := fun.Run(`
    --sh 'echo hello world'
  `)
	task := fun.Wait(id)

	if task.State != tes.State_COMPLETE {
		t.Fatal("unexpected task state")
	}

	if task.Logs[0].Logs[0].Stdout != "hello world\n" {
		t.Fatal("Missing stdout")
	}
}

func TestGetUnknownTask(t *testing.T) {
	tests.SetLogOutput(log, t)
	var err error

	_, err = fun.HTTP.GetTask(context.Background(), &tes.GetTaskRequest{
		Id:   "nonexistent-task-id",
		View: tes.View_MINIMAL.String(),
	})
	if err == nil || !strings.Contains(err.Error(), "STATUS CODE - 404") {
		t.Error("expected not found error", err)
	}

	_, err = fun.RPC.GetTask(
		context.Background(),
		&tes.GetTaskRequest{Id: "nonexistent-task-id", View: tes.View_MINIMAL.String()},
	)
	s, _ := status.FromError(err)
	if err == nil || s.Code() != codes.NotFound {
		t.Error("expected not found error", err)
	}
}

func TestGetTaskView(t *testing.T) {
	tests.SetLogOutput(log, t)
	var err error
	var task *tes.Task

	fun.WriteFile("test_content.txt", "hello world")

	id := fun.Run(`
    --sh 'echo hello world | tee /dev/stderr'
    --name 'foo'
    --content in={{ .storage }}/test_content.txt
  `)
	fun.Wait(id)

	task = fun.GetView(id, tes.View_MINIMAL)

	if task.Id != id {
		t.Fatal("expected task ID in minimal view")
	}
	if task.State == tes.State_UNKNOWN {
		t.Fatal("expected state in minimal view")
	}
	if task.Name != "" {
		t.Fatal("unexpected task name included in minimal view")
	}
	if task.Inputs != nil {
		t.Fatal("unexpected task inputs included in minimal view")
	}
	if task.Outputs != nil {
		t.Fatal("unexpected task inputs included in minimal view")
	}
	if task.Executors != nil {
		t.Fatal("unexpected task executors included in minimal view")
	}
	if task.Logs != nil {
		t.Fatal("unexpected task logs included in minimal view")
	}

	task = fun.GetView(id, tes.View_BASIC)

	if task.Id != id {
		t.Fatal("expected task ID in basic view")
	}
	if task.State == tes.State_UNKNOWN {
		t.Fatal("expected state in basic view")
	}
	if task.Name != "foo" {
		t.Fatal("expected task name to be included basic view")
	}
	if len(task.Inputs) != 1 {
		t.Fatal("expected Inputs to be included in basic view")
	}
	if task.Inputs[0].Content != "" {
		t.Fatal("unexpected Input content in basic view")
	}
	if len(task.Executors) != 1 {
		t.Fatal("expected Executors to be included in basic view")
	}
	if len(task.Logs) != 1 {
		t.Fatal("expected TaskLog to be included in basic view")
	}
	if len(task.Logs[0].SystemLogs) != 0 {
		t.Fatal("unexpected SystemLogs included in basic view")
	}
	if len(task.Logs[0].Logs) != 1 {
		t.Fatal("expected ExecutorLog to be included in basic view")
	}
	if task.Logs[0].Logs[0].Stdout != "" {
		t.Fatal("unexpected ExecutorLog stdout included in basic view")
	}
	if task.Logs[0].Logs[0].Stderr != "" {
		t.Fatal("unexpected ExecutorLog stderr included in basic view")
	}

	task = fun.GetView(id, tes.View_FULL)

	if task.Id != id {
		t.Fatal("expected task ID in full view")
	}
	if task.State == tes.State_UNKNOWN {
		t.Fatal("expected state in full view")
	}
	if task.Name != "foo" {
		t.Fatal("expected task name to be included full view")
	}
	if len(task.Inputs) != 1 {
		t.Fatal("expected Inputs to be included in full view")
	}
	if task.Inputs[0].Content != "hello world" {
		t.Fatal("missing Input content in full view")
	}
	if len(task.Executors) != 1 {
		t.Fatal("expected Executors to be included in full view")
	}
	if len(task.Logs) != 1 {
		t.Fatal("expected TaskLog to be included in full view")
	}
	if len(task.Logs[0].SystemLogs) == 0 {
		t.Fatal("Missing syslogs in full view")
	}
	if len(task.Logs[0].Logs) != 1 {
		t.Fatal("expected ExecutorLog to be included in full view")
	}
	if task.Logs[0].Logs[0].Stdout != "hello world\n" {
		t.Fatal("Missing stdout in full view")
	}
	if task.Logs[0].Logs[0].Stderr != "hello world\n" {
		t.Fatal("Missing stderr in full view")
	}

	// test http proxy
	task, err = fun.HTTP.GetTask(context.Background(), &tes.GetTaskRequest{
		Id:   id,
		View: tes.View_MINIMAL.String(),
	})
	if err != nil {
		t.Fatal(err)
	}

	if task.Id != id {
		t.Fatal("expected task ID in minimal view")
	}
	if task.State == tes.State_UNKNOWN {
		t.Fatal("expected state in minimal view")
	}
	if task.Name != "" {
		t.Fatal("unexpected task name included in minimal view")
	}
	if task.Inputs != nil {
		t.Fatal("unexpected task inputs included in minimal view")
	}
	if task.Outputs != nil {
		t.Fatal("unexpected task inputs included in minimal view")
	}
	if task.Executors != nil {
		t.Fatal("unexpected task executors included in minimal view")
	}
	if task.Logs != nil {
		t.Fatal("unexpected task logs included in minimal view")
	}

	task, err = fun.HTTP.GetTask(context.Background(), &tes.GetTaskRequest{
		Id:   id,
		View: tes.View_BASIC.String(),
	})
	if err != nil {
		t.Fatal(err)
	}

	if task.Id != id {
		t.Fatal("expected task ID in basic view")
	}
	if task.State == tes.State_UNKNOWN {
		t.Fatal("expected state in basic view")
	}
	if task.Name != "foo" {
		t.Fatal("expected task name to be included basic view")
	}
	if len(task.Inputs) != 1 {
		t.Fatal("expected Inputs to be included in basic view")
	}
	if task.Inputs[0].Content != "" {
		t.Fatal("unexpected Input content in basic view")
	}
	if len(task.Executors) != 1 {
		t.Fatal("expected Executors to be included in basic view")
	}
	if len(task.Logs) != 1 {
		t.Fatal("expected TaskLog to be included in basic view")
	}
	if len(task.Logs[0].SystemLogs) != 0 {
		t.Fatal("unexpected SystemLogs included in basic view")
	}
	if len(task.Logs[0].Logs) != 1 {
		t.Fatal("expected ExecutorLog to be included in basic view")
	}
	if task.Logs[0].Logs[0].Stdout != "" {
		t.Fatal("unexpected ExecutorLog stdout included in basic view")
	}
	if task.Logs[0].Logs[0].Stderr != "" {
		t.Fatal("unexpected ExecutorLog stderr included in basic view")
	}

	task, err = fun.HTTP.GetTask(context.Background(), &tes.GetTaskRequest{
		Id:   id,
		View: tes.View_FULL.String(),
	})
	if err != nil {
		t.Fatal(err)
	}

	if task.Id != id {
		t.Fatal("expected task ID in full view")
	}
	if task.State == tes.State_UNKNOWN {
		t.Fatal("expected state in full view")
	}
	if task.Name != "foo" {
		t.Fatal("expected task name to be included full view")
	}
	if len(task.Inputs) != 1 {
		t.Fatal("expected Inputs to be included in full view")
	}
	if task.Inputs[0].Content != "hello world" {
		t.Fatal("missing Input content in full view")
	}
	if len(task.Executors) != 1 {
		t.Fatal("expected Executors to be included in full view")
	}
	if len(task.Logs) != 1 {
		t.Fatal("expected TaskLog to be included in full view")
	}
	if len(task.Logs[0].SystemLogs) == 0 {
		t.Fatal("Missing syslogs in full view")
	}
	if len(task.Logs[0].Logs) != 1 {
		t.Fatal("expected ExecutorLog to be included in full view")
	}
	if task.Logs[0].Logs[0].Stdout != "hello world\n" {
		t.Fatal("Missing stdout in full view")
	}
	if task.Logs[0].Logs[0].Stderr != "hello world\n" {
		t.Fatal("Missing stderr in full view")
	}

}

// TODO this is a bit hacky for now because we're reusing the same
//
//	server + DB for all the e2e tests, so ListTasks gets the
//	results of all of those. It works for the moment, but
//	should probably run against a clean environment.
func TestListTaskView(t *testing.T) {
	tests.SetLogOutput(log, t)
	var tasks []*tes.Task
	var task *tes.Task
	var err error

	fun.WriteFile("test_content.txt", "hello world")

	id := fun.Run(`
    --sh 'echo hello world  | tee /dev/stderr'
    --name 'foo'
    --content in={{ .storage }}/test_content.txt
  `)
	fun.Wait(id)

	time.Sleep(500 * time.Millisecond)

	tasks = fun.ListView(tes.View_MINIMAL)
	task = tasks[0]

	if task.Id != id {
		t.Fatal("expected task ID in minimal view")
	}
	if task.State != tes.State_COMPLETE {
		t.Fatal("expected the COMPLETE state in minimal view, got ", task.State)
	}
	if task.Name != "" {
		t.Fatal("unexpected task name included in minimal view")
	}
	if task.Inputs != nil {
		t.Fatal("unexpected task inputs included in minimal view")
	}
	if task.Outputs != nil {
		t.Fatal("unexpected task inputs included in minimal view")
	}
	if task.Executors != nil {
		t.Fatal("unexpected task executors included in minimal view")
	}
	if task.Logs != nil {
		t.Fatal("unexpected task logs included in minimal view")
	}

	tasks = fun.ListView(tes.View_BASIC)
	task = tasks[0]

	if task.Id != id {
		t.Fatal("expected task ID in basic view")
	}
	if task.State != tes.State_COMPLETE {
		t.Fatal("expected the COMPLETE state in basic view, got ", task.State)
	}
	if task.Name == "" {
		t.Fatal("expected task name to be included basic view")
	}
	if len(task.Inputs) != 1 {
		t.Fatal("expected Inputs to be included in basic view")
	}
	if task.Inputs[0].Content != "" {
		t.Fatal("unexpected Input content in basic view")
	}
	if len(task.Executors) != 1 {
		t.Fatal("expected Executors to be included in basic view")
	}
	if len(task.Logs) != 1 {
		t.Fatal("expected TaskLog to be included in basic view")
	}
	if len(task.Logs[0].SystemLogs) != 0 {
		t.Fatal("unexpected SystemLogs included in basic view")
	}
	if len(task.Logs[0].Logs) != 1 {
		t.Fatal("expected ExecutorLog to be included in basic view")
	}
	if task.Logs[0].Logs[0].Stdout != "" {
		t.Fatal("unexpected ExecutorLog stdout included in basic view")
	}
	if task.Logs[0].Logs[0].Stderr != "" {
		t.Fatal("unexpected ExecutorLog stderr included in basic view")
	}

	tasks = fun.ListView(tes.View_FULL)
	task = tasks[0]

	if task.Id != id {
		t.Fatal("expected task ID in full view")
	}
	if task.State != tes.State_COMPLETE {
		t.Fatal("expected the COMPLETE state in full view, got ", task.State)
	}
	if task.Name == "" {
		t.Fatal("expected task name to be included full view")
	}
	if len(task.Inputs) != 1 {
		t.Fatal("expected Inputs to be included in full view")
	}
	if task.Inputs[0].Content != "hello world" {
		t.Fatal("missing Input content in full view")
	}
	if len(task.Executors) != 1 {
		t.Fatal("expected Executors to be included in full view")
	}
	if len(task.Logs) != 1 {
		t.Fatal("expected TaskLog to be included in full view")
	}
	if len(task.Logs[0].SystemLogs) == 0 {
		t.Fatal("Missing syslogs in full view")
	}
	if len(task.Logs[0].Logs) != 1 {
		t.Fatal("expected ExecutorLog to be included in full view")
	}
	if task.Logs[0].Logs[0].Stdout != "hello world\n" {
		t.Fatal("Missing stdout in full view")
	}
	if task.Logs[0].Logs[0].Stderr != "hello world\n" {
		t.Fatal("Missing stderr in full view")
	}

	// test http proxy
	var r *tes.ListTasksResponse
	r, err = fun.HTTP.ListTasks(context.Background(), &tes.ListTasksRequest{
		View: tes.View_MINIMAL.String(),
	})
	if err != nil {
		t.Fatal(err)
	}
	task = r.Tasks[0]

	if task.Id == "" {
		t.Fatal("expected task ID in minimal view")
	}
	if task.State == tes.State_UNKNOWN {
		t.Fatal("expected state in minimal view")
	}
	if task.Name != "" {
		t.Fatal("unexpected task name included in minimal view")
	}
	if task.Inputs != nil {
		t.Fatal("unexpected task inputs included in minimal view")
	}
	if task.Executors != nil {
		t.Fatal("unexpected task executors included in minimal view")
	}
	if task.Logs != nil {
		t.Fatal("unexpected task logs included in minimal view")
	}

	r, err = fun.HTTP.ListTasks(context.Background(), &tes.ListTasksRequest{
		View: tes.View_BASIC.String(),
	})
	if err != nil {
		t.Fatal(err)
	}
	task = r.Tasks[0]

	if task.Id == "" {
		t.Fatal("expected task ID in basic view")
	}
	if task.State == tes.State_UNKNOWN {
		t.Fatal("expected state in basic view")
	}
	if task.State == tes.State_UNKNOWN {
		t.Fatal("expected complete state")
	}
	if task.Name == "" {
		t.Fatal("expected task name to be included basic view")
	}
	if len(task.Inputs) != 1 {
		t.Fatal("expected Inputs to be included in basic view")
	}
	if task.Inputs[0].Content != "" {
		t.Fatal("unexpected Input content in basic view")
	}
	if len(task.Executors) != 1 {
		t.Fatal("expected Executors to be included in basic view")
	}
	if len(task.Logs) != 1 {
		t.Fatal("expected TaskLog to be included in basic view")
	}
	if len(task.Logs[0].SystemLogs) != 0 {
		t.Fatal("unexpected SystemLogs included in basic view")
	}
	if len(task.Logs[0].Logs) != 1 {
		t.Fatal("expected ExecutorLog to be included in basic view")
	}
	if task.Logs[0].Logs[0].Stdout != "" {
		t.Fatal("unexpected ExecutorLog stdout included in basic view")
	}
	if task.Logs[0].Logs[0].Stderr != "" {
		t.Fatal("unexpected ExecutorLog stderr included in basic view")
	}

	r, err = fun.HTTP.ListTasks(context.Background(), &tes.ListTasksRequest{
		View: tes.View_FULL.String(),
	})
	if err != nil {
		t.Fatal(err)
	}
	task = r.Tasks[0]

	if task.Id == "" {
		t.Fatal("expected task ID in full view")
	}
	if task.State == tes.State_UNKNOWN {
		t.Fatal("expected state in full view")
	}
	if task.Name == "" {
		t.Fatal("expected task name to be included full view")
	}
	if len(task.Inputs) != 1 {
		t.Fatal("expected Inputs to be included in full view")
	}
	if task.Inputs[0].Content != "hello world" {
		t.Fatal("missing Input content in full view")
	}
	if len(task.Executors) != 1 {
		t.Fatal("expected Executors to be included in full view")
	}
	if len(task.Logs) != 1 {
		t.Fatal("expected TaskLog to be included in full view")
	}
	if len(task.Logs[0].SystemLogs) == 0 {
		t.Fatal("Missing syslogs in full view")
	}
	if len(task.Logs[0].Logs) != 1 {
		t.Fatal("expected ExecutorLog to be included in full view")
	}
	if task.Logs[0].Logs[0].Stdout != "hello world\n" {
		t.Fatal("Missing stdout in full view")
	}
	if task.Logs[0].Logs[0].Stderr != "hello world\n" {
		t.Fatal("Missing stderr in full view")
	}
}

// Test that the streaming logs pick up a single character.
// This ensures that the streaming works even when a small
// amount of logs are written (which was once a bug).
func TestSingleCharLog(t *testing.T) {
	tests.SetLogOutput(log, t)
	id := fun.Run(`
    --sh 'echo a; sleep 100'
  `)
	fun.WaitForRunning(id)

	// The EXECUTOR_STDOUT event may take some time, so let's wait max 10 seconds
	stdout := ""
	for range time.NewTicker(10 * time.Second).C {
		task := fun.Get(id)
		stdout = task.Logs[0].Logs[0].Stdout
		if stdout != "" {
			t.Log("Non-empty Stdout detected.")
			break
		}
	}

	if stdout != "a\n" {
		t.Fatal("Missing logs")
	}
	fun.Cancel(id)
}

// Test that a completed task cannot change state.
func TestCompleteStateImmutable(t *testing.T) {
	tests.SetLogOutput(log, t)
	id := fun.Run(`
    --sh 'echo hello'
  `)
	fun.Wait(id)
	_ = fun.Cancel(id)
	task := fun.Get(id)
	if task.State != tes.State_COMPLETE {
		t.Fatal("Unexpected state")
	}
}

// Test canceling a task
func TestCancel(t *testing.T) {
	tests.SetLogOutput(log, t)
	id := fun.Run(`
    --sh 'echo start'
    --sh 'sleep 1000'
    --sh 'echo never'
  `)
	fun.WaitForExec(id, 1)
	fun.Cancel(id)
	fun.WaitForDockerDestroy(id + "-0")
	task := fun.Get(id)
	if task.State != tes.State_CANCELED {
		t.Fatalf("Unexpected state: %s", task.State.String())
	}
}

// Test canceling a task that doesn't exist
func TestCancelUnknownTask(t *testing.T) {
	tests.SetLogOutput(log, t)
	var err error

	_, err = fun.HTTP.CancelTask(context.Background(), &tes.CancelTaskRequest{
		Id: "nonexistent-task-id",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "STATUS CODE - 404") && !strings.Contains(err.Error(), "STATUS CODE - 500") {
		t.Fatal("expected not found error, got", err)
	}

	_, err = fun.RPC.CancelTask(
		context.Background(),
		&tes.CancelTaskRequest{Id: "nonexistent-task-id"},
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	s, _ := status.FromError(err)
	if s.Code() != codes.NotFound {
		t.Fatal("expected not found error, got", s)
	}
}

// The task executor logs list should only include entries for steps that
// have been started or completed, i.e. steps that have yet to be started
// won't show up in Task.Logs[0].Logs
func TestExecutorLogLength(t *testing.T) {
	tests.SetLogOutput(log, t)
	id := fun.Run(`
    --sh 'echo first'
    --sh 'sleep 10'
    --sh 'echo done'
  `)
	fun.WaitForExec(id, 2)
	task := fun.Get(id)
	fun.Cancel(id)
	if len(task.Logs[0].Logs) != 2 {
		t.Fatal("Unexpected executor log count")
	}
}

// There was a bug + fix where the task was being marked complete after
// the first step completed, but the correct behavior is to mark the
// task complete after *all* steps have completed.
func TestMarkCompleteBug(t *testing.T) {
	tests.SetLogOutput(log, t)
	id := fun.Run(`
    --sh 'echo step 1'
    --sh 'sleep 100'
  `)
	fun.WaitForRunning(id)
	fun.WaitForExec(id, 2)
	task := fun.Get(id)
	if task.State != tes.State_RUNNING {
		t.Fatal("Unexpected task state")
	}
	fun.Cancel(id)
}

func TestTaskStartEndTimeLogs(t *testing.T) {
	tests.SetLogOutput(log, t)
	id := fun.Run(`--sh 'echo 1'`)
	task := fun.Wait(id)
	// Some databases require more time to process the updates,
	// such as EndTime, which will happen just before fun.Wait() exists above.
	time.Sleep(time.Millisecond * 500)
	if task.Logs[0].StartTime == "" {
		t.Fatal("missing task start time log")
	}
	if task.Logs[0].EndTime == "" {
		t.Fatalf("missing task end time log: %#v", task.Logs[0])
	}
}

func TestOutputFileLog(t *testing.T) {
	tests.SetLogOutput(log, t)
	dir := fun.Tempdir()

	id, _ := fun.RunTask(&tes.Task{
		Executors: []*tes.Executor{
			{
				Image: "alpine",
				Command: []string{
					"sh", "-c", "mkdir /tmp/outdir; echo fooo > /tmp/outdir/fooofile; echo ba > /tmp/outdir/bafile; echo bar > /tmp/barfile",
				},
			},
		},
		Outputs: []*tes.Output{
			{
				Url:  dir + "/outdir",
				Path: "/tmp/outdir",
				Type: tes.FileType_DIRECTORY,
			},
			{
				Url:  dir + "/barfile",
				Path: "/tmp/barfile",
			},
		},
	})

	task := fun.Wait(id)

	out := task.Logs[0].Outputs

	if out[0].Url != dir+"/outdir/bafile" {
		t.Fatal("unexpected output url", out[0].Url, dir+"/outdir/bafile")
	}

	if out[1].Url != dir+"/outdir/fooofile" {
		t.Fatal("unexpected output url", out[1].Url, dir+"/outdir/fooofile")
	}

	if out[2].Url != dir+"/barfile" {
		t.Fatal("unexpected output url")
	}

	if out[0].SizeBytes != "3" {
		t.Fatal("unexpected output size")
	}
	if out[1].SizeBytes != "5" {
		t.Fatal("unexpected output size")
	}
	if out[2].SizeBytes != "4" {
		t.Fatal("unexpected output size")
	}
}

func TestPagination(t *testing.T) {
	tests.SetLogOutput(log, t)
	c := tests.DefaultConfig()
	c.Compute = "noop"
	f := tests.NewFunnel(c)
	f.StartServer()
	ctx := context.Background()

	// Ensure database is empty
	r0, err := f.RPC.ListTasks(ctx, &tes.ListTasksRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(r0.Tasks) != 0 {
		t.Fatal("expected empty database")
	}

	for i := 0; i < 2050; i++ {
		f.Run(`--sh 'echo 1'`)
	}
	time.Sleep(time.Second * 10)

	r1, _ := f.RPC.ListTasks(ctx, &tes.ListTasksRequest{})

	// Default page size is 256
	if len(r1.Tasks) != 256 {
		t.Error("wrong default page size")
	}

	r2, _ := f.RPC.ListTasks(ctx, &tes.ListTasksRequest{
		PageSize: 1,
	})

	// Minimum page size is 1
	if len(r2.Tasks) != 1 {
		t.Error("wrong minimum page size")
	}

	r3, _ := f.RPC.ListTasks(ctx, &tes.ListTasksRequest{
		PageSize: 5000,
	})

	if len(r3.Tasks) != 2048 {
		t.Error("wrong max page size")
	}

	r4, _ := f.RPC.ListTasks(ctx, &tes.ListTasksRequest{
		PageSize: 500,
	})

	if len(r4.Tasks) != 500 {
		t.Error("wrong requested page size")
	}

	if r4.NextPageToken == "" {
		t.Error("expected next page token")
	}

	// Get all pages
	var tasks []*tes.Task
	tasks = append(tasks, r4.Tasks...)
	for r4.NextPageToken != "" {
		r4, _ = f.RPC.ListTasks(ctx, &tes.ListTasksRequest{
			PageSize:  500,
			PageToken: r4.NextPageToken,
		})
		tasks = append(tasks, r4.Tasks...)
	}

	if len(tasks) != 2050 {
		t.Error("unexpected task count", len(tasks), "expected 2050")
	}
}

// Smaller test for debugging getting the full set of pages and for
// testing sort order
func TestSmallPaginationAndSortOrder(t *testing.T) {
	tests.SetLogOutput(log, t)
	c := tests.DefaultConfig()
	c.Compute = "noop"
	f := tests.NewFunnel(c)
	f.StartServer()
	ctx := context.Background()

	// Ensure database is empty
	r0, err := f.RPC.ListTasks(ctx, &tes.ListTasksRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(r0.Tasks) != 0 {
		t.Fatal("expected empty database")
	}

	taskIds := make([]string, 150)
	for i := range 150 {
		taskIds[149-i] = f.Run(`--sh 'echo 1'`)
	}

	request := &tes.GetTaskRequest{View: tes.View_BASIC.String()}
	for _, taskId := range taskIds {
		request.Id = taskId
		task, err := f.RPC.GetTask(ctx, request)
		t.Log("GetTask", request.Id, task.State.String(), err)
	}

	r4, err := f.RPC.ListTasks(ctx, &tes.ListTasksRequest{
		PageSize: 50,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Get all pages
	var tasks []*tes.Task
	tasks = append(tasks, r4.Tasks...)
	for r4.NextPageToken != "" {
		r4, err = f.RPC.ListTasks(ctx, &tes.ListTasksRequest{
			PageSize:  50,
			PageToken: r4.NextPageToken,
		})
		if err != nil {
			t.Fatal(err)
		}

		// Check a bug where the end of the last page was being included
		// in the start of the next page.
		if len(r4.Tasks) > 0 && r4.Tasks[0].Id == tasks[len(tasks)-1].Id {
			t.Error("Page start/end bug")
		}

		tasks = append(tasks, r4.Tasks...)
	}

	if len(tasks) != 150 {
		t.Error("unexpected task count", len(tasks))
	}

	min := func(a, b int) int {
		if a < b {
			return a
		}
		return b
	}

	for i := range tasks {
		j := min(i+1, len(tasks)-1)
		if tasks[i].CreationTime < tasks[j].CreationTime {
			t.Error("unexpected task sort order")
		}
	}
}

func TestTaskError(t *testing.T) {
	tests.SetLogOutput(log, t)
	id := fun.Run(`
    --sh 'exit 1'
  `)
	task := fun.Wait(id)

	if task.State != tes.State_EXECUTOR_ERROR {
		t.Fatal("Unexpected task state")
	}
}

func TestLargeLogTail(t *testing.T) {
	tests.SetLogOutput(log, t)
	// Generate lots of random data to stdout.
	// At the end, echo "\n\nhello\n".
	id := fun.Run(`
    --sh 'base64 /dev/urandom | head -c 1000000; echo; echo; echo hello'
  `)
	task := fun.Wait(id)
	if !strings.HasSuffix(task.Logs[0].Logs[0].Stdout, "\n\nhello\n") {
		t.Log("actual:", task.Logs[0].Logs[0].Stdout)
		t.Error("unexpected stdout tail")
	}
}

func TestListTaskFilterState(t *testing.T) {
	tests.SetLogOutput(log, t)

	c := tests.DefaultConfig()
	f := tests.NewFunnel(c)
	f.StartServer()
	ctx := context.Background()

	// will be COMPLETE
	id1 := f.Run(`'echo hello'`)
	// will be COMPLETE
	id2 := f.Run(`'echo hello'`)
	// will be CANCELED
	id3 := f.Run(`'sleep 10'`)

	f.Wait(id1)
	f.Wait(id2)
	f.WaitForRunning(id3)

	err := f.Cancel(id3)
	if err != nil {
		t.Fatal(err)
	}
	f.Wait(id3)

	time.Sleep(500 * time.Millisecond)

	r, err := f.HTTP.ListTasks(ctx, &tes.ListTasksRequest{
		View: tes.Full.String(),
	})
	log.Info("all tasks", "tasks", r.Tasks, "err", err)

	r, err = f.HTTP.ListTasks(ctx, &tes.ListTasksRequest{
		View: tes.View_MINIMAL.String(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Tasks) != 3 {
		t.Error("unexpected all tasks", r.Tasks)
	}

	r, err = f.HTTP.ListTasks(ctx, &tes.ListTasksRequest{
		View:  tes.View_MINIMAL.String(),
		State: tes.Complete,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Tasks) != 2 {
		t.Error("expected 2 tasks", r.Tasks)
	}
	if r.Tasks[0].Id != id2 || r.Tasks[1].Id != id1 {
		t.Error("unexpected complete task IDs", r.Tasks, id2, id1)
	}

	r, err = f.HTTP.ListTasks(ctx, &tes.ListTasksRequest{
		View:  tes.View_MINIMAL.String(),
		State: tes.Canceled,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Tasks) != 1 {
		t.Fatal("expected 1 tasks", r.Tasks)
	}
	if r.Tasks[0].Id != id3 {
		t.Fatal("unexpected canceled task IDs", r.Tasks)
	}
}

func TestListTaskFilterTags(t *testing.T) {
	tests.SetLogOutput(log, t)

	c := tests.DefaultConfig()
	f := tests.NewFunnel(c)
	f.StartServer()
	ctx := context.Background()

	id1 := f.Run(`'echo hello' --tag foo=bar`)
	id2 := f.Run(`'echo hello' --tag foo=bar --tag hello=world`)
	id3 := f.Run(`'echo hello'`)
	id4 := f.Run(`'echo hello' --tag foo=bar-bar`)

	f.Wait(id1)
	f.Wait(id2)
	f.Wait(id3)
	f.Wait(id4)

	r, err := f.HTTP.ListTasks(ctx, &tes.ListTasksRequest{
		View: tes.Full.String(),
	})
	log.Info("all tasks", "tasks", r.Tasks, "err", err)

	r, err = f.HTTP.ListTasks(ctx, &tes.ListTasksRequest{
		View: tes.View_BASIC.String(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Tasks) != 4 {
		t.Error("unexpected all tasks", r.Tasks)
	}

	r, err = f.HTTP.ListTasks(ctx, &tes.ListTasksRequest{
		View:     tes.View_BASIC.String(),
		TagKey:   []string{"foo"},
		TagValue: []string{"bar"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Tasks) != 2 {
		t.Error("expected 2 tasks", r.Tasks)
	}
	if r.Tasks[0].Id != id2 || r.Tasks[1].Id != id1 {
		t.Error("unexpected task IDs", r.Tasks, id2, id1)
	}

	r, err = f.HTTP.ListTasks(ctx, &tes.ListTasksRequest{
		View:     tes.View_BASIC.String(),
		TagKey:   []string{"hello"},
		TagValue: []string{"world"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Tasks) != 1 {
		t.Error("expected 1 tasks", r.Tasks)
	}
	if r.Tasks[0].Id != id2 {
		t.Error("unexpected task IDs", r.Tasks)
	}

	r, err = f.HTTP.ListTasks(ctx, &tes.ListTasksRequest{
		View:   tes.View_BASIC.String(),
		TagKey: []string{"ASFasfa"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Tasks) != 0 {
		t.Error("expected 0 tasks", r.Tasks)
	}
}

func TestListTaskMultipleFilters(t *testing.T) {
	tests.SetLogOutput(log, t)

	c := tests.DefaultConfig()
	f := tests.NewFunnel(c)
	f.StartServer()
	ctx := context.Background()

	// will be COMPLETE
	id1 := f.Run(`'echo hello' --tag foo=bar`)
	// will be COMPLETE
	id2 := f.Run(`'echo hello' --tag foo=bar --tag hello=world`)
	// will be CANCELED
	id3 := f.Run(`'sleep 10' --tag fizz=buzz`)

	f.Wait(id1)
	f.Wait(id2)
	f.WaitForRunning(id3)

	err := f.Cancel(id3)
	if err != nil {
		t.Fatal(err)
	}
	f.Wait(id3)

	time.Sleep(500 * time.Millisecond)

	r, err := f.HTTP.ListTasks(ctx, &tes.ListTasksRequest{
		View: tes.View_FULL.String(),
	})
	log.Info("all tasks", "tasks", r.Tasks, "err", err)

	r, err = f.HTTP.ListTasks(ctx, &tes.ListTasksRequest{
		View: tes.View_BASIC.String(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Tasks) != 3 {
		t.Error("unexpected all tasks", r.Tasks)
	}

	r, err = f.HTTP.ListTasks(ctx, &tes.ListTasksRequest{
		View:     tes.View_BASIC.String(),
		State:    tes.Complete,
		TagKey:   []string{"foo"},
		TagValue: []string{"bar"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Tasks) != 2 {
		t.Error("expected 2 tasks", r.Tasks)
	}
	if r.Tasks[0].Id != id2 || r.Tasks[1].Id != id1 {
		t.Error("unexpected task IDs", r.Tasks, id2, id1)
	}

	r, err = f.HTTP.ListTasks(ctx, &tes.ListTasksRequest{
		View:     tes.View_BASIC.String(),
		State:    tes.Complete,
		TagKey:   []string{"hello"},
		TagValue: []string{"world"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Tasks) != 1 {
		t.Fatal("expected 1 tasks", r.Tasks)
	}
	if r.Tasks[0].Id != id2 {
		t.Fatal("unexpected task IDs", r.Tasks)
	}

	r, _ = f.HTTP.ListTasks(ctx, &tes.ListTasksRequest{
		View:     tes.View_BASIC.String(),
		State:    tes.Canceled,
		TagKey:   []string{"hello"},
		TagValue: []string{"world"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Tasks) != 0 {
		t.Fatal("expected 0 tasks", r.Tasks)
	}

	r, _ = f.HTTP.ListTasks(ctx, &tes.ListTasksRequest{
		View:     tes.View_BASIC.String(),
		State:    tes.Canceled,
		TagKey:   []string{"fizz"},
		TagValue: []string{"buzz"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Tasks) != 1 {
		t.Fatal("expected 1 tasks", r.Tasks)
	}
	if r.Tasks[0].Id != id3 {
		t.Fatal("unexpected task IDs", r.Tasks)
	}
}

func TestConcurrentStateUpdate(t *testing.T) {
	tests.SetLogOutput(log, t)

	ctx := context.Background()
	c := tests.DefaultConfig()
	c.Compute = "noop"
	f := tests.NewFunnel(c)
	f.StartServer()

	var result *multierror.Error

	ids := []string{}
	for i := 0; i < 10; i++ {
		id := f.Run(`--sh 'echo hello'`)
		ids = append(ids, id)

		go func(id string) {
			opts := &workerCmd.Options{TaskID: id}
			w, err := workerCmd.NewWorker(ctx, c, log, opts)
			if err != nil {
				result = multierror.Append(result, err)
				return
			}

			log.Info("writing state initializing event", "taskID", id)
			err = w.EventWriter.WriteEvent(ctx, events.NewState(id, tes.Initializing))
			if err != nil {
				// Not appending errors here as the task may have already been canceled
				log.Error("error writing event", err)
			}
		}(id)

		go func(id string) {
			opts := &workerCmd.Options{TaskID: id}
			w, err := workerCmd.NewWorker(ctx, c, log, opts)
			if err != nil {
				result = multierror.Append(result, err)
				return
			}

			log.Info("writing state canceled event", "taskID", id)
			err = w.EventWriter.WriteEvent(ctx, events.NewState(id, tes.Canceled))
			if err != nil {
				result = multierror.Append(result, err)
				log.Error("error writing event", "error", err, "taskID", id)
			}
		}(id)
	}

	if result != nil {
		t.Error(result)
	}

	for _, i := range ids {
		log.Info("waiting for task", "taskID", i)
		task := f.Wait(i)
		if task.State != tes.Canceled {
			t.Error("expected canceled state", task)
		}
	}
}

func TestMetadataEvent(t *testing.T) {
	tests.SetLogOutput(log, t)

	ctx := context.Background()
	c := tests.DefaultConfig()
	c.Compute = "noop"
	f := tests.NewFunnel(c)
	f.StartServer()

	id := f.Run(`--sh 'echo hello'`)

	w, err := workerCmd.NewWorker(ctx, c, log, &workerCmd.Options{TaskID: id})
	if err != nil {
		t.Fatal(err)
	}
	e := w.EventWriter

	err = e.WriteEvent(ctx, events.NewMetadata(id, 0, map[string]string{"one": "two"}))
	if err != nil {
		t.Error("error writing event", err)
	}
	err = e.WriteEvent(ctx, events.NewMetadata(id, 0, map[string]string{"three": "four"}))
	if err != nil {
		t.Error("error writing event", "error", err, "taskID", id)
	}

	err = w.Run(ctx)
	if err != nil {
		t.Error("error running task", "error", err, "taskID", id)
	}

	task := f.Wait(id)
	if task.State != tes.Complete {
		t.Error("expected complete state", task)
	}

	if len(task.Logs[0].Metadata) != 3 {
		t.Errorf("expected 3 items in task metadata, got %d: %s",
			len(task.Logs[0].Metadata),
			task.Logs[0].Metadata)
	}

	for k, v := range task.Logs[0].Metadata {
		switch k {
		case "one":
			if v != "two" {
				t.Error("unexpected task metadata", task)
			}
		case "three":
			if v != "four" {
				t.Error("unexpected task metadata", task)
			}
		case "hostname":
			// will vary
		default:
			t.Error("unexpected task metadata", task)
		}
	}
}
