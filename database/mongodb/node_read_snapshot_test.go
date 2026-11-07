package mongodb

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/calypr/funnel/compute/scheduler"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/server"
	"github.com/calypr/funnel/tes"
	"github.com/calypr/funnel/tests/mongotest"
	"go.mongodb.org/mongo-driver/v2/event"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"google.golang.org/protobuf/proto"
)

type nodeSnapshotReadBarrier struct {
	database string
	armed    atomic.Bool
	target   atomic.Int64
	read     chan struct{}
	resume   chan struct{}
}

func (b *nodeSnapshotReadBarrier) started(_ context.Context, evt *event.CommandStartedEvent) {
	if evt.DatabaseName != b.database || evt.CommandName != "find" {
		return
	}
	collection, ok := evt.Command.Lookup("find").StringValueOK()
	if !ok || collection != "nodes" || !b.armed.CompareAndSwap(true, false) {
		return
	}
	b.target.Store(evt.RequestID)
}

func (b *nodeSnapshotReadBarrier) succeeded(ctx context.Context, evt *event.CommandSucceededEvent) {
	if evt.RequestID != b.target.Load() {
		return
	}
	close(b.read)
	select {
	case <-b.resume:
	case <-ctx.Done():
	}
}

func TestNodeReadReservationSnapshot(t *testing.T) {
	uri := mongotest.Start(t)
	const database = "node_read_snapshot"
	db := newMongoTestDB(t, uri, database)
	if err := db.Init(); err != nil {
		t.Fatalf("initialize MongoDB: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), server.UserInfoKey,
		server.UserInfo{Username: "node-snapshot-test", IsAdmin: true}), 30*time.Second)
	defer cancel()

	for _, list := range []bool{false, true} {
		for _, release := range []bool{false, true} {
			name := "GetNode"
			if list {
				name = "ListNodes"
			}
			if release {
				name += "/completion"
			} else {
				name += "/assignment"
			}
			t.Run(name, func(t *testing.T) {
				nodeID := "snapshot-" + strings.ToLower(strings.ReplaceAll(name, "/", "-"))
				taskID := nodeID + "-task"
				task := &tes.Task{
					Id:        taskID,
					State:     tes.State_QUEUED,
					Resources: &tes.Resources{CpuCores: 1, RamGb: 1, DiskGb: 1},
				}
				if err := db.insertTask(ctx, task); err != nil {
					t.Fatalf("create task: %v", err)
				}
				reported := &scheduler.Node{
					Id:        nodeID,
					State:     scheduler.NodeState_ALIVE,
					Resources: &scheduler.Resources{Cpus: 4, RamGb: 8, DiskGb: 10},
				}
				if _, err := db.PutNode(ctx, reported); err != nil {
					t.Fatalf("create node: %v", err)
				}
				candidate, err := db.GetNode(ctx, &scheduler.GetNodeRequest{Id: nodeID})
				if err != nil {
					t.Fatalf("read candidate node: %v", err)
				}
				if release {
					if owner, err := db.TryAssign(ctx, task, candidate); err != nil || owner != nodeID {
						t.Fatalf("assign before completion: owner=%q err=%v", owner, err)
					}
				}
				before, err := readNodeSnapshotView(ctx, db, list, nodeID)
				if err != nil {
					t.Fatalf("read before snapshot: %v", err)
				}

				barrier := &nodeSnapshotReadBarrier{
					database: database,
					read:     make(chan struct{}),
					resume:   make(chan struct{}),
				}
				var resumeOnce sync.Once
				resume := func() { resumeOnce.Do(func() { close(barrier.resume) }) }
				t.Cleanup(resume)
				monitored, err := mongo.Connect(options.Client().ApplyURI(uri).SetMonitor(&event.CommandMonitor{
					Started:   barrier.started,
					Succeeded: barrier.succeeded,
				}))
				if err != nil {
					t.Fatalf("connect monitored MongoDB client: %v", err)
				}
				oldClient := db.client
				db.client = monitored
				db.database = monitored.Database(database)
				if err := oldClient.Disconnect(ctx); err != nil {
					t.Fatalf("close unmonitored MongoDB client: %v", err)
				}
				barrier.armed.Store(true)

				type readResult struct {
					node *scheduler.Node
					err  error
				}
				result := make(chan readResult, 1)
				go func() {
					node, err := readNodeSnapshotView(ctx, db, list, nodeID)
					result <- readResult{node: node, err: err}
				}()
				select {
				case <-barrier.read:
				case got := <-result:
					t.Fatalf("node read completed before controlled query boundary: %v", got.err)
				case <-ctx.Done():
					t.Fatalf("waiting for node read boundary: %v", ctx.Err())
				}

				if release {
					err = db.WriteEvent(ctx, events.NewState(taskID, tes.State_COMPLETE))
				} else {
					var owner string
					owner, err = db.TryAssign(ctx, task, candidate)
					if err == nil && owner != nodeID {
						t.Fatalf("assignment owner = %q, want %q", owner, nodeID)
					}
				}
				if err != nil {
					resume()
					t.Fatalf("mutate reservation during read: %v", err)
				}
				resume()

				var got readResult
				select {
				case got = <-result:
				case <-ctx.Done():
					t.Fatalf("node read did not finish after releasing barrier: %v", ctx.Err())
				}
				if got.err != nil {
					t.Fatal(got.err)
				}
				after, err := readNodeSnapshotView(ctx, db, list, nodeID)
				if err != nil {
					t.Fatalf("read after snapshot: %v", err)
				}
				if sameNodeReadReservationView(before, after) {
					t.Fatal("reservation mutation did not change the node view")
				}
				if !sameNodeReadReservationView(got.node, before) && !sameNodeReadReservationView(got.node, after) {
					t.Errorf("non-serial node response: available=%v tasks=%v; before available=%v tasks=%v; after available=%v tasks=%v",
						got.node.Available, got.node.TaskIds, before.Available, before.TaskIds, after.Available, after.TaskIds)
				}
			})
		}
	}
}

func readNodeSnapshotView(ctx context.Context, db *MongoDB, list bool, nodeID string) (*scheduler.Node, error) {
	if !list {
		return db.GetNode(ctx, &scheduler.GetNodeRequest{Id: nodeID})
	}
	response, err := db.ListNodes(ctx, &scheduler.ListNodesRequest{})
	if err != nil {
		return nil, err
	}
	for _, node := range response.Nodes {
		if node.Id == nodeID {
			return node, nil
		}
	}
	return nil, errors.New("node not returned by ListNodes")
}

func sameNodeReadReservationView(left, right *scheduler.Node) bool {
	return proto.Equal(left.Available, right.Available) && slices.Equal(left.TaskIds, right.TaskIds)
}

type nodeReadFindCounts struct {
	database       string
	nodeFinds      atomic.Int64
	taskFinds      atomic.Int64
	heartbeatFinds atomic.Int64
}

func (c *nodeReadFindCounts) started(_ context.Context, evt *event.CommandStartedEvent) {
	if evt.DatabaseName != c.database || evt.CommandName != "find" {
		return
	}
	collection, ok := evt.Command.Lookup("find").StringValueOK()
	if !ok {
		return
	}
	switch collection {
	case "nodes":
		c.nodeFinds.Add(1)
	case "tasks":
		c.taskFinds.Add(1)
	case "node_heartbeats":
		c.heartbeatFinds.Add(1)
	}
}

func (c *nodeReadFindCounts) reset() {
	c.nodeFinds.Store(0)
	c.taskFinds.Store(0)
	c.heartbeatFinds.Store(0)
}

func TestNodeReadQueryBudgetAndCallerCancellation(t *testing.T) {
	uri := mongotest.Start(t)
	const database = "node_read_query_budget"
	db := newMongoTestDB(t, uri, database)
	if err := db.Init(); err != nil {
		t.Fatalf("initialize MongoDB: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), server.UserInfoKey,
		server.UserInfo{Username: "node-query-budget-test", IsAdmin: true}), 30*time.Second)
	defer cancel()

	for _, suffix := range []string{"a", "b"} {
		taskID := "budget-task-" + suffix
		task := &tes.Task{Id: taskID, State: tes.State_QUEUED, Resources: &tes.Resources{CpuCores: 1, RamGb: 1, DiskGb: 1}}
		if err := db.insertTask(ctx, task); err != nil {
			t.Fatalf("create task: %v", err)
		}
		nodeID := "budget-node-" + suffix
		reported := &scheduler.Node{
			Id:        nodeID,
			State:     scheduler.NodeState_ALIVE,
			Resources: &scheduler.Resources{Cpus: 4, RamGb: 8, DiskGb: 10},
		}
		if _, err := db.PutNode(ctx, reported); err != nil {
			t.Fatalf("create node: %v", err)
		}
		candidate, err := db.GetNode(ctx, &scheduler.GetNodeRequest{Id: nodeID})
		if err != nil {
			t.Fatalf("read candidate node: %v", err)
		}
		if owner, err := db.TryAssign(ctx, task, candidate); err != nil || owner != nodeID {
			t.Fatalf("assign task: owner=%q err=%v", owner, err)
		}
	}

	counts := &nodeReadFindCounts{database: database}
	monitored, err := mongo.Connect(options.Client().ApplyURI(uri).SetMonitor(&event.CommandMonitor{Started: counts.started}))
	if err != nil {
		t.Fatalf("connect monitored MongoDB client: %v", err)
	}
	oldClient := db.client
	db.client = monitored
	db.database = monitored.Database(database)
	if err := oldClient.Disconnect(ctx); err != nil {
		t.Fatalf("close unmonitored MongoDB client: %v", err)
	}

	counts.reset()
	assigned, err := db.GetNode(ctx, &scheduler.GetNodeRequest{Id: "budget-node-a"})
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if !slices.Equal(assigned.TaskIds, []string{"budget-task-a"}) || counts.nodeFinds.Load() != 1 || counts.taskFinds.Load() > 1 || counts.heartbeatFinds.Load() > 1 {
		t.Fatalf("GetNode returned tasks %v with node/task/heartbeat find counts %d/%d/%d; want [budget-task-a] and at most 1/1/1 finds",
			assigned.TaskIds, counts.nodeFinds.Load(), counts.taskFinds.Load(), counts.heartbeatFinds.Load())
	}

	counts.reset()
	listed, err := db.ListNodes(ctx, &scheduler.ListNodesRequest{})
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}
	if len(listed.Nodes) != 2 || counts.nodeFinds.Load() != 1 || counts.taskFinds.Load() > 1 || counts.heartbeatFinds.Load() > 1 {
		t.Fatalf("ListNodes returned %d nodes with node/task/heartbeat find counts %d/%d/%d; want 2 and at most 1/1/1 finds",
			len(listed.Nodes), counts.nodeFinds.Load(), counts.taskFinds.Load(), counts.heartbeatFinds.Load())
	}

	counts.reset()
	candidates, err := db.ListNodeCandidates(ctx, &scheduler.ListNodesRequest{})
	if err != nil {
		t.Fatalf("ListNodeCandidates: %v", err)
	}
	if len(candidates.Nodes) != 2 || counts.nodeFinds.Load() != 1 || counts.taskFinds.Load() != 0 || counts.heartbeatFinds.Load() != 1 {
		t.Fatalf("ListNodeCandidates returned %d nodes with node/task/heartbeat find counts %d/%d/%d; want 2 and 1/0/1 finds",
			len(candidates.Nodes), counts.nodeFinds.Load(), counts.taskFinds.Load(), counts.heartbeatFinds.Load())
	}
	for _, node := range candidates.Nodes {
		if len(node.TaskIds) != 0 {
			t.Fatalf("candidate node %q unexpectedly includes task IDs: %v", node.Id, node.TaskIds)
		}
		if node.LastPing == 0 {
			t.Fatalf("candidate node %q lost its heartbeat timestamp", node.Id)
		}
	}

	canceled, cancelCaller := context.WithCancel(ctx)
	cancelCaller()
	if _, err := db.GetNode(canceled, &scheduler.GetNodeRequest{Id: "budget-node-a"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("GetNode with canceled caller context = %v, want context.Canceled", err)
	}
	if _, err := db.ListNodes(canceled, &scheduler.ListNodesRequest{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("ListNodes with canceled caller context = %v, want context.Canceled", err)
	}
	if _, err := db.ListNodeCandidates(canceled, &scheduler.ListNodesRequest{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("ListNodeCandidates with canceled caller context = %v, want context.Canceled", err)
	}
}
