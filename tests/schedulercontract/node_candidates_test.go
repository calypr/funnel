package schedulercontract

import (
	"testing"

	"github.com/calypr/funnel/compute/scheduler"
	"github.com/calypr/funnel/tes"
)

func runNodeCandidateContracts(t *testing.T, open storeFactory) {
	t.Run("candidate pages omit assignments and retain current budgets", func(t *testing.T) {
		db := open(t)
		ctx := testContext()
		createNode(t, db, ctx, "candidate", 2)
		task := reserveContractTask(t, db, "assigned", "candidate")
		full := readContractNode(t, db, "candidate")
		page, err := db.ListNodeCandidates(ctx, &scheduler.ListNodesRequest{PageSize: 1})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Nodes) != 1 {
			t.Fatalf("candidate page=%v", page)
		}
		node := page.Nodes[0]
		if node.Id != full.Id || node.Version != full.Version || node.Available.Cpus != 1 || len(node.TaskIds) != 0 {
			t.Fatalf("candidate projection=%v; full=%v", node, full)
		}
		public, err := db.ListNodes(ctx, &scheduler.ListNodesRequest{PageSize: 1})
		if err != nil {
			t.Fatal(err)
		}
		if len(public.Nodes) != 1 || len(public.Nodes[0].TaskIds) != 1 || public.Nodes[0].TaskIds[0] != task.Id {
			t.Fatalf("public assignments lost: %v", public)
		}
	})
	t.Run("health state remains authoritative when reporting again", func(t *testing.T) {
		db := open(t)
		ctx := testContext()
		createNode(t, db, ctx, "health", 2)
		node := readContractNode(t, db, "health")
		node.State = scheduler.NodeState_DEAD
		if _, err := db.SetNodeState(ctx, node); err != nil {
			t.Fatal(err)
		}
		node = readContractNode(t, db, "health")
		node.State = scheduler.NodeState_ALIVE
		if _, err := db.PutNode(ctx, node); err != nil {
			t.Fatal(err)
		}
		node = readContractNode(t, db, "health")
		if node.State != scheduler.NodeState_ALIVE {
			t.Fatalf("node failed to revive: %v", node)
		}
		node.State = scheduler.NodeState_GONE
		if _, err := db.SetNodeState(ctx, node); err != nil {
			t.Fatal(err)
		}
		node = readContractNode(t, db, "health")
		node.State = scheduler.NodeState_ALIVE
		if _, err := db.PutNode(ctx, node); err == nil {
			t.Fatal("gone node accepted a live report")
		}
	})

	t.Run("gone candidate hydration preserves task failure cleanup", func(t *testing.T) {
		db := open(t)
		ctx := testContext()
		createNode(t, db, ctx, "gone-candidate", 2)
		task := reserveContractTask(t, db, "gone-assignment", "gone-candidate")
		node := readContractNode(t, db, "gone-candidate")
		node.State = scheduler.NodeState_GONE
		if _, err := db.SetNodeState(ctx, node); err != nil {
			t.Fatal(err)
		}
		if err := newScheduler(db, db, db, db).Schedule(ctx); err != nil {
			t.Fatal(err)
		}
		if state := getTask(t, db, ctx, task.Id).State; state != tes.SystemError {
			t.Fatalf("gone task state=%s; want SYSTEM_ERROR", state)
		}
		remaining, err := scheduler.ListAllNodes(ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		if len(remaining) != 0 {
			t.Fatalf("gone node retained after successful failure reporting: %v", remaining)
		}
	})
}
