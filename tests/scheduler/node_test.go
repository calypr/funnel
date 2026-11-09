package scheduler

import (
	"context"
	"fmt"
	"testing"
	"time"

	workercmd "github.com/calypr/funnel/cmd/worker"
	"github.com/calypr/funnel/compute/scheduler"
	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/logger"
	"github.com/calypr/funnel/tes"
	"github.com/calypr/funnel/tests"
	"google.golang.org/protobuf/types/known/durationpb"
)

// When the node's context is canceled (e.g. because the process
// is being killed) the node should signal the database/server
// that it is gone, and the server will delete the node.
func TestNodeGoneOnCanceledContext(t *testing.T) {
	conf := tests.DefaultConfig()
	conf.Compute = "manual"
	conf.Scheduler.NodeInitTimeout = &config.TimeoutConfig{
		TimeoutOption: &config.TimeoutConfig_Duration{
			Duration: durationpb.New(time.Second * 10),
		},
	}
	conf.Scheduler.NodePingTimeout = &config.TimeoutConfig{
		TimeoutOption: &config.TimeoutConfig_Duration{
			Duration: durationpb.New(time.Second * 10),
		},
	}
	conf.Scheduler.NodeDeadTimeout = &config.TimeoutConfig{
		TimeoutOption: &config.TimeoutConfig_Duration{
			Duration: durationpb.New(time.Second * 10),
		},
	}

	bg := context.Background()
	log := logger.NewLogger("node", tests.LogConfig())
	tests.SetLogOutput(log, t)
	srv := tests.NewFunnel(conf)
	srv.StartServer()

	srv.Conf.Node.ID = "test-node-gone-on-cancel"
	n, err := scheduler.NewNodeProcess(bg, srv.Conf, scheduler.NoopWorker, log)
	if err != nil {
		t.Fatal("failed to start node", err)
	}
	ctx, cancel := context.WithCancel(bg)
	runDone := make(chan error, 1)
	go func() { runDone <- n.Run(ctx) }()
	defer func() {
		cancel()
		err := <-runDone
		if err != nil {
			t.Errorf("run node: %v", err)
		}
	}()

	err = srv.Scheduler.CheckNodes()
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(conf.Node.UpdateRate.AsDuration() * 2)

	resp, err := srv.Scheduler.Nodes.ListNodes(bg, &scheduler.ListNodesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	nodes := resp.Nodes

	if len(nodes) != 1 {
		t.Fatal("failed to register node", nodes)
	}

	cancel()
	time.Sleep(conf.Node.UpdateRate.AsDuration() * 2)
	err = srv.Scheduler.CheckNodes()
	if err != nil {
		t.Fatal(err)
	}

	resp, err = srv.Scheduler.Nodes.ListNodes(bg, &scheduler.ListNodesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	nodes = resp.Nodes

	if len(nodes) != 0 {
		t.Error("expected node to be deleted")
	}
}

// Run some tasks with the manual backend
func TestManualBackend(t *testing.T) {
	conf := tests.DefaultConfig()
	conf.Compute = "manual"
	conf.Scheduler.NodeInitTimeout = &config.TimeoutConfig{
		TimeoutOption: &config.TimeoutConfig_Duration{
			Duration: durationpb.New(time.Second * 10),
		},
	}
	conf.Scheduler.NodePingTimeout = &config.TimeoutConfig{
		TimeoutOption: &config.TimeoutConfig_Duration{
			Duration: durationpb.New(time.Second * 10),
		},
	}
	conf.Scheduler.NodeDeadTimeout = &config.TimeoutConfig{
		TimeoutOption: &config.TimeoutConfig_Duration{
			Duration: durationpb.New(time.Second * 10),
		},
	}

	log := logger.NewLogger("node", tests.LogConfig())
	tests.SetLogOutput(log, t)
	srv := tests.NewFunnel(conf)
	srv.StartServer()

	ctx, cancel := context.WithCancel(context.Background())

	srv.Conf.Node.ID = "test-node-manual"
	// create a node
	srv.Conf.Node.ID = "test-node-manual"

	factory := func(ctx context.Context, taskID string) error {
		w, err := workercmd.NewWorker(ctx, conf, log, &workercmd.Options{TaskID: taskID})
		if err != nil {
			return err
		}
		return w.Run(ctx)
	}

	n, err := scheduler.NewNodeProcess(ctx, srv.Conf, factory, log)
	if err != nil {
		t.Fatal("failed to create node", err)
	}
	runDone := make(chan error, 1)
	go func() { runDone <- n.Run(ctx) }()
	defer func() {
		cancel()
		err := <-runDone
		if err != nil {
			t.Errorf("run node: %v", err)
		}
	}()

	// run tasks and check that they all complete
	tasks := []string{}
	for i := 0; i < 10; i++ {
		id := srv.Run(`
      --sh 'echo hello world'
    `)
		tasks = append(tasks, id)
	}

	for _, id := range tasks {
		task := srv.Wait(id)
		time.Sleep(time.Millisecond * 100)
		if task.State != tes.State_COMPLETE {
			t.Fatal("unexpected task state")
		}

		if task.Logs[0].Logs[0].Stdout != "hello world\n" {
			t.Fatalf("Missing stdout for task %s", id)
		}
	}
}

func TestDeadNodeTaskCleanup(t *testing.T) {
	conf := tests.DefaultConfig()
	conf.Compute = "manual"
	conf.Scheduler.NodeInitTimeout = &config.TimeoutConfig{
		TimeoutOption: &config.TimeoutConfig_Duration{
			Duration: durationpb.New(time.Second * 10),
		},
	}
	conf.Scheduler.NodePingTimeout = &config.TimeoutConfig{
		TimeoutOption: &config.TimeoutConfig_Duration{
			Duration: durationpb.New(time.Second * 10),
		},
	}
	conf.Scheduler.NodeDeadTimeout = &config.TimeoutConfig{
		TimeoutOption: &config.TimeoutConfig_Duration{
			Duration: durationpb.New(time.Second * 10),
		},
	}

	log := logger.NewLogger("node", tests.LogConfig())
	tests.SetLogOutput(log, t)
	srv := tests.NewFunnel(conf)
	srv.StartServer()

	srv.Conf.Node.ID = "test-node-manual"
	blockingNoopWorker := func(ctx context.Context, taskID string) error {
		time.Sleep(time.Minute * 10)
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	n, err := scheduler.NewNodeProcess(ctx, srv.Conf, blockingNoopWorker, log)
	if err != nil {
		t.Fatal("failed to create node")
	}
	go func() {
		err := n.Run(ctx)
		if err != nil {
			t.Error(err)
		}
	}()

	id := srv.Run(`
      --sh 'echo hello world'
  `)

	// wait for the task to be assigned to a node
	srv.WaitForInitializing(id)

	// cancel context of Node
	cancel()

	// scheduler should discover node is gone and update the task state accordingly
	task := srv.Wait(id)
	if task.State != tes.State_SYSTEM_ERROR {
		t.Fatal("unexpected task state")
	}
}

// Tests a bug where tasks and nodes were not being correctly cleaned up
// when the node crashed and was restarted.
func TestNodeCleanup(t *testing.T) {
	ctx := context.Background()
	conf := tests.DefaultConfig()
	conf.Compute = "manual"
	srv := tests.NewFunnel(conf)
	tasks := make([]*tes.Task, 5)
	for i := range tasks {
		tasks[i] = tests.HelloWorld()
		if _, err := srv.Server.Tasks.CreateTask(ctx, tasks[i]); err != nil {
			t.Fatal(err)
		}
	}
	for i, indices := range [][]int{{0, 1, 2}, {3}, {4}} {
		node := &scheduler.Node{Id: fmt.Sprintf("cleanup-%d", i), State: scheduler.NodeState_ALIVE, Resources: &scheduler.Resources{Cpus: 100, RamGb: 100, DiskGb: 100}}
		if _, err := srv.Scheduler.Nodes.PutNode(ctx, node); err != nil {
			t.Fatal(err)
		}

		for _, index := range indices {
			if _, err := srv.Scheduler.AtomicAssigner.TryAssign(ctx, tasks[index], node); err != nil {
				t.Fatal(err)
			}
		}
	}
	for i, state := range []tes.State{tes.Complete, tes.Running, tes.SystemError, tes.Running, tes.Running} {
		if _, err := srv.Server.Events.WriteEvent(ctx, events.NewState(tasks[i].Id, state)); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"cleanup-0", "cleanup-1"} {
		node, err := srv.Scheduler.Nodes.GetNode(ctx, &scheduler.GetNodeRequest{Id: id})
		if err != nil {
			t.Fatal(err)
		}
		node.State = scheduler.NodeState_GONE
		if _, err := srv.Scheduler.Nodes.PutNode(ctx, node); err != nil {
			t.Fatal(err)
		}
	}
	err := srv.Scheduler.CheckNodes()
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := scheduler.ListAllNodes(ctx, srv.Scheduler.Nodes)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].Id != "cleanup-2" {
		t.Fatalf("remaining nodes = %v", nodes)
	}
	for i, want := range []tes.State{tes.Complete, tes.SystemError, tes.SystemError, tes.SystemError, tes.Running} {
		if got := srv.GetView(tasks[i].Id, tes.Minimal).State; got != want {
			t.Errorf("task %s state=%s want=%s", tasks[i].Id, got, want)
		}
	}
}

func TestNodeDrain(t *testing.T) {
	conf := tests.DefaultConfig()
	conf.Compute = "manual"
	conf.Scheduler.NodeInitTimeout = &config.TimeoutConfig{
		TimeoutOption: &config.TimeoutConfig_Duration{
			Duration: durationpb.New(time.Second * 10),
		},
	}
	conf.Scheduler.NodePingTimeout = &config.TimeoutConfig{
		TimeoutOption: &config.TimeoutConfig_Duration{
			Duration: durationpb.New(time.Second * 10),
		},
	}
	conf.Scheduler.NodeDeadTimeout = &config.TimeoutConfig{
		TimeoutOption: &config.TimeoutConfig_Duration{
			Duration: durationpb.New(time.Second * 10),
		},
	}

	bg := context.Background()
	log := logger.NewLogger("node", tests.LogConfig())
	tests.SetLogOutput(log, t)
	srv := tests.NewFunnel(conf)
	srv.StartServer()

	// test worker that blocks until the test unblocks it below.
	block := make(chan struct{})
	worker := func(ctx context.Context, taskID string) error {
		select {
		case <-block:
			_, err := srv.Server.Events.WriteEvent(ctx, events.NewState(taskID, tes.Complete))
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	srv.Conf.Node.ID = "test-node-drain"
	n, err := scheduler.NewNodeProcess(bg, srv.Conf, worker, log)
	if err != nil {
		t.Fatal("failed to start node", err)
	}
	ctx, cancel := context.WithCancel(bg)
	runDone := make(chan error, 1)
	go func() { runDone <- n.Run(ctx) }()
	defer func() {
		cancel()
		err := <-runDone
		if err != nil {
			t.Errorf("run node: %v", err)
		}
	}()

	err = srv.Scheduler.CheckNodes()
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(conf.Node.UpdateRate.AsDuration() * 10)

	resp, err := srv.Scheduler.Nodes.ListNodes(bg, &scheduler.ListNodesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	nodes := resp.Nodes

	// Ensure the node started.
	if len(nodes) != 1 {
		t.Fatal("failed to register node", nodes)
	}

	// Start a task, which is expected to be scheduled to the node.
	first := srv.Run("echo")

	time.Sleep(conf.Node.UpdateRate.AsDuration() * 10)
	err = srv.Scheduler.CheckNodes()
	if err != nil {
		t.Fatal(err)
	}
	resp, err = srv.Scheduler.Nodes.ListNodes(bg, &scheduler.ListNodesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	nodes = resp.Nodes

	// Ensure the task was scheduled.
	if len(nodes) != 1 || len(nodes[0].TaskIds) != 1 {
		t.Fatal("expected task to be scheduled to node")
	}

	// Drain the node.
	n.Drain()
	time.Sleep(conf.Node.UpdateRate.AsDuration() * 10)

	// Start a second task, which is expected NOT to be scheduled,
	// since the node is now draining.
	second := srv.Run("echo")

	time.Sleep(conf.Node.UpdateRate.AsDuration() * 10)

	resp, err = srv.Scheduler.Nodes.ListNodes(bg, &scheduler.ListNodesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	nodes = resp.Nodes

	// Ensure the task was scheduled.
	if len(nodes) != 1 || len(nodes[0].TaskIds) != 1 {
		t.Fatal("expected only 1 task to be scheduled to node")
	}

	log.Info("NODE", nodes[0])

	close(block)
	time.Sleep(conf.Node.UpdateRate.AsDuration() * 10)

	resp, err = srv.Scheduler.Nodes.ListNodes(bg, &scheduler.ListNodesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	nodes = resp.Nodes

	if len(nodes) != 0 {
		t.Fatal("expected node to have exited")
	}

	// One last check to ensure the second task wasn't scheduled.
	srv.GetView(first, tes.Minimal)

	if srv.GetView(second, tes.Minimal).State != tes.Queued {
		t.Fatal("expected second task to be queued")
	}
}
