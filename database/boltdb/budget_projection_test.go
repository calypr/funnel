package boltdb

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/calypr/funnel/compute/scheduler"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/tes"
	bolt "go.etcd.io/bbolt"
)

func copyStoredNodeAndBudget(t *testing.T, db *BoltDB, nodeID string) ([]byte, nodeBudget) {
	t.Helper()

	var nodeData, budgetData []byte
	if err := db.db.View(func(tx *bolt.Tx) error {
		nodeData = append([]byte(nil), tx.Bucket(Nodes).Get([]byte(nodeID))...)
		budgetData = append([]byte(nil), tx.Bucket(NodeUsage).Get([]byte(nodeID))...)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if nodeData == nil {
		t.Fatalf("stored node %q is missing", nodeID)
	}
	var budget nodeBudget
	if err := json.Unmarshal(budgetData, &budget); err != nil {
		t.Fatalf("decode node budget for %q: %v", nodeID, err)
	}
	return nodeData, budget
}

func TestReservationAssignmentAndReleaseLeaveNodePayloadUntouched(t *testing.T) {
	db := openTryAssignDB(t)
	ctx := context.Background()
	const nodeID = "budget-projection-node"
	const taskID = "budget-projection-task"

	task := &tes.Task{Id: taskID, Resources: &tes.Resources{CpuCores: 1, RamGb: 1, DiskGb: 1}}
	if err := db.WriteEvent(ctx, events.NewTaskCreated(task)); err != nil {
		t.Fatalf("create task: %v", err)
	}
	reported := &scheduler.Node{
		Id:        nodeID,
		State:     scheduler.NodeState_ALIVE,
		Resources: &scheduler.Resources{Cpus: 4, RamGb: 8, DiskGb: 20},
		Metadata:  map[string]string{"rack": "west", "owner": "scheduler-test"},
	}
	if _, err := db.PutNode(ctx, reported); err != nil {
		t.Fatalf("create node: %v", err)
	}
	candidate, err := db.GetNode(ctx, &scheduler.GetNodeRequest{Id: nodeID})
	if err != nil {
		t.Fatalf("get initial node: %v", err)
	}
	payloadBefore, budgetBefore := copyStoredNodeAndBudget(t, db, nodeID)
	if candidate.Version != budgetBefore.Version {
		t.Fatalf("initial public version = %d, stored budget version = %d", candidate.Version, budgetBefore.Version)
	}

	owner, err := db.TryAssign(ctx, task, candidate)
	if err != nil || owner != nodeID {
		t.Fatalf("TryAssign = %q, %v; want %q", owner, err, nodeID)
	}
	payloadAfterAssign, assignedBudget := copyStoredNodeAndBudget(t, db, nodeID)
	if !bytes.Equal(payloadBefore, payloadAfterAssign) {
		t.Fatal("assignment rewrote the stored node payload")
	}
	if assignedBudget.Version != budgetBefore.Version+1 || assignedBudget.ResourceUsage != (scheduler.ResourceUsage{CPUs: 1, RAM: 1, Disk: 1}) {
		t.Fatalf("assigned node budget = %+v; want one task's usage and version %d", assignedBudget, budgetBefore.Version+1)
	}
	assigned, err := db.GetNode(ctx, &scheduler.GetNodeRequest{Id: nodeID})
	if err != nil {
		t.Fatalf("get assigned node: %v", err)
	}
	if assigned.Version != budgetBefore.Version+1 || assigned.Available.GetCpus() != 3 || assigned.Available.GetRamGb() != 7 || assigned.Available.GetDiskGb() != 19 || len(assigned.TaskIds) != 1 || assigned.TaskIds[0] != taskID {
		t.Fatalf("public assigned node = %v; want updated version, capacity, and task ID", assigned)
	}
	if assigned.Metadata["rack"] != "west" || assigned.Metadata["owner"] != "scheduler-test" {
		t.Fatalf("assignment changed node metadata: %v", assigned.Metadata)
	}

	if err := db.WriteEvent(ctx, events.NewState(taskID, tes.State_COMPLETE)); err != nil {
		t.Fatalf("complete task: %v", err)
	}
	payloadAfterRelease, releasedBudget := copyStoredNodeAndBudget(t, db, nodeID)
	if !bytes.Equal(payloadBefore, payloadAfterRelease) {
		t.Fatal("terminal release rewrote the stored node payload")
	}
	if releasedBudget.Version != budgetBefore.Version+2 || releasedBudget.ResourceUsage != (scheduler.ResourceUsage{}) {
		t.Fatalf("released node budget = %+v; want empty usage and version %d", releasedBudget, budgetBefore.Version+2)
	}
	released, err := db.GetNode(ctx, &scheduler.GetNodeRequest{Id: nodeID})
	if err != nil {
		t.Fatalf("get released node: %v", err)
	}
	if released.Version != budgetBefore.Version+2 || released.Available.GetCpus() != 4 || released.Available.GetRamGb() != 8 || released.Available.GetDiskGb() != 20 || len(released.TaskIds) != 0 {
		t.Fatalf("public released node = %v; want restored capacity and no task IDs", released)
	}
	if released.Metadata["rack"] != "west" || released.Metadata["owner"] != "scheduler-test" {
		t.Fatalf("release changed node metadata: %v", released.Metadata)
	}
}
