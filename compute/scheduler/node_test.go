package scheduler

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/calypr/funnel/config"
	"github.com/stretchr/testify/mock"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/durationpb"
)

// Test calling stopping a node by canceling its context
func TestStopNode(t *testing.T) {
	conf := config.DefaultConfig()
	conf.Node.UpdateRate = durationpb.New(time.Millisecond * 2)
	n := newTestNode(conf, t)

	n.Client.On("GetNode", mock.Anything, mock.Anything, mock.Anything).
		Return(emptyNodeResponse, nil)

	stop := n.Start()

	// Fail if this test doesn't complete in the given time.
	cleanup := timeLimit(t, time.Millisecond*100)
	defer cleanup()
	stop()
	err := n.Wait()
	if err != nil {
		t.Fatal(err)
	}
	n.Client.AssertCalled(t, "Close")
}

func TestDrainPublishesBeforeNextUpdate(t *testing.T) {
	conf := config.DefaultConfig()
	conf.Node.UpdateRate = durationpb.New(time.Hour)
	n := newTestNode(conf, t)

	client := new(MockClient)
	client.On("Close").Return(nil)
	n.Client = client
	n.client = client

	getNodeCalls := atomic.Int32{}
	client.On("GetNode", mock.Anything, mock.Anything, mock.Anything).
		Return(func(context.Context, *GetNodeRequest, ...grpc.CallOption) *Node {
			if getNodeCalls.Add(1) == 2 {
				return &Node{TaskIds: []string{"active-task"}}
			}
			return &Node{}
		}, nil)

	workerStarted := make(chan struct{})
	workerCanceled := make(chan struct{}, 1)
	finishWorker := make(chan struct{})
	n.workerRun = func(ctx context.Context, _ string) error {
		close(workerStarted)
		select {
		case <-ctx.Done():
			workerCanceled <- struct{}{}
		case <-finishWorker:
		}
		return nil
	}

	putStates := make(chan NodeState, 4)
	client.On("PutNode", mock.Anything, mock.Anything).
		Return(nil, nil).Run(func(args mock.Arguments) {
		putStates <- args.Get(1).(*Node).State
	})

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	runErr := make(chan error, 1)
	go func() {
		runErr <- n.Run(ctx)
		close(runDone)
	}()
	defer func() {
		cancel()
		close(finishWorker)
		select {
		case <-runDone:
			err := <-runErr
			if err != nil {
				t.Errorf("run node: %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("node did not stop during test cleanup")
		}
	}()

	select {
	case state := <-putStates:
		if state != NodeState_ALIVE {
			t.Fatalf("first node update state = %v, want ALIVE", state)
		}
	case <-time.After(time.Second):
		t.Fatal("node did not publish its initial state")
	}
	select {
	case <-workerStarted:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	n.Drain()

	select {
	case state := <-putStates:
		if state != NodeState_DRAIN {
			t.Fatalf("node update state = %v, want DRAIN", state)
		}
	case <-time.After(time.Second):
		t.Fatal("node did not publish DRAIN before the next update")
	}

	select {
	case <-workerCanceled:
		t.Fatal("draining canceled the active worker")
	default:
	}
	select {
	case <-runDone:
		t.Fatal("node exited before the active worker finished")
	default:
	}
}

// Mainly exercising a panic bug caused by an unhandled
// error from client.GetNode().
func TestGetNodeFail(t *testing.T) {
	conf := config.DefaultConfig()
	conf.Node.UpdateRate = durationpb.New(time.Millisecond * 2)
	n := newTestNode(conf, t)

	// Set GetNode to return an error
	n.Client.On("GetNode", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("TEST"))
	n.sync(context.Background())
	time.Sleep(time.Second)
}

// Test the flow of a node completing a task then timing out
func TestNodeTimeout(t *testing.T) {
	conf := config.DefaultConfig()
	conf.Node.Timeout = &config.TimeoutConfig{
		TimeoutOption: &config.TimeoutConfig_Duration{
			Duration: durationpb.New(time.Millisecond),
		},
	}
	durationpb.New(time.Millisecond)
	conf.Node.UpdateRate = durationpb.New(time.Millisecond * 2)

	n := newTestNode(conf, t)

	// Set up a test worker which this code can easily control.
	//w := testWorker{}
	// Hook the test worker up to the node's worker factory.
	//n.newWorker = Worker(w.Factory)

	// Set up scheduler mock to return a task
	n.AddTasks("task-1")

	n.Start()

	// Fail if this test doesn't complete in the given time.
	cleanup := timeLimit(t, conf.Node.Timeout.GetDuration().AsDuration()*500)
	defer cleanup()

	// Wait for the node to exit
	err := n.Wait()
	if err != nil {
		t.Fatal(err)
	}
}

// Test that a node does nothing where there are no assigned tasks.
func TestNoTasks(t *testing.T) {
	conf := config.DefaultConfig()
	conf.Node.UpdateRate = durationpb.New(time.Millisecond * 2)
	n := newTestNode(conf, t)

	// Tell the scheduler mock to return nothing
	n.Client.On("GetNode", mock.Anything, mock.Anything, mock.Anything).
		Return(emptyNodeResponse, nil)

	// Count the number of times the worker factory was called
	var count atomic.Int32
	n.workerRun = func(context.Context, string) error {
		count.Add(1)
		return nil
	}

	n.sync(context.Background())
	n.sync(context.Background())
	n.sync(context.Background())
	time.Sleep(time.Second)

	if count.Load() != 0 {
		t.Fatal("Unexpected worker factory call count")
	}
	if n.workers.Count() != 0 {
		t.Fatal("Unexpected node worker count")
	}
}

// Test that a worker gets created for each task.
func TestNodeWorkerCreated(t *testing.T) {
	conf := config.DefaultConfig()
	conf.Node.UpdateRate = durationpb.New(time.Millisecond * 2)
	n := newTestNode(conf, t)

	// Count the number of times the worker factory was called
	var count atomic.Int32
	n.workerRun = func(context.Context, string) error {
		count.Add(1)
		return nil
	}

	n.AddTasks("task-1", "task-2")
	n.sync(context.Background())
	time.Sleep(time.Second)

	if count.Load() != 2 {
		t.Fatalf("Unexpected node worker count: %d", count.Load())
	}
}

// Test that a finished task is not immediately re-run.
// Tests a bugfix.
func TestFinishedTaskNotRerun(t *testing.T) {
	conf := config.DefaultConfig()
	conf.Node.UpdateRate = durationpb.New(time.Millisecond * 2)
	n := newTestNode(conf, t)

	// Set up a test worker which this code can easily control.
	//w := testWorker{}
	// Hook the test worker up to the node's worker factory.
	//n.newWorker = Worker(w.Factory)

	n.AddTasks("task-1")

	// manually sync the node to avoid timing issues.
	n.sync(context.Background())
	time.Sleep(time.Second)

	if n.workers.Count() != 0 {
		t.Fatalf("Unexpected worker count: %d", n.workers.Count())
	}

	// There was a bug where later syncs would end up re-running the task.
	// Do a few syncs to make sure.
	n.sync(context.Background())
	n.sync(context.Background())
	time.Sleep(time.Second)

	if n.workers.Count() != 0 {
		t.Fatalf("Unexpected worker count: %d", n.workers.Count())
	}
}

// Test that tasks are removed from the node's runset when they finish.
func TestFinishedTaskRunsetCount(t *testing.T) {
	conf := config.DefaultConfig()
	conf.Node.UpdateRate = durationpb.New(time.Millisecond * 2)
	n := newTestNode(conf, t)

	// Set up a test worker which this code can easily control.
	//w := testWorker{}
	// Hook the test worker up to the node's worker factory.
	//n.newWorker = Worker(w.Factory)

	n.AddTasks("task-1")

	// manually sync the node to avoid timing issues.
	n.sync(context.Background())
	time.Sleep(time.Second)

	if n.workers.Count() != 0 {
		t.Fatalf("Unexpected worker count: %d", n.workers.Count())
	}
}
