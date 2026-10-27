package boltdb

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/calypr/funnel/compute/scheduler"
	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/tes"
	bolt "go.etcd.io/bbolt"
)

func openTryAssignDB(t *testing.T) *BoltDB {
	t.Helper()
	db, err := NewBoltDB(&config.BoltDB{Path: filepath.Join(t.TempDir(), "tasks.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Init(); err != nil {
		db.Close()
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	return db
}

func createTryAssignTask(t *testing.T, db *BoltDB, id string) *tes.Task {
	t.Helper()
	task := &tes.Task{
		Id: id,
		Resources: &tes.Resources{
			CpuCores: 1,
			RamGb:    1,
			DiskGb:   1,
		},
	}
	if err := db.WriteEvent(context.Background(), events.NewTaskCreated(task)); err != nil {
		t.Fatal(err)
	}
	return task
}

func createTryAssignNode(t *testing.T, db *BoltDB, id string) *scheduler.Node {
	t.Helper()
	node := &scheduler.Node{
		Id:    id,
		State: scheduler.NodeState_ALIVE,
		Resources: &scheduler.Resources{
			Cpus:   4,
			RamGb:  8,
			DiskGb: 20,
		},
	}
	if _, err := db.PutNode(context.Background(), node); err != nil {
		t.Fatal(err)
	}
	current, err := db.GetNode(context.Background(), &scheduler.GetNodeRequest{Id: id})
	if err != nil {
		t.Fatal(err)
	}
	return current
}

func TestTryAssignStoresClaimAndNodeAssignmentButLeavesTaskQueued(t *testing.T) {
	db := openTryAssignDB(t)
	task := createTryAssignTask(t, db, "task-1")
	candidate := createTryAssignNode(t, db, "node-1")

	owner, err := db.TryAssign(context.Background(), task, candidate)
	if err != nil || owner != "node-1" {
		t.Fatalf("TryAssign = %q, %v; want node-1", owner, err)
	}

	node, err := db.GetNode(context.Background(), &scheduler.GetNodeRequest{Id: owner})
	if err != nil {
		t.Fatal(err)
	}
	if len(node.TaskIds) != 1 || node.TaskIds[0] != task.Id {
		t.Fatalf("node task IDs = %v; want [%s]", node.TaskIds, task.Id)
	}
	if got := node.Available.GetCpus(); got != 3 {
		t.Fatalf("available CPUs = %d; want 3", got)
	}

	queued := db.ReadQueue(10, nil)
	if len(queued) != 1 || queued[0].Id != task.Id {
		t.Fatalf("queue after assignment = %v; want task %s to remain queued", queued, task.Id)
	}
	stored, err := db.GetTask(context.Background(), &tes.GetTaskRequest{Id: task.Id, View: tes.View_MINIMAL.String()})
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != tes.State_QUEUED {
		t.Fatalf("task state after assignment = %s; want QUEUED", stored.State)
	}

	// A retry returns the committed owner without changing the node again.
	version := node.Version
	owner, err = db.TryAssign(context.Background(), task, candidate)
	if err != nil || owner != "node-1" {
		t.Fatalf("retry TryAssign = %q, %v; want node-1", owner, err)
	}
	node, err = db.GetNode(context.Background(), &scheduler.GetNodeRequest{Id: owner})
	if err != nil {
		t.Fatal(err)
	}
	if node.Version != version || len(node.TaskIds) != 1 {
		t.Fatalf("retry changed node: version=%d task IDs=%v; want version=%d and one task", node.Version, node.TaskIds, version)
	}
}

func TestTryAssignRejectsStaleCandidateWhenNodeIsIneligible(t *testing.T) {
	db := openTryAssignDB(t)
	task := createTryAssignTask(t, db, "task-1")
	stale := createTryAssignNode(t, db, "node-1")
	other := createTryAssignNode(t, db, "node-2")

	// Exhaust node-1 after the candidate snapshot. TryAssign must use the
	// current resources rather than assigning from its stale eligibility view.
	changed := &scheduler.Node{
		Id:        stale.Id,
		Version:   stale.Version,
		State:     stale.State,
		Resources: &scheduler.Resources{Cpus: 0, RamGb: 2, DiskGb: 2},
	}
	if _, err := db.PutNode(context.Background(), changed); err != nil {
		t.Fatal(err)
	}
	if owner, err := db.TryAssign(context.Background(), task, stale); err == nil || owner != "" {
		t.Fatalf("TryAssign with stale ineligible candidate = %q, %v; want eligibility error and no owner", owner, err)
	}
	var claim string
	if err := db.db.View(func(tx *bolt.Tx) error {
		claim = string(tx.Bucket(TaskClaims).Get([]byte(task.Id)))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if claim != "" {
		t.Fatalf("version conflict persisted claim %q", claim)
	}

	owner, err := db.TryAssign(context.Background(), task, other)
	if err != nil || owner != "node-2" {
		t.Fatalf("TryAssign with another candidate = %q, %v; want node-2", owner, err)
	}
}

func TestTryAssignAdoptsLegacyNodeAssignment(t *testing.T) {
	db := openTryAssignDB(t)
	task := createTryAssignTask(t, db, "task-1")
	node := createTryAssignNode(t, db, "node-1")
	node.TaskIds = []string{task.Id}
	if _, err := db.PutNode(context.Background(), node); err != nil {
		t.Fatal(err)
	}
	if owner, err := db.TryAssign(context.Background(), task, nil); err != nil || owner != node.Id {
		t.Fatalf("read legacy assignment = %q, %v; want %s", owner, err, node.Id)
	}
	var claim string
	if err := db.db.View(func(tx *bolt.Tx) error {
		claim = string(tx.Bucket(TaskClaims).Get([]byte(task.Id)))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if claim != "" {
		t.Fatalf("read-only lookup created claim %q", claim)
	}

	owner, err := db.TryAssign(context.Background(), task, node)
	if err != nil || owner != node.Id {
		t.Fatalf("TryAssign legacy assignment = %q, %v; want %s", owner, err, node.Id)
	}
	stored, err := db.GetNode(context.Background(), &scheduler.GetNodeRequest{Id: node.Id})
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.TaskIds) != 1 {
		t.Fatalf("legacy node task IDs = %v; want exactly one task", stored.TaskIds)
	}
	if err := db.db.View(func(tx *bolt.Tx) error {
		claim = string(tx.Bucket(TaskClaims).Get([]byte(task.Id)))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if claim != node.Id {
		t.Fatalf("adopted claim = %q; want %q", claim, node.Id)
	}
}

func TestTryAssignNilCandidateOnlyLooksUpExistingOwner(t *testing.T) {
	db := openTryAssignDB(t)
	task := createTryAssignTask(t, db, "task-1")
	if owner, err := db.TryAssign(context.Background(), task, nil); err != nil || owner != "" {
		t.Fatalf("TryAssign with no candidate = %q, %v; want empty owner", owner, err)
	}
	var claim string
	if err := db.db.View(func(tx *bolt.Tx) error {
		claim = string(tx.Bucket(TaskClaims).Get([]byte(task.Id)))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if claim != "" {
		t.Fatalf("nil candidate created claim %q", claim)
	}

	candidate := createTryAssignNode(t, db, "node-1")
	if owner, err := db.TryAssign(context.Background(), task, candidate); err != nil || owner != candidate.Id {
		t.Fatalf("TryAssign = %q, %v; want %s", owner, err, candidate.Id)
	}
	if owner, err := db.TryAssign(context.Background(), task, nil); err != nil || owner != candidate.Id {
		t.Fatalf("TryAssign lookup = %q, %v; want %s", owner, err, candidate.Id)
	}
}

func TestTryAssignOrphanClaimReturnsMissingOwnerOrErrorsOnMissingReservation(t *testing.T) {
	db := openTryAssignDB(t)
	task := createTryAssignTask(t, db, "task-1")
	if err := db.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(TaskClaims).Put([]byte(task.Id), []byte("node-1"))
	}); err != nil {
		t.Fatal(err)
	}

	owner, err := db.TryAssign(context.Background(), task, nil)
	if err != nil || owner != "node-1" {
		t.Fatalf("lookup missing owner = %q, %v; want node-1", owner, err)
	}

	createTryAssignNode(t, db, "node-1")
	owner, err = db.TryAssign(context.Background(), task, nil)
	if err == nil || owner != "" || !strings.Contains(err.Error(), "without a node assignment") {
		t.Fatalf("lookup orphan claim = %q, %v; want reservation error", owner, err)
	}
}

func TestTryAssignConcurrentCandidatesPersistOneAssignment(t *testing.T) {
	db := openTryAssignDB(t)
	task := createTryAssignTask(t, db, "task-1")
	candidates := []*scheduler.Node{
		createTryAssignNode(t, db, "node-1"),
		createTryAssignNode(t, db, "node-2"),
	}

	start := make(chan struct{})
	owners := make([]string, len(candidates))
	errs := make([]error, len(candidates))
	var wg sync.WaitGroup
	for i, candidate := range candidates {
		wg.Add(1)
		go func(i int, candidate *scheduler.Node) {
			defer wg.Done()
			<-start
			owners[i], errs[i] = db.TryAssign(context.Background(), task, candidate)
		}(i, candidate)
	}
	close(start)
	wg.Wait()

	if errs[0] != nil || errs[1] != nil {
		t.Fatalf("concurrent TryAssign errors = [%v, %v]", errs[0], errs[1])
	}
	if owners[0] == "" || owners[0] != owners[1] {
		t.Fatalf("concurrent owners = [%q, %q]; want one shared owner", owners[0], owners[1])
	}

	assigned := 0
	for _, candidate := range candidates {
		node, err := db.GetNode(context.Background(), &scheduler.GetNodeRequest{Id: candidate.Id})
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range node.TaskIds {
			if id == task.Id {
				assigned++
				if node.Id != owners[0] {
					t.Fatalf("task assigned to %q but owner is %q", node.Id, owners[0])
				}
			}
		}
	}
	if assigned != 1 {
		t.Fatalf("task appears on %d nodes; want exactly one", assigned)
	}
}
