package postgres

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/calypr/funnel/compute/scheduler"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/tes"
	"github.com/testcontainers/testcontainers-go"
	"google.golang.org/protobuf/proto"
)

func newTryAssignDB(t *testing.T) (*Postgres, context.Context) {
	t.Helper()
	db, container := getTestPostgres(t)
	t.Cleanup(func() {
		db.Close()
		if err := testcontainers.TerminateContainer(container); err != nil {
			t.Logf("terminating PostgreSQL container: %v", err)
		}
	})
	return db, getUserContext()
}

func createTryAssignNode(t *testing.T, db *Postgres, ctx context.Context, id string) *scheduler.Node {
	t.Helper()
	_, err := db.PutNode(ctx, &scheduler.Node{
		Id:        id,
		State:     scheduler.NodeState_ALIVE,
		Resources: &scheduler.Resources{Cpus: 4, RamGb: 8, DiskGb: 10},
	})
	if err != nil {
		t.Fatalf("create node %q: %v", id, err)
	}
	node, err := db.GetNode(ctx, &scheduler.GetNodeRequest{Id: id})
	if err != nil {
		t.Fatalf("get node %q: %v", id, err)
	}
	return node
}

func taskClaimOwner(t *testing.T, db *Postgres, ctx context.Context, taskID string) string {
	t.Helper()
	var owner string
	if err := db.client.QueryRow(ctx, `SELECT COALESCE(claim_owner, '') FROM tasks WHERE id = $1`, taskID).Scan(&owner); err != nil {
		t.Fatalf("read task %q claim: %v", taskID, err)
	}
	return owner
}

func TestTryAssignReservesNodeAndLeavesStateToEventWriter(t *testing.T) {
	db, ctx := newTryAssignDB(t)
	const taskID = "atomic-task"
	const nodeID = "atomic-node"
	task := newTestTask(taskID, taskID, testUser)
	if err := db.WriteEvent(ctx, events.NewTaskCreated(task)); err != nil {
		t.Fatalf("create task: %v", err)
	}
	node := createTryAssignNode(t, db, ctx, nodeID)
	// Queue snapshots can be stale or incomplete; the database row remains the
	// source of task resources during the reservation.
	task.Resources.CpuCores = 99

	owner, err := db.TryAssign(ctx, task, node)
	if err != nil {
		t.Fatalf("TryAssign: %v", err)
	}
	if owner != nodeID {
		t.Fatalf("TryAssign owner = %q, want %q", owner, nodeID)
	}
	if got := taskClaimOwner(t, db, ctx, taskID); got != nodeID {
		t.Fatalf("claim owner = %q, want %q", got, nodeID)
	}

	storedTask, err := db.GetTask(ctx, &tes.GetTaskRequest{Id: taskID})
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if storedTask.State != tes.State_QUEUED {
		t.Fatalf("TryAssign changed task state to %s, want QUEUED", storedTask.State)
	}

	storedNode, err := db.GetNode(ctx, &scheduler.GetNodeRequest{Id: nodeID})
	if err != nil {
		t.Fatalf("get reserved node: %v", err)
	}
	var heartbeat time.Time
	if err := db.client.QueryRow(ctx, "SELECT last_heartbeat FROM nodes WHERE id = $1", nodeID).Scan(&heartbeat); err != nil {
		t.Fatalf("read node heartbeat: %v", err)
	}
	if heartbeat.UnixMicro() != time.Unix(0, storedNode.LastPing).UnixMicro() {
		t.Fatalf("node heartbeat %v does not match LastPing %d", heartbeat, storedNode.LastPing)
	}
	if len(storedNode.TaskIds) != 1 || storedNode.TaskIds[0] != taskID {
		t.Fatalf("node task IDs = %v, want [%s]", storedNode.TaskIds, taskID)
	}
	if storedNode.Available.Cpus != 3 || storedNode.Available.RamGb != 7 || storedNode.Available.DiskGb != 9 {
		t.Fatalf("node available resources = %v, want 3 CPU, 7 GB RAM, 9 GB disk", storedNode.Available)
	}

	// A repeated call returns the durable owner without appending or charging
	// resources again, even when its candidate snapshot is stale.
	owner, err = db.TryAssign(ctx, task, node)
	if err != nil {
		t.Fatalf("repeat TryAssign: %v", err)
	}
	if owner != nodeID {
		t.Fatalf("repeat TryAssign owner = %q, want %q", owner, nodeID)
	}
	storedNode, err = db.GetNode(ctx, &scheduler.GetNodeRequest{Id: nodeID})
	if err != nil {
		t.Fatalf("get node after repeat: %v", err)
	}
	if len(storedNode.TaskIds) != 1 || storedNode.Available.Cpus != 3 || storedNode.Available.RamGb != 7 || storedNode.Available.DiskGb != 9 {
		t.Fatalf("repeat TryAssign changed reservation: IDs=%v available=%v", storedNode.TaskIds, storedNode.Available)
	}

	if err := db.WriteEvent(ctx, events.NewState(taskID, tes.State_INITIALIZING)); err != nil {
		t.Fatalf("event writer state transition: %v", err)
	}
	storedTask, err = db.GetTask(ctx, &tes.GetTaskRequest{Id: taskID})
	if err != nil {
		t.Fatalf("get task after event: %v", err)
	}
	if storedTask.State != tes.State_INITIALIZING {
		t.Fatalf("event writer state = %s, want INITIALIZING", storedTask.State)
	}
}

func TestTryAssignChecksCurrentEligibilityAndRollsBackClaim(t *testing.T) {
	db, ctx := newTryAssignDB(t)
	const nodeID = "versioned-node"
	staleNode := createTryAssignNode(t, db, ctx, nodeID)

	if err := db.WriteEvent(ctx, events.NewTaskCreated(newTestTask("stale-task", "stale-task", testUser))); err != nil {
		t.Fatalf("create stale task: %v", err)
	}
	if _, err := db.TryAssign(ctx, newTestTask("stale-task", "stale-task", testUser), staleNode); err != nil {
		t.Fatalf("initial assignment: %v", err)
	}

	if err := db.WriteEvent(ctx, events.NewTaskCreated(newTestTask("conflict-task", "conflict-task", testUser))); err != nil {
		t.Fatalf("create conflict task: %v", err)
	}
	owner, err := db.TryAssign(ctx, newTestTask("conflict-task", "conflict-task", testUser), nil)
	if err != nil || owner != "" {
		t.Fatalf("TryAssign without candidate = (%q, %v), want empty owner and no error", owner, err)
	}
	if owner := taskClaimOwner(t, db, ctx, "conflict-task"); owner != "" {
		t.Fatalf("lookup without candidate created claim owner %q", owner)
	}
	currentNode, err := db.GetNode(ctx, &scheduler.GetNodeRequest{Id: nodeID})
	if err != nil {
		t.Fatalf("get node before reducing capacity: %v", err)
	}
	staleEligibleSnapshot := proto.Clone(currentNode).(*scheduler.Node)
	currentNode.Resources = &scheduler.Resources{Cpus: 1, RamGb: 8, DiskGb: 10}
	if _, err := db.PutNode(ctx, currentNode); err != nil {
		t.Fatalf("reduce current node capacity: %v", err)
	}
	if _, err := db.TryAssign(ctx, newTestTask("conflict-task", "conflict-task", testUser), staleEligibleSnapshot); err == nil {
		t.Fatal("TryAssign with stale snapshot of eligible node succeeded after current capacity was exhausted")
	}
	if owner := taskClaimOwner(t, db, ctx, "conflict-task"); owner != "" {
		t.Fatalf("ineligible-node conflict left claim owner %q, want empty", owner)
	}

	if err := db.WriteEvent(ctx, events.NewTaskCreated(newTestTask("rollback-task", "rollback-task", testUser))); err != nil {
		t.Fatalf("create rollback task: %v", err)
	}
	currentNode, err = db.GetNode(ctx, &scheduler.GetNodeRequest{Id: nodeID})
	if err != nil {
		t.Fatalf("get current node: %v", err)
	}
	currentNode.Resources = &scheduler.Resources{Cpus: 4, RamGb: 8, DiskGb: 10}
	if _, err := db.PutNode(ctx, currentNode); err != nil {
		t.Fatalf("restore node capacity for rollback check: %v", err)
	}
	if currentNode.Available.Cpus != 3 {
		t.Fatalf("restored node available CPUs = %d; want 3", currentNode.Available.Cpus)
	}
	_, err = db.client.Exec(ctx, `
		CREATE FUNCTION reject_try_assign_node_update() RETURNS trigger AS $$
		BEGIN
			RAISE EXCEPTION 'forced reservation failure';
		END;
		$$ LANGUAGE plpgsql;
	`)
	if err != nil {
		t.Fatalf("install reservation failure function: %v", err)
	}
	_, err = db.client.Exec(ctx, `
		CREATE TRIGGER reject_try_assign_node_update
		BEFORE UPDATE ON nodes
		FOR EACH ROW
		WHEN (NEW.id = 'versioned-node')
		EXECUTE FUNCTION reject_try_assign_node_update();
	`)
	if err != nil {
		t.Fatalf("install reservation failure trigger: %v", err)
	}
	if _, err := db.TryAssign(ctx, newTestTask("rollback-task", "rollback-task", testUser), currentNode); err == nil || !strings.Contains(err.Error(), "forced reservation failure") {
		t.Fatalf("TryAssign error = %v; want forced reservation failure", err)
	}
	if owner := taskClaimOwner(t, db, ctx, "rollback-task"); owner != "" {
		t.Fatalf("failed node reservation left claim owner %q, want empty", owner)
	}
	afterRollback, err := db.GetNode(ctx, &scheduler.GetNodeRequest{Id: nodeID})
	if err != nil {
		t.Fatalf("get node after rollback: %v", err)
	}
	if len(afterRollback.TaskIds) != 1 || afterRollback.TaskIds[0] != "stale-task" || afterRollback.Available.Cpus != 3 {
		t.Fatalf("failed reservation changed node: IDs=%v available=%v; want unchanged assignment with 3 CPUs", afterRollback.TaskIds, afterRollback.Available)
	}

	if err := db.WriteEvent(ctx, events.NewTaskCreated(newTestTask("canceled-task", "canceled-task", testUser))); err != nil {
		t.Fatalf("create canceled task: %v", err)
	}
	if err := db.WriteEvent(ctx, events.NewState("canceled-task", tes.State_CANCELED)); err != nil {
		t.Fatalf("cancel task: %v", err)
	}
	currentNode, err = db.GetNode(ctx, &scheduler.GetNodeRequest{Id: nodeID})
	if err != nil {
		t.Fatalf("get current node: %v", err)
	}
	owner, err = db.TryAssign(ctx, newTestTask("canceled-task", "canceled-task", testUser), currentNode)
	if err != nil {
		t.Fatalf("TryAssign canceled task: %v", err)
	}
	if owner != "" {
		t.Fatalf("TryAssign canceled task owner = %q, want empty", owner)
	}
	if owner := taskClaimOwner(t, db, ctx, "canceled-task"); owner != "" {
		t.Fatalf("canceled task has claim owner %q, want empty", owner)
	}
}

func TestConcurrentTryAssignReservesTaskOnOnlyOneNode(t *testing.T) {
	db, ctx := newTryAssignDB(t)
	const taskID = "concurrent-atomic-task"
	if err := db.WriteEvent(ctx, events.NewTaskCreated(newTestTask(taskID, taskID, testUser))); err != nil {
		t.Fatalf("create task: %v", err)
	}
	nodes := []*scheduler.Node{
		createTryAssignNode(t, db, ctx, "concurrent-node-a"),
		createTryAssignNode(t, db, ctx, "concurrent-node-b"),
	}

	type result struct {
		owner string
		err   error
	}
	results := make(chan result, len(nodes))
	var wg sync.WaitGroup
	for _, node := range nodes {
		wg.Add(1)
		go func(candidate *scheduler.Node) {
			defer wg.Done()
			owner, err := db.TryAssign(ctx, newTestTask(taskID, taskID, testUser), candidate)
			results <- result{owner: owner, err: err}
		}(node)
	}
	wg.Wait()
	close(results)

	winner := ""
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent TryAssign: %v", result.err)
		}
		if result.owner == "" {
			t.Fatal("concurrent TryAssign returned an empty owner")
		}
		if winner == "" {
			winner = result.owner
		} else if result.owner != winner {
			t.Fatalf("concurrent TryAssign owners differ: %q and %q", winner, result.owner)
		}
	}

	assignedNodes := 0
	for _, candidate := range nodes {
		stored, err := db.GetNode(ctx, &scheduler.GetNodeRequest{Id: candidate.Id})
		if err != nil {
			t.Fatalf("get node %q: %v", candidate.Id, err)
		}
		for _, id := range stored.TaskIds {
			if id == taskID {
				assignedNodes++
			}
		}
	}
	if assignedNodes != 1 {
		t.Fatalf("task reserved on %d nodes, want exactly one", assignedNodes)
	}
	if owner := taskClaimOwner(t, db, ctx, taskID); owner != winner {
		t.Fatalf("claim owner = %q, concurrent winner = %q", owner, winner)
	}
}

func TestTryAssignRepairsSingleLegacyAssignmentAndRejectsAmbiguousOnes(t *testing.T) {
	db, ctx := newTryAssignDB(t)
	const legacyTaskID = "legacy-assignment-task"
	if err := db.WriteEvent(ctx, events.NewTaskCreated(newTestTask(legacyTaskID, legacyTaskID, testUser))); err != nil {
		t.Fatalf("create legacy task: %v", err)
	}
	legacyNode := createTryAssignNode(t, db, ctx, "legacy-assignment-node")
	legacyNode.TaskIds = []string{legacyTaskID}
	if _, err := db.PutNode(ctx, legacyNode); err != nil {
		t.Fatalf("write legacy node assignment: %v", err)
	}
	legacyNode, err := db.GetNode(ctx, &scheduler.GetNodeRequest{Id: legacyNode.Id})
	if err != nil {
		t.Fatalf("get legacy node: %v", err)
	}

	owner, err := db.TryAssign(ctx, newTestTask(legacyTaskID, legacyTaskID, testUser), nil)
	if err != nil || owner != legacyNode.Id {
		t.Fatalf("read legacy assignment = (%q, %v), want owner %q", owner, err, legacyNode.Id)
	}
	if got := taskClaimOwner(t, db, ctx, legacyTaskID); got != "" {
		t.Fatalf("read-only legacy lookup created claim owner %q", got)
	}

	owner, err = db.TryAssign(ctx, newTestTask(legacyTaskID, legacyTaskID, testUser), legacyNode)
	if err != nil || owner != legacyNode.Id {
		t.Fatalf("repair legacy assignment = (%q, %v), want owner %q", owner, err, legacyNode.Id)
	}
	if got := taskClaimOwner(t, db, ctx, legacyTaskID); got != legacyNode.Id {
		t.Fatalf("repaired claim owner = %q, want %q", got, legacyNode.Id)
	}
	storedLegacyNode, err := db.GetNode(ctx, &scheduler.GetNodeRequest{Id: legacyNode.Id})
	if err != nil {
		t.Fatalf("get repaired legacy node: %v", err)
	}
	if len(storedLegacyNode.TaskIds) != 1 || storedLegacyNode.Available.Cpus != 3 {
		t.Fatalf("legacy repair duplicated or recharged task: IDs=%v available=%v", storedLegacyNode.TaskIds, storedLegacyNode.Available)
	}

	const ambiguousTaskID = "ambiguous-assignment-task"
	if err := db.WriteEvent(ctx, events.NewTaskCreated(newTestTask(ambiguousTaskID, ambiguousTaskID, testUser))); err != nil {
		t.Fatalf("create ambiguous task: %v", err)
	}
	nodeA := createTryAssignNode(t, db, ctx, "ambiguous-node-a")
	nodeB := createTryAssignNode(t, db, ctx, "ambiguous-node-b")
	for _, node := range []*scheduler.Node{nodeA, nodeB} {
		node.TaskIds = []string{ambiguousTaskID}
		if _, err := db.PutNode(ctx, node); err != nil {
			t.Fatalf("write ambiguous assignment to %q: %v", node.Id, err)
		}
	}
	nodeA, err = db.GetNode(ctx, &scheduler.GetNodeRequest{Id: nodeA.Id})
	if err != nil {
		t.Fatalf("get ambiguous node: %v", err)
	}
	if _, err := db.TryAssign(ctx, newTestTask(ambiguousTaskID, ambiguousTaskID, testUser), nodeA); err == nil {
		t.Fatal("TryAssign accepted task already stored on multiple nodes")
	}
	if got := taskClaimOwner(t, db, ctx, ambiguousTaskID); got != "" {
		t.Fatalf("ambiguous assignment created claim owner %q", got)
	}
}

func TestTryAssignRejectsOrphanClaimAndReturnsMissingOwner(t *testing.T) {
	db, ctx := newTryAssignDB(t)
	const taskID = "claimed-without-reservation"
	if err := db.WriteEvent(ctx, events.NewTaskCreated(newTestTask(taskID, taskID, testUser))); err != nil {
		t.Fatalf("create task: %v", err)
	}
	ownerNode := createTryAssignNode(t, db, ctx, "durable-owner-node")
	candidate := createTryAssignNode(t, db, ctx, "different-candidate-node")
	if _, err := db.client.Exec(ctx, `UPDATE tasks SET claim_owner = $1 WHERE id = $2`, ownerNode.Id, taskID); err != nil {
		t.Fatalf("seed durable claim: %v", err)
	}
	if _, err := db.TryAssign(ctx, newTestTask(taskID, taskID, testUser), nil); err == nil {
		t.Fatal("TryAssign returned an owner with no reservation on an existing node; want a clear error")
	}
	if _, err := db.TryAssign(ctx, newTestTask(taskID, taskID, testUser), candidate); err == nil {
		t.Fatal("TryAssign silently repaired an orphan claim; want a clear error")
	}
	unchangedOwner, err := db.GetNode(ctx, &scheduler.GetNodeRequest{Id: ownerNode.Id})
	if err != nil {
		t.Fatalf("get owner after orphan claim: %v", err)
	}
	if len(unchangedOwner.TaskIds) != 0 {
		t.Fatalf("orphan claim changed owner TaskIds: %v", unchangedOwner.TaskIds)
	}
	storedCandidate, err := db.GetNode(ctx, &scheduler.GetNodeRequest{Id: candidate.Id})
	if err != nil {
		t.Fatalf("get candidate node: %v", err)
	}
	if len(storedCandidate.TaskIds) != 0 || storedCandidate.Available.Cpus != 4 {
		t.Fatalf("candidate changed despite orphan claim: IDs=%v available=%v", storedCandidate.TaskIds, storedCandidate.Available)
	}

	const missingNodeTaskID = "claim-for-missing-node"
	if err := db.WriteEvent(ctx, events.NewTaskCreated(newTestTask(missingNodeTaskID, missingNodeTaskID, testUser))); err != nil {
		t.Fatalf("create missing-node task: %v", err)
	}
	const missingNodeID = "missing-durable-owner"
	if _, err := db.client.Exec(ctx, `UPDATE tasks SET claim_owner = $1 WHERE id = $2`, missingNodeID, missingNodeTaskID); err != nil {
		t.Fatalf("seed missing-node claim: %v", err)
	}
	owner, err := db.TryAssign(ctx, newTestTask(missingNodeTaskID, missingNodeTaskID, testUser), nil)
	if err != nil || owner != missingNodeID {
		t.Fatalf("TryAssign missing claimed node = (%q, %v), want owner %q", owner, err, missingNodeID)
	}
}
