package scheduler

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/logger"
	"github.com/calypr/funnel/tes"
	"github.com/stretchr/testify/mock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// queueStub returns a fixed set of tasks on every ReadQueue call.
type queueStub struct {
	tasks []*tes.Task
}

func (q *queueStub) ReadQueue(count int, after *tes.Task) []*tes.Task {
	return q.tasks
}

type atomicTaskAssignerFunc func(context.Context, *tes.Task, *Node) (string, error)

func (f atomicTaskAssignerFunc) TryAssign(ctx context.Context, task *tes.Task, candidate *Node) (string, error) {
	return f(ctx, task, candidate)
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

// The fake health store keeps unit tests focused on scheduler control flow.
// Database fencing is exercised by the shared backend contract.
func (s *MockSchedulerServiceServer) SetNodeState(_ context.Context, node *Node) (*Node, error) {
	return node, nil
}

func newTestScheduler(nodes []*Node, queue TaskQueue, event events.Writer) *Scheduler {
	conf := config.DefaultConfig()
	nodesServer := new(MockSchedulerServiceServer)
	nodesServer.On("ListNodes", mock.Anything, mock.Anything).
		Return(&ListNodesResponse{Nodes: nodes}, nil)
	nodesServer.On("PutNode", mock.Anything, mock.Anything).
		Return(nil, nil)
	nodesServer.On("GetNode", mock.Anything, mock.Anything).
		Return(func(_ context.Context, req *GetNodeRequest) *Node {
			for _, node := range nodes {
				if node.Id == req.Id {
					return node
				}
			}
			return nil
		}, func(_ context.Context, req *GetNodeRequest) error {
			for _, node := range nodes {
				if node.Id == req.Id {
					return nil
				}
			}
			return status.Error(codes.NotFound, "node not found")
		})
	return &Scheduler{
		Conf:  conf.Scheduler,
		Log:   logger.NewLogger("test-scheduler", logger.DefaultConfig()),
		Nodes: nodesServer,
		Queue: queue,
		Event: event,
		AtomicAssigner: atomicTaskAssignerFunc(func(_ context.Context, task *tes.Task, candidate *Node) (string, error) {
			if candidate == nil {
				for _, node := range nodes {
					if slices.Contains(node.TaskIds, task.Id) {
						return node.Id, nil
					}
				}
				return "", nil
			}
			candidate.TaskIds = append(candidate.TaskIds, task.Id)
			return candidate.Id, nil
		}),
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

	s.Nodes.(*MockSchedulerServiceServer).AssertNotCalled(t, "PutNode", mock.Anything, mock.Anything)
	if len(node.TaskIds) != 1 || node.TaskIds[0] != task.Id {
		t.Fatalf("assignment = %v", node.TaskIds)
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

func TestScheduleAssignmentFailureSkipsInitializing(t *testing.T) {
	node := newNode("node-3")
	writer := &recordingWriter{}
	s := newTestScheduler([]*Node{node}, &queueStub{tasks: []*tes.Task{{Id: "task-3"}}}, writer)
	s.AtomicAssigner = atomicTaskAssignerFunc(func(_ context.Context, _ *tes.Task, candidate *Node) (string, error) {
		if candidate == nil {
			return "", nil
		}
		return "", context.DeadlineExceeded
	})
	if err := s.Schedule(context.Background()); err != nil {
		t.Fatal(err)
	}
	if states := stateEvents(writer); len(states) != 0 {
		t.Fatalf("failed reservation must not mark INITIALIZING: %v", states)
	}
}

func TestScheduleUsesAtomicAssignerOwnerAndFetchesFreshNode(t *testing.T) {
	nodeA := newNode("node-a")
	nodeB := newNode("node-b")
	task := &tes.Task{Id: "task-owner"}
	writer := &recordingWriter{}
	s := newTestScheduler([]*Node{nodeA, nodeB}, &queueStub{tasks: []*tes.Task{task}}, writer)
	candidateID := ""
	s.AtomicAssigner = atomicTaskAssignerFunc(func(_ context.Context, task *tes.Task, candidate *Node) (string, error) {
		if candidate == nil {
			return "", nil
		}
		candidateID = candidate.Id
		nodeB.TaskIds = append(nodeB.TaskIds, task.Id)
		return nodeB.Id, nil
	})

	if err := s.Schedule(context.Background()); err != nil {
		t.Fatal(err)
	}

	if candidateID == "" {
		t.Fatal("expected scheduler to pass a candidate to the atomic assigner")
	}
	if len(nodeA.TaskIds) != 0 || len(nodeB.TaskIds) != 1 || nodeB.TaskIds[0] != task.Id {
		t.Fatalf("task was not assigned to the claimed owner: node-a=%v node-b=%v", nodeA.TaskIds, nodeB.TaskIds)
	}
	nodeServer := s.Nodes.(*MockSchedulerServiceServer)
	nodeServer.AssertCalled(t, "GetNode", mock.Anything, &GetNodeRequest{Id: nodeB.Id})
}

func TestScheduleRetriesInitializingForExistingClaimedAssignment(t *testing.T) {
	node := newNode("node-existing")
	node.TaskIds = []string{"task-existing"}
	writer := &recordingWriter{}
	s := newTestScheduler([]*Node{node}, &queueStub{tasks: []*tes.Task{{Id: "task-existing"}}}, writer)
	s.AtomicAssigner = atomicTaskAssignerFunc(func(_ context.Context, task *tes.Task, candidate *Node) (string, error) {
		if candidate != nil || task.Id != "task-existing" {
			t.Fatalf("atomic recovery used unexpected task or candidate: %s %v", task.Id, candidate)
		}
		return node.Id, nil
	})

	if err := s.Schedule(context.Background()); err != nil {
		t.Fatal(err)
	}

	s.Nodes.(*MockSchedulerServiceServer).AssertNotCalled(t, "PutNode", mock.Anything, mock.Anything)
	states := stateEvents(writer)
	if len(states) != 1 || states[0] != tes.State_INITIALIZING {
		t.Fatalf("expected only an INITIALIZING retry, got %v", states)
	}
}

type pagedSchedulerNodeServer struct {
	SchedulerServiceServer
	pages    [][]*Node
	requests []*ListNodesRequest
}

func (s *pagedSchedulerNodeServer) ListNodes(_ context.Context, req *ListNodesRequest) (*ListNodesResponse, error) {
	s.requests = append(s.requests, req)
	page := 0
	switch req.PageToken {
	case "":
		page = 0
	case "page-two":
		page = 1
	default:
		return nil, errors.New("unexpected ListNodes page token")
	}
	if page >= len(s.pages) {
		return nil, errors.New("requested page does not exist")
	}
	response := &ListNodesResponse{Nodes: s.pages[page]}
	if page < len(s.pages)-1 {
		response.NextPageToken = "page-two"
	}
	return response, nil
}

func TestScheduleFindsExistingAssignmentOnLaterNodePage(t *testing.T) {
	nodeA := newNode("node-a")
	nodeB := newNode("node-b")
	nodeB.TaskIds = []string{"task-existing-on-later-page"}
	task := &tes.Task{Id: "task-existing-on-later-page"}
	writer := &recordingWriter{}
	s := newTestScheduler([]*Node{nodeA, nodeB}, &queueStub{tasks: []*tes.Task{task}}, writer)
	paged := &pagedSchedulerNodeServer{
		SchedulerServiceServer: s.Nodes,
		pages:                  [][]*Node{{nodeA}, {nodeB}},
	}
	s.Nodes = paged
	lookupCalls := 0
	s.AtomicAssigner = atomicTaskAssignerFunc(func(_ context.Context, _ *tes.Task, candidate *Node) (string, error) {
		if candidate != nil {
			t.Fatal("existing claim must not be placed again")
		}
		lookupCalls++
		return nodeB.Id, nil
	})

	if err := s.Schedule(context.Background()); err != nil {
		t.Fatal(err)
	}
	if lookupCalls != 1 {
		t.Fatalf("claim lookups = %d, want 1", lookupCalls)
	}
	if len(paged.requests) != 2 || paged.requests[0].PageToken != "" || paged.requests[1].PageToken != "page-two" {
		t.Fatalf("expected one complete snapshot traversal, got %v", paged.requests)
	}

	nodeServer := s.Nodes.(*pagedSchedulerNodeServer).SchedulerServiceServer.(*MockSchedulerServiceServer)
	nodeServer.AssertNotCalled(t, "PutNode", mock.Anything, mock.Anything)
	if states := stateEvents(writer); len(states) != 1 || states[0] != tes.State_INITIALIZING {
		t.Fatalf("existing assignment states = %v, want one INITIALIZING event", states)
	}
}

func TestScheduleFailsTaskWhenAtomicOwnerNodeIsMissing(t *testing.T) {
	task := &tes.Task{Id: "task-missing-owner"}
	writer := &recordingWriter{}
	s := newTestScheduler(nil, &queueStub{tasks: []*tes.Task{task}}, writer)
	passedCandidate := (*Node)(nil)
	s.AtomicAssigner = atomicTaskAssignerFunc(func(_ context.Context, _ *tes.Task, candidate *Node) (string, error) {
		passedCandidate = candidate
		return "missing-node", nil
	})

	if err := s.Schedule(context.Background()); err != nil {
		t.Fatal(err)
	}

	if passedCandidate != nil {
		t.Fatalf("expected nil candidate when no node is eligible, got %v", passedCandidate)
	}
	states := stateEvents(writer)
	if len(states) != 1 || states[0] != tes.State_SYSTEM_ERROR {
		t.Fatalf("expected missing claimed node to fail the task, got states %v", states)
	}
	s.Nodes.(*MockSchedulerServiceServer).AssertNotCalled(t, "PutNode", mock.Anything, mock.Anything)
}

func TestScheduleRequiresAtomicAssignment(t *testing.T) {
	s := newTestScheduler(nil, &queueStub{}, &recordingWriter{})
	s.AtomicAssigner = nil
	if err := s.Schedule(context.Background()); err == nil {
		t.Fatal("scheduler accepted a nontransactional store")
	}
}

func TestScheduleRetriesAtomicAssignmentAfterNodeVersionConflict(t *testing.T) {
	node := newNode("node-no-claimer")
	writer := &recordingWriter{}
	s := newTestScheduler([]*Node{node}, &queueStub{tasks: []*tes.Task{{Id: "task-no-claimer"}}}, writer)
	callCount := 0
	s.AtomicAssigner = atomicTaskAssignerFunc(func(_ context.Context, task *tes.Task, candidate *Node) (string, error) {
		if candidate == nil {
			return "", nil
		}
		callCount++
		if callCount == 1 {
			return "", errors.New("node version conflict")
		}
		candidate.TaskIds = append(candidate.TaskIds, task.Id)
		return candidate.Id, nil
	})

	if err := s.Schedule(context.Background()); err != nil {
		t.Fatal(err)
	}
	if states := stateEvents(writer); len(states) != 0 {
		t.Fatalf("atomic reservation error must not mark task INITIALIZING, got %v", states)
	}
	if err := s.Schedule(context.Background()); err != nil {
		t.Fatal(err)
	}

	if callCount != 2 {
		t.Fatalf("expected retry after atomic node conflict, got %d attempts", callCount)
	}
	s.Nodes.(*MockSchedulerServiceServer).AssertNotCalled(t, "PutNode", mock.Anything, mock.Anything)
	if states := stateEvents(writer); len(states) != 1 || states[0] != tes.State_INITIALIZING {
		t.Fatalf("expected one INITIALIZING event after successful retry, got %v", states)
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
