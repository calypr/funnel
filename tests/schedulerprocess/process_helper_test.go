package schedulerprocess

import (
	"context"
	"fmt"
	"os/signal"
	"syscall"
	"testing"

	servercmd "github.com/calypr/funnel/cmd/server"
	workerCmd "github.com/calypr/funnel/cmd/worker"
	"github.com/calypr/funnel/compute/scheduler"
	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/logger"
	"github.com/calypr/funnel/tes"
	"github.com/calypr/funnel/worker"
)

func runServerHelper(t *testing.T, conf *config.Config, log *logger.Logger) {
	t.Helper()
	srv, err := servercmd.NewServer(context.Background(), conf, log)
	if err != nil {
		t.Fatalf("construct Funnel server: %v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	fmt.Println("PROCESS_EVENT: SERVER_CONFIGURED")
	if err := srv.Run(ctx); err != nil && ctx.Err() == nil {
		t.Fatalf("run Funnel server: %v", err)
	}
}

func runNodeHelper(t *testing.T, conf *config.Config, log *logger.Logger) {
	t.Helper()
	factory := func(ctx context.Context, taskID string) error {
		w, err := workerCmd.NewWorker(ctx, conf, log, &workerCmd.Options{TaskID: taskID})
		if err != nil {
			return err
		}
		defer w.Close()
		return w.Run(ctx)
	}
	node, err := scheduler.NewNodeProcess(context.Background(), conf, factory, log)
	if err != nil {
		t.Fatalf("construct Funnel node: %v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	fmt.Printf("PROCESS_EVENT: NODE_CONFIGURED id=%s\n", conf.Node.ID)
	if err := node.Run(ctx); err != nil && ctx.Err() == nil {
		t.Fatalf("run Funnel node: %v", err)
	}
}

func runSchedulerCommitBarrier(t *testing.T, conf *config.Config, settings dbSettings, log *logger.Logger) {
	t.Helper()
	db := openStore(t, settings, false)
	defer db.Close()
	assigner := &assignmentCommitBarrier{AtomicTaskAssigner: db}
	sched := &scheduler.Scheduler{
		Conf: conf.Scheduler, Log: log, Nodes: db, Queue: db,
		AtomicAssigner: assigner, Event: db,
	}
	if err := sched.Schedule(context.Background()); err != nil {
		t.Fatalf("schedule through production scheduler: %v", err)
	}
	t.Fatal("scheduler returned without reaching the committed-assignment barrier")
}

type assignmentCommitBarrier struct {
	scheduler.AtomicTaskAssigner
}

func (b *assignmentCommitBarrier) TryAssign(ctx context.Context, task *tes.Task, candidate *scheduler.Node) (string, error) {
	owner, err := b.AtomicTaskAssigner.TryAssign(ctx, task, candidate)
	if err == nil && candidate != nil && owner != "" {
		fmt.Printf("PROCESS_EVENT: ASSIGNMENT_COMMITTED task=%s node=%s\n", task.GetId(), owner)
		select {}
	}
	return owner, err
}

func runWorkerAdmissionBarrier(t *testing.T, conf *config.Config, log *logger.Logger) {
	t.Helper()
	taskID := requiredEnv(t, "FUNNEL_PROCESS_TASK_ID")
	w, err := workerCmd.NewWorker(context.Background(), conf, log, &workerCmd.Options{TaskID: taskID})
	if err != nil {
		t.Fatalf("construct Funnel worker: %v", err)
	}
	defer w.Close()
	w.ExecutionClaimer = &executionAdmissionBarrier{ExecutionClaimer: w.ExecutionClaimer}
	if err := w.Run(context.Background()); err != nil {
		t.Fatalf("run Funnel worker to admission boundary: %v", err)
	}
	t.Fatal("worker returned without reaching the execution-admission barrier")
}

type executionAdmissionBarrier struct {
	worker.ExecutionClaimer
}

func (b *executionAdmissionBarrier) ClaimExecution(ctx context.Context, taskID string) (bool, error) {
	claimed, err := b.ExecutionClaimer.ClaimExecution(ctx, taskID)
	if err == nil && claimed {
		fmt.Printf("PROCESS_EVENT: EXECUTION_ADMITTED task=%s\n", taskID)
		select {}
	}
	return claimed, err
}

func runWorkerObserver(t *testing.T, conf *config.Config, log *logger.Logger) {
	t.Helper()
	taskID := requiredEnv(t, "FUNNEL_PROCESS_TASK_ID")
	w, err := workerCmd.NewWorker(context.Background(), conf, log, &workerCmd.Options{TaskID: taskID})
	if err != nil {
		t.Fatalf("construct Funnel observer worker: %v", err)
	}
	defer w.Close()
	w.TaskReader = &initializingTaskObserver{TaskReader: w.TaskReader}
	if err := w.Run(context.Background()); err != nil {
		t.Fatalf("run Funnel observer worker: %v", err)
	}
}

type initializingTaskObserver struct {
	worker.TaskReader
	seen bool
}

func (r *initializingTaskObserver) Task(ctx context.Context) (*tes.Task, error) {
	task, err := r.TaskReader.Task(ctx)
	if err == nil && task.GetState() == tes.State_INITIALIZING && !r.seen {
		r.seen = true
		fmt.Printf("PROCESS_EVENT: INITIALIZING_OBSERVED task=%s\n", task.GetId())
	}
	return task, err
}

var _ events.ExecutionClaimer = (*executionAdmissionBarrier)(nil)
