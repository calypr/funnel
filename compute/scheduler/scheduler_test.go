package scheduler

import (
	"context"
	"testing"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/logger"
	"github.com/calypr/funnel/tes"
	"github.com/stretchr/testify/mock"
)

// queueStub returns a fixed set of tasks on every ReadQueue call.
type queueStub struct {
	tasks []*tes.Task
}

func (q *queueStub) ReadQueue(count int, after *tes.Task) []*tes.Task {
	return q.tasks
}

// recordingWriter captures events so tests can assert on state transitions.
type recordingWriter struct {
	events []*events.Event
}

func (w *recordingWriter) WriteEvent(_ context.Context, ev *events.Event) error {
	w.events = append(w.events, ev)
	return nil
}

func (w *recordingWriter) Close() {}

// newNode returns an ALIVE node with resources available for a task.
func newNode(id string) *Node {
	return &Node{
		Id:    id,
		State: NodeState_ALIVE,
		Available: &Resources{
			Cpus:   10,
			RamGb:  10.0,
			DiskGb: 10.0,
		},
	}
}

func newTestScheduler(nodes []*Node, queue TaskQueue, event events.Writer) *Scheduler {
	conf := config.DefaultConfig()
	nodesServer := new(MockSchedulerServiceServer)
	nodesServer.On("ListNodes", mock.Anything, mock.Anything).
		Return(&ListNodesResponse{Nodes: nodes}, nil)
	nodesServer.On("PutNode", mock.Anything, mock.Anything).
		Return(nil, nil)
	return &Scheduler{
		Conf:  conf.Scheduler,
		Log:   logger.NewLogger("test-scheduler", logger.DefaultConfig()),
		Nodes: nodesServer,
		Queue: queue,
		Event: event,
	}
}

// stateEvents returns the task states written to the event writer, in order.
func stateEvents(w *recordingWriter) []tes.State {
	var states []tes.State
	for _, ev := range w.events {
		if state := ev.GetState(); state != tes.State_UNKNOWN {
			states = append(states, state)
		}
	}
	return states
}

// TestScheduleAssignsTaskAndMarksInitializing covers the transition flagged as
// important since 2017: when the scheduler assigns a task to a node, the
// task's state is written as INITIALIZING immediately — before the node (and
// its worker) has received or started the task. Without this immediate
// transition the task would sit QUEUED forever if a worker never picked it up.
func TestScheduleAssignsTaskAndMarksInitializing(t *testing.T) {
	node := newNode("node-1")
	task := &tes.Task{Id: "task-1"}
	writer := &recordingWriter{}
	s := newTestScheduler([]*Node{node}, &queueStub{tasks: []*tes.Task{task}}, writer)

	if err := s.Schedule(context.Background()); err != nil {
		t.Fatal(err)
	}

	// The node record was updated with the assignment.
	nodeServer := s.Nodes.(*MockSchedulerServiceServer)
	nodeServer.AssertNumberOfCalls(t, "PutNode", 1)
	var put *Node
	for _, call := range nodeServer.Calls {
		if call.Method == "PutNode" {
			put, _ = call.Arguments.Get(1).(*Node)
		}
	}
	if put == nil || len(put.TaskIds) != 1 || put.TaskIds[0] != "task-1" {
		t.Fatalf("PutNode did not assign the task to the node: %v", put)
	}

	// The state moved straight to INITIALIZING, before any node action.
	states := stateEvents(writer)
	if len(states) != 1 || states[0] != tes.State_INITIALIZING {
		t.Fatalf("expected exactly one INITIALIZING state event, got %v", states)
	}
}

// TestScheduleNoOfferLeavesTaskQueued verifies that a task with no matching
// node is not assigned and its state is untouched.
func TestScheduleNoOfferLeavesTaskQueued(t *testing.T) {
	task := &tes.Task{Id: "task-2"}
	writer := &recordingWriter{}
	s := newTestScheduler(nil, &queueStub{tasks: []*tes.Task{task}}, writer)

	if err := s.Schedule(context.Background()); err != nil {
		t.Fatal(err)
	}

	if states := stateEvents(writer); len(states) != 0 {
		t.Fatalf("task without an offer must not transition state, got %v", states)
	}
}

// TestSchedulePutNodeFailureSkipsInitializing verifies the failure path: if
// the node record cannot be written, the assignment did not happen and the
// task must not be marked INITIALIZING — otherwise it would be stranded in a
// state no worker will ever pick up.
func TestSchedulePutNodeFailureSkipsInitializing(t *testing.T) {
	node := newNode("node-3")
	task := &tes.Task{Id: "task-3"}
	writer := &recordingWriter{}

	conf := config.DefaultConfig()
	nodesServer := new(MockSchedulerServiceServer)
	nodesServer.On("ListNodes", mock.Anything, mock.Anything).
		Return(&ListNodesResponse{Nodes: []*Node{node}}, nil)
	nodesServer.On("PutNode", mock.Anything, mock.Anything).
		Return(nil, context.DeadlineExceeded)

	s := &Scheduler{
		Conf:  conf.Scheduler,
		Log:   logger.NewLogger("test-scheduler", logger.DefaultConfig()),
		Nodes: nodesServer,
		Queue: &queueStub{tasks: []*tes.Task{task}},
		Event: writer,
	}

	if err := s.Schedule(context.Background()); err != nil {
		t.Fatal(err)
	}

	if states := stateEvents(writer); len(states) != 0 {
		t.Fatalf("failed PutNode must not mark the task INITIALIZING, got %v", states)
	}
}

type limitedQueue struct{ tasks []*tes.Task }

func (q *limitedQueue) ReadQueue(count int, after *tes.Task) []*tes.Task {
	start := 0
	if after != nil {
		for start < len(q.tasks) && q.tasks[start].Id <= after.Id {
			start++
		}
	}
	end := start + count
	if end > len(q.tasks) {
		end = len(q.tasks)
	}
	return q.tasks[start:end]
}

func TestScheduleReachesRunnableTaskBeyondBlockedChunk(t *testing.T) {
	node := newNode("node-zone-b")
	node.Zone = "b"
	queue := &limitedQueue{tasks: []*tes.Task{
		{Id: "blocked-1", Resources: &tes.Resources{Zones: []string{"a"}}},
		{Id: "blocked-2", Resources: &tes.Resources{Zones: []string{"a"}}},
		{Id: "runnable", Resources: &tes.Resources{Zones: []string{"b"}}},
	}}
	s := newTestScheduler([]*Node{node}, queue, &recordingWriter{})
	s.Conf.ScheduleChunk = 2
	for i := 0; i < 2; i++ {
		if err := s.Schedule(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(node.TaskIds) != 1 || node.TaskIds[0] != "runnable" {
		t.Fatalf("runnable task remained queued after 2 scheduling passes; node assignments: %v", node.TaskIds)
	}
}

func TestScheduleReturnsToBlockedTasks(t *testing.T) {
	node := newNode("node")
	node.Zone = "b"
	q := &limitedQueue{tasks: []*tes.Task{
		{Id: "a", Resources: &tes.Resources{Zones: []string{"a"}}},
		{Id: "b", Resources: &tes.Resources{Zones: []string{"a"}}},
		{Id: "c", Resources: &tes.Resources{Zones: []string{"a"}}},
	}}
	s := newTestScheduler([]*Node{node}, q, &recordingWriter{})
	s.Conf.ScheduleChunk = 2
	if err := s.Schedule(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.Schedule(context.Background()); err != nil {
		t.Fatal(err)
	}
	node.Zone = "a"
	if err := s.Schedule(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(node.TaskIds) != 2 || node.TaskIds[0] != "a" || node.TaskIds[1] != "b" {
		t.Fatalf("scheduler did not return to first blocked tasks: %v", node.TaskIds)
	}
}

func TestScheduleContinuesThroughQueueInOrder(t *testing.T) {
	node := newNode("node")
	node.Zone = "b"
	q := &limitedQueue{tasks: []*tes.Task{}}
	for _, id := range []string{"a", "b", "c", "d", "e", "f"} {
		q.tasks = append(q.tasks, &tes.Task{Id: id, Resources: &tes.Resources{Zones: []string{"a"}}})
	}
	s := newTestScheduler([]*Node{node}, q, &recordingWriter{})
	s.Conf.ScheduleChunk = 2
	for i := 0; i < 2; i++ {
		if err := s.Schedule(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	node.Zone = "a"
	if err := s.Schedule(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(node.TaskIds) != 2 || node.TaskIds[0] != "e" || node.TaskIds[1] != "f" {
		t.Fatalf("did not continue to the next queued tasks: %v", node.TaskIds)
	}
}
