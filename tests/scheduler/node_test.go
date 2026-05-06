package scheduler

import (
	"context"
	"testing"
	"time"

	workercmd "github.com/ohsu-comp-bio/funnel/cmd/worker"
	"github.com/ohsu-comp-bio/funnel/compute/scheduler"
	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/events"
	"github.com/ohsu-comp-bio/funnel/logger"
	"github.com/ohsu-comp-bio/funnel/tes"
	"github.com/ohsu-comp-bio/funnel/tests"
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
	defer cancel()
	go n.Run(ctx)

	srv.Scheduler.CheckNodes()
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
	srv.Scheduler.CheckNodes()

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
	defer cancel()

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
	go n.Run(ctx)

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
	log := logger.NewLogger("node", tests.LogConfig())
	ctx := context.Background()

	conf := tests.DefaultConfig()
	conf.Compute = "manual"
	srv := tests.NewFunnel(conf)

	e := srv.Server.Events

	t1 := tests.HelloWorld()
	srv.Server.Tasks.CreateTask(ctx, t1)
	e.WriteEvent(ctx, events.NewState(t1.Id, tes.Complete))

	t2 := tests.HelloWorld()
	srv.Server.Tasks.CreateTask(ctx, t2)
	e.WriteEvent(ctx, events.NewState(t2.Id, tes.Running))

	t3 := tests.HelloWorld()
	srv.Server.Tasks.CreateTask(ctx, t3)
	e.WriteEvent(ctx, events.NewState(t3.Id, tes.SystemError))

	t4 := tests.HelloWorld()
	srv.Server.Tasks.CreateTask(ctx, t4)
	e.WriteEvent(ctx, events.NewState(t4.Id, tes.Running))

	t5 := tests.HelloWorld()
	srv.Server.Tasks.CreateTask(ctx, t5)
	e.WriteEvent(ctx, events.NewState(t5.Id, tes.Running))

	srv.Scheduler.Nodes.PutNode(ctx, &scheduler.Node{
		Id:      "test-gone-node-cleanup-restart-1",
		State:   scheduler.NodeState_GONE,
		TaskIds: []string{t1.Id, t2.Id, t3.Id},
	})

	srv.Scheduler.Nodes.PutNode(ctx, &scheduler.Node{
		Id:      "test-gone-node-cleanup-restart-2",
		State:   scheduler.NodeState_GONE,
		TaskIds: []string{t4.Id},
	})

	srv.Scheduler.Nodes.PutNode(ctx, &scheduler.Node{
		Id:      "test-gone-node-cleanup-restart-3",
		State:   scheduler.NodeState_ALIVE,
		TaskIds: []string{t5.Id},
	})

	ns, _ := srv.Scheduler.Nodes.ListNodes(ctx, &scheduler.ListNodesRequest{})
	log.Info("nodes before", ns)

	err := srv.Scheduler.CheckNodes()
	if err != nil {
		t.Error(err)
	}

	ns, _ = srv.Scheduler.Nodes.ListNodes(ctx, &scheduler.ListNodesRequest{})
	if len(ns.Nodes) != 1 {
		t.Error("expected 1 node")
	}

	if ns.Nodes[0].Id != "test-gone-node-cleanup-restart-3" {
		t.Error("unexpected node")
	}

	ts, _ := srv.Server.Tasks.ListTasks(ctx, &tes.ListTasksRequest{})
	if len(ts.Tasks) != 5 {
		log.Info("tasks", ts)
		t.Error("expected 5 tasks")
	}

	expected := []tes.State{
		tes.Running,
		tes.SystemError,
		tes.SystemError,
		tes.SystemError,
		tes.Complete,
	}

	for i, task := range ts.Tasks {
		e := expected[i]
		if task.State != e {
			t.Error("expected state for task", i, task.State, e)
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
		<-block
		return nil
	}

	srv.Conf.Node.ID = "test-node-drain"
	n, err := scheduler.NewNodeProcess(bg, srv.Conf, worker, log)
	if err != nil {
		t.Fatal("failed to start node", err)
	}
	ctx, cancel := context.WithCancel(bg)
	defer cancel()
	go n.Run(ctx)

	srv.Scheduler.CheckNodes()
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
	srv.Scheduler.CheckNodes()
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
