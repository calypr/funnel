package schedulercontract

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"testing"

	"github.com/calypr/funnel/compute/scheduler"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/tes"
	"google.golang.org/protobuf/proto"
)

func readContractNode(t *testing.T, db store, id string) *scheduler.Node {
	t.Helper()
	node, err := db.GetNode(testContext(), &scheduler.GetNodeRequest{Id: id})
	if err != nil {
		t.Fatal(err)
	}
	return node
}
func reserveContractTask(t *testing.T, db store, id, nodeID string) *tes.Task {
	t.Helper()
	task := createTask(t, db, testContext(), id)
	owner, err := db.TryAssign(testContext(), task, readContractNode(t, db, nodeID))
	if err != nil || owner != nodeID {
		t.Fatalf("reserve %s = (%q,%v)", id, owner, err)
	}
	return task
}
func completeContractTask(t *testing.T, db store, id string) {
	t.Helper()
	if err := db.WriteEvent(testContext(), events.NewState(id, tes.Complete)); err != nil {
		t.Fatal(err)
	}
}

func runReservationContracts(t *testing.T, open storeFactory) {
	t.Run("heartbeats cannot forge or erase allocations", func(t *testing.T) {
		db := open(t)
		ctx := testContext()
		createNode(t, db, ctx, "node", 2)
		stale := readContractNode(t, db, "node")
		reserveContractTask(t, db, "owned", "node")
		createTask(t, db, ctx, "unclaimed")
		before := readContractNode(t, db, "node")
		stale.TaskIds = []string{"unclaimed"}
		stale.Available = &scheduler.Resources{Cpus: 99, RamGb: 99, DiskGb: 99}
		if _, err := db.PutNode(ctx, stale); err != nil {
			t.Fatal(err)
		}
		after := readContractNode(t, db, "node")
		if after.Version != before.Version || !proto.Equal(after.Available, before.Available) || len(after.TaskIds) != 1 || after.TaskIds[0] != "owned" {
			t.Fatalf("heartbeat changed allocation: %v", after)
		}
		if after.LastPing <= before.LastPing {
			t.Fatal("heartbeat did not refresh liveness")
		}
		if owner, err := db.TryAssign(ctx, &tes.Task{Id: "unclaimed"}, nil); err != nil || owner != "" {
			t.Fatalf("heartbeat forged claim: %q %v", owner, err)
		}
		stale.Resources = &scheduler.Resources{Cpus: 99, RamGb: 99, DiskGb: 99}
		stale.Version = before.Version - 1
		if _, err := db.PutNode(ctx, stale); err == nil {
			t.Fatal("stale configuration update succeeded")
		}
	})
	t.Run("fresh heartbeat fences stale health decision", func(t *testing.T) {
		db := open(t)
		ctx := testContext()
		createNode(t, db, ctx, "node", 2)
		observed := readContractNode(t, db, "node")
		heartbeat := proto.Clone(observed).(*scheduler.Node)
		if _, err := db.PutNode(ctx, heartbeat); err != nil {
			t.Fatal(err)
		}
		observed.State = scheduler.NodeState_DEAD
		health := db
		if _, err := health.SetNodeState(ctx, observed); !errors.Is(err, scheduler.ErrNodeChanged) {
			t.Fatalf("stale health decision = %v", err)
		}
		current := readContractNode(t, db, "node")
		if current.State != scheduler.NodeState_ALIVE || current.Version != heartbeat.Version {
			t.Fatalf("stale observation changed node: %v", current)
		}
		current.State = scheduler.NodeState_DRAIN
		drained, err := health.SetNodeState(ctx, current)
		if err != nil {
			t.Fatal(err)
		}
		task := createTask(t, db, ctx, "after-drain")
		if owner, err := db.TryAssign(ctx, task, heartbeat); err == nil || owner != "" {
			t.Fatalf("stale ALIVE snapshot bypassed drain: %q %v", owner, err)
		}
		if drained.LastPing != heartbeat.LastPing {
			t.Fatal("health change refreshed liveness")
		}
	})
	t.Run("capacity shrink and repeated completion preserve usage", func(t *testing.T) {
		db := open(t)
		ctx := testContext()
		createNode(t, db, ctx, "node", 2)
		reserveContractTask(t, db, "a", "node")
		reserveContractTask(t, db, "b", "node")
		node := readContractNode(t, db, "node")
		node.Resources = &scheduler.Resources{Cpus: 1, RamGb: 1, DiskGb: 1}
		if _, err := db.PutNode(ctx, node); err != nil {
			t.Fatal(err)
		}
		completeContractTask(t, db, "a")
		completeContractTask(t, db, "a")
		node = readContractNode(t, db, "node")
		if node.Available.Cpus != 0 || node.Available.RamGb != 0 || node.Available.DiskGb != 0 || len(node.TaskIds) != 1 || node.TaskIds[0] != "b" {
			t.Fatalf("completion over-credited shrunk node: %v", node)
		}
		task := createTask(t, db, ctx, "blocked")
		if owner, err := db.TryAssign(ctx, task, node); err == nil || owner != "" {
			t.Fatalf("overcommitted reduced capacity: %q %v", owner, err)
		}
		completeContractTask(t, db, "b")
		node = readContractNode(t, db, "node")
		if !proto.Equal(node.Available, node.Resources) || len(node.TaskIds) != 0 {
			t.Fatalf("usage was not fully released: %v", node)
		}
		if owner, err := db.TryAssign(ctx, task, node); err != nil || owner != "node" {
			t.Fatalf("released capacity unusable: %q %v", owner, err)
		}
	})
	for _, terminal := range []tes.State{tes.Complete, tes.Canceled} {
		t.Run("concurrent "+terminal.String()+" writes release once", func(t *testing.T) {
			db := open(t)
			ctx := testContext()
			createNode(t, db, ctx, "node", 2)
			reserveContractTask(t, db, "a", "node")
			reserveContractTask(t, db, "b", "node")
			before := readContractNode(t, db, "node")
			start := make(chan struct{})
			errs := make(chan error, 4)
			var done sync.WaitGroup
			for i := 0; i < 4; i++ {
				done.Add(1)
				go func() { defer done.Done(); <-start; errs <- db.WriteEvent(ctx, events.NewState("a", terminal)) }()
			}
			close(start)
			done.Wait()
			close(errs)
			for err := range errs {
				if err != nil {
					t.Fatal(err)
				}
			}
			node := readContractNode(t, db, "node")
			if node.Available.Cpus != 1 || node.Available.RamGb != 1 || node.Available.DiskGb != 1 || len(node.TaskIds) != 1 || node.TaskIds[0] != "b" {
				t.Fatalf("duplicate release changed capacity: %v", node)
			}
			if node.LastPing != before.LastPing {
				t.Fatal("release refreshed liveness")
			}
			if err := db.WriteEvent(ctx, events.NewState("a", tes.Queued)); err == nil {
				t.Fatal("completed allocation was reactivated")
			}
		})
	}
	t.Run("missing owner failure retires allocation", func(t *testing.T) {
		db := open(t)
		ctx := testContext()
		createNode(t, db, ctx, "gone", 2)
		createNode(t, db, ctx, "other", 2)
		task := reserveContractTask(t, db, "orphaned", "gone")
		if _, err := db.DeleteNode(ctx, &scheduler.Node{Id: "gone"}); err != nil {
			t.Fatal(err)
		}
		if err := newScheduler(db, db, db, db).Schedule(ctx); err != nil {
			t.Fatal(err)
		}
		if state := getTask(t, db, ctx, task.Id).State; state != tes.SystemError {
			t.Fatalf("state = %s", state)
		}
		if node := readContractNode(t, db, "other"); len(node.TaskIds) != 0 {
			t.Fatal("orphan was reassigned")
		}
		// Retirement allows the ID to register again, without resurrecting old work.
		createNode(t, db, ctx, "gone", 2)
		if node := readContractNode(t, db, "gone"); len(node.TaskIds) != 0 || node.Available.Cpus != 2 {
			t.Fatalf("retired allocation resurrected: %v", node)
		}
	})
	t.Run("one complete node snapshot serves an entire batch", func(t *testing.T) {
		db := open(t)
		ctx := testContext()
		for _, id := range []string{"a", "b", "c", "z"} {
			state := scheduler.NodeState_DRAIN
			if id == "z" {
				state = scheduler.NodeState_ALIVE
			}
			if _, err := db.PutNode(ctx, &scheduler.Node{Id: id, State: state, Resources: &scheduler.Resources{Cpus: 3, RamGb: 3, DiskGb: 3}}); err != nil {
				t.Fatal(err)
			}
		}
		for i := 0; i < 3; i++ {
			createTask(t, db, ctx, fmt.Sprintf("batch-%d", i))
		}
		pages := &singleNodePages{SchedulerServiceServer: db, health: db}
		if err := newScheduler(pages, db, db, db).Schedule(ctx); err != nil {
			t.Fatal(err)
		}
		if pages.calls != 4 {
			t.Fatalf("node pages fetched = %d, want one four-page snapshot", pages.calls)
		}
		node := readContractNode(t, db, "z")
		if len(node.TaskIds) != 3 || node.Available.Cpus != 0 {
			t.Fatalf("batch lost reservations or reused capacity: %v", node)
		}
		for i := 0; i < 3; i++ {
			if state := getTask(t, db, ctx, fmt.Sprintf("batch-%d", i)).State; state != tes.Initializing {
				t.Fatalf("task not scheduled: %s", state)
			}
		}
	})
	t.Run("fractional reservations release without capacity drift", func(t *testing.T) {
		db := open(t)
		ctx := testContext()
		createNode(t, db, ctx, "node", 4)
		for i, size := range []float64{0.1, 0.2, 0.3} {
			task := testTask(fmt.Sprintf("fraction-%d", i))
			task.Resources.RamGb = size
			task.Resources.DiskGb = size
			if err := db.WriteEvent(ctx, events.NewTaskCreated(task)); err != nil {
				t.Fatal(err)
			}
			if _, err := db.TryAssign(ctx, task, readContractNode(t, db, "node")); err != nil {
				t.Fatal(err)
			}
		}
		for _, i := range []int{1, 0, 2} {
			completeContractTask(t, db, fmt.Sprintf("fraction-%d", i))
		}
		node := readContractNode(t, db, "node")
		if node.Available.Cpus != 4 || math.Abs(node.Available.RamGb-2) > 1e-12 || math.Abs(node.Available.DiskGb-2) > 1e-12 {
			t.Fatalf("resource accounting drift: %v", node.Available)
		}
	})
}

type singleNodePages struct {
	scheduler.SchedulerServiceServer
	health scheduler.NodeHealthStore
	calls  int
}

func (p *singleNodePages) ListNodes(ctx context.Context, req *scheduler.ListNodesRequest) (*scheduler.ListNodesResponse, error) {
	p.calls++
	request := proto.Clone(req).(*scheduler.ListNodesRequest)
	request.PageSize = 1
	return p.SchedulerServiceServer.ListNodes(ctx, request)
}
func (p *singleNodePages) SetNodeState(ctx context.Context, node *scheduler.Node) (*scheduler.Node, error) {
	return p.health.SetNodeState(ctx, node)
}
