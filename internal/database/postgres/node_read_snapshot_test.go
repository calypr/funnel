package postgres

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/calypr/funnel/api/tes"
	"github.com/calypr/funnel/internal/compute/scheduler"
	"github.com/calypr/funnel/internal/events"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"
)

type nodeSnapshotReadBarrierKey struct{}

type nodeSnapshotReadBarrier struct {
	once   sync.Once
	read   chan struct{}
	resume chan struct{}
}

func (b *nodeSnapshotReadBarrier) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	matched := strings.Contains(data.SQL, "n.reserved") && strings.Contains(data.SQL, "FROM nodes n")
	return context.WithValue(ctx, nodeSnapshotReadBarrierKey{}, matched)
}

func (b *nodeSnapshotReadBarrier) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	matched, _ := ctx.Value(nodeSnapshotReadBarrierKey{}).(bool)
	if !matched || data.Err != nil {
		return
	}
	b.once.Do(func() {
		close(b.read)
		select {
		case <-b.resume:
		case <-ctx.Done():
		}
	})
}

func TestNodeReadReservationSnapshot(t *testing.T) {
	db, ctx := newTryAssignDB(t)
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
				task := newTestTask(taskID, taskID, testUser)
				if err := db.WriteEvent(ctx, events.NewTaskCreated(task)); err != nil {
					t.Fatalf("create task: %v", err)
				}
				candidate := createTryAssignNode(t, db, ctx, nodeID)
				if release {
					if owner, err := db.TryAssign(ctx, task, candidate); err != nil || owner != nodeID {
						t.Fatalf("assign before completion: owner=%q err=%v", owner, err)
					}
				}
				before, err := readNodeSnapshotView(ctx, db, list, nodeID)
				if err != nil {
					t.Fatalf("read before snapshot: %v", err)
				}

				barrier := &nodeSnapshotReadBarrier{read: make(chan struct{}), resume: make(chan struct{})}
				var resumeOnce sync.Once
				resume := func() { resumeOnce.Do(func() { close(barrier.resume) }) }
				t.Cleanup(resume)
				config := db.client.Config()
				config.ConnConfig.Tracer = barrier
				pool, err := pgxpool.NewWithConfig(ctx, config)
				if err != nil {
					t.Fatal(err)
				}
				oldPool := db.client
				db.client = pool
				oldPool.Close()

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
				case <-time.After(10 * time.Second):
					t.Fatal("node read did not reach controlled query boundary")
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

				got := <-result
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

func readNodeSnapshotView(ctx context.Context, db *Postgres, list bool, nodeID string) (*scheduler.Node, error) {
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

type nodeReadQueryCounts struct {
	nodeQueries atomic.Int64
	taskQueries atomic.Int64
}

func (c *nodeReadQueryCounts) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	sql := strings.ToLower(strings.Join(strings.Fields(data.SQL), " "))
	if strings.Contains(sql, "from nodes n") {
		c.nodeQueries.Add(1)
	}
	if strings.Contains(sql, "from tasks where claim_active") {
		c.taskQueries.Add(1)
	}
	return ctx
}

func (*nodeReadQueryCounts) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func (c *nodeReadQueryCounts) reset() {
	c.nodeQueries.Store(0)
	c.taskQueries.Store(0)
}

func TestNodeReadQueryBudgetAndCallerCancellation(t *testing.T) {
	db, ctx := newTryAssignDB(t)
	for _, suffix := range []string{"a", "b"} {
		task := newTestTask("budget-task-"+suffix, "budget-task-"+suffix, testUser)
		if err := db.WriteEvent(ctx, events.NewTaskCreated(task)); err != nil {
			t.Fatalf("create task: %v", err)
		}
		candidate := createTryAssignNode(t, db, ctx, "budget-node-"+suffix)
		if owner, err := db.TryAssign(ctx, task, candidate); err != nil || owner != candidate.Id {
			t.Fatalf("assign task: owner=%q err=%v", owner, err)
		}
	}

	counts := &nodeReadQueryCounts{}
	config := db.client.Config()
	config.ConnConfig.Tracer = counts
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	oldPool := db.client
	db.client = pool
	oldPool.Close()

	counts.reset()
	assigned, err := db.GetNode(ctx, &scheduler.GetNodeRequest{Id: "budget-node-a"})
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if !slices.Equal(assigned.TaskIds, []string{"budget-task-a"}) {
		t.Fatalf("GetNode task IDs = %v, want [budget-task-a]", assigned.TaskIds)
	}
	if got := counts.nodeQueries.Load(); got != 1 {
		t.Fatalf("GetNode node projection query count = %d, want 1", got)
	}
	if got := counts.taskQueries.Load(); got > 1 {
		t.Fatalf("GetNode active-task query count = %d, want at most 1", got)
	}

	counts.reset()
	listed, err := db.ListNodes(ctx, &scheduler.ListNodesRequest{})
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}
	if len(listed.Nodes) != 2 || counts.nodeQueries.Load() != 1 || counts.taskQueries.Load() > 1 {
		t.Fatalf("ListNodes returned %d nodes with %d node and %d task queries; want 2, 1, at most 1", len(listed.Nodes), counts.nodeQueries.Load(), counts.taskQueries.Load())
	}

	counts.reset()
	candidates, err := db.ListNodeCandidates(ctx, &scheduler.ListNodesRequest{})
	if err != nil {
		t.Fatalf("ListNodeCandidates: %v", err)
	}
	if len(candidates.Nodes) != 2 || counts.nodeQueries.Load() != 1 || counts.taskQueries.Load() != 0 {
		t.Fatalf("ListNodeCandidates returned %d nodes with %d node and %d task queries; want 2, 1, 0", len(candidates.Nodes), counts.nodeQueries.Load(), counts.taskQueries.Load())
	}
	for _, node := range candidates.Nodes {
		if len(node.TaskIds) != 0 {
			t.Fatalf("candidate node %q unexpectedly includes task IDs: %v", node.Id, node.TaskIds)
		}
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
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
