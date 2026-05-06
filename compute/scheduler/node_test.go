package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/stretchr/testify/mock"
	"google.golang.org/protobuf/types/known/durationpb"
)

// Test calling stopping a node by canceling its context
func TestStopNode(t *testing.T) {
	conf := config.DefaultConfig()
	conf.Node.UpdateRate = durationpb.New(time.Millisecond * 2)
	n := newTestNode(conf, t)

	n.Client.On("GetNode", mock.Anything, mock.Anything, mock.Anything).
		Return(&Node{}, nil)

	stop := n.Start()

	// Fail if this test doesn't complete in the given time.
	cleanup := timeLimit(t, time.Millisecond*100)
	defer cleanup()
	stop()
	n.Wait()
	n.Client.AssertCalled(t, "Close")
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
	n.Wait()
}

// Test that a node does nothing where there are no assigned tasks.
func TestNoTasks(t *testing.T) {
	conf := config.DefaultConfig()
	conf.Node.UpdateRate = durationpb.New(time.Millisecond * 2)
	n := newTestNode(conf, t)

	// Tell the scheduler mock to return nothing
	n.Client.On("GetNode", mock.Anything, mock.Anything, mock.Anything).
		Return(&Node{}, nil)

	// Count the number of times the worker factory was called
	var count int
	n.workerRun = func(context.Context, string) error {
		count++
		return nil
	}

	n.sync(context.Background())
	n.sync(context.Background())
	n.sync(context.Background())
	time.Sleep(time.Second)

	if count != 0 {
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
	var count int
	n.workerRun = func(context.Context, string) error {
		count++
		return nil
	}

	n.AddTasks("task-1", "task-2")
	n.sync(context.Background())
	time.Sleep(time.Second)

	if count != 2 {
		t.Fatalf("Unexpected node worker count: %d", count)
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
