package postgres

import (
	"context"
	"testing"

	"github.com/calypr/funnel/compute/scheduler"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/tes"
)

func readStoredNodeData(t *testing.T, db *Postgres, ctx context.Context, nodeID string) string {
	t.Helper()
	var data string
	if err := db.client.QueryRow(ctx, `SELECT data::text FROM nodes WHERE id=$1`, nodeID).Scan(&data); err != nil {
		t.Fatalf("read node payload %q: %v", nodeID, err)
	}
	return data
}

func TestReservationAssignmentAndReleaseLeaveNodePayloadUntouched(t *testing.T) {
	db, ctx := newTryAssignDB(t)
	const nodeID = "budget-projection-node"
	const taskID = "budget-projection-task"

	task := newTestTask(taskID, taskID, testUser)
	if err := db.WriteEvent(ctx, events.NewTaskCreated(task)); err != nil {
		t.Fatalf("create task: %v", err)
	}
	reported := &scheduler.Node{
		Id:        nodeID,
		State:     scheduler.NodeState_ALIVE,
		Resources: &scheduler.Resources{Cpus: 4, RamGb: 8, DiskGb: 10},
		Metadata:  map[string]string{"rack": "west", "owner": "scheduler-test"},
	}
	if _, err := db.PutNode(ctx, reported); err != nil {
		t.Fatalf("create node: %v", err)
	}
	candidate, err := db.GetNode(ctx, &scheduler.GetNodeRequest{Id: nodeID})
	if err != nil {
		t.Fatalf("get initial node: %v", err)
	}
	payloadBefore := readStoredNodeData(t, db, ctx, nodeID)
	for _, statement := range []string{
		`CREATE FUNCTION reject_node_data_write() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'reservation must not target node data'; END; $$`,
		`CREATE TRIGGER reject_node_data_write BEFORE UPDATE OF data ON nodes FOR EACH ROW EXECUTE FUNCTION reject_node_data_write()`,
	} {
		if _, err := db.client.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}

	owner, err := db.TryAssign(ctx, task, candidate)
	if err != nil || owner != nodeID {
		t.Fatalf("TryAssign = %q, %v; want %q", owner, err, nodeID)
	}
	if got := readStoredNodeData(t, db, ctx, nodeID); got != payloadBefore {
		t.Fatalf("assignment rewrote nodes.data:\nbefore: %s\nafter:  %s", payloadBefore, got)
	}
	assigned, err := db.GetNode(ctx, &scheduler.GetNodeRequest{Id: nodeID})
	if err != nil {
		t.Fatalf("get assigned node: %v", err)
	}
	if assigned.Version != candidate.Version+1 || assigned.Available.GetCpus() != 3 || assigned.Available.GetRamGb() != 7 || assigned.Available.GetDiskGb() != 9 || len(assigned.TaskIds) != 1 || assigned.TaskIds[0] != taskID {
		t.Fatalf("public assigned node = %v; want updated version, capacity, and task ID", assigned)
	}
	if assigned.Metadata["rack"] != "west" || assigned.Metadata["owner"] != "scheduler-test" {
		t.Fatalf("assignment changed node metadata: %v", assigned.Metadata)
	}

	if err := db.WriteEvent(ctx, events.NewState(taskID, tes.State_COMPLETE)); err != nil {
		t.Fatalf("complete task: %v", err)
	}
	if got := readStoredNodeData(t, db, ctx, nodeID); got != payloadBefore {
		t.Fatalf("terminal release rewrote nodes.data:\nbefore: %s\nafter:  %s", payloadBefore, got)
	}
	released, err := db.GetNode(ctx, &scheduler.GetNodeRequest{Id: nodeID})
	if err != nil {
		t.Fatalf("get released node: %v", err)
	}
	if released.Version != candidate.Version+2 || released.Available.GetCpus() != 4 || released.Available.GetRamGb() != 8 || released.Available.GetDiskGb() != 10 || len(released.TaskIds) != 0 {
		t.Fatalf("public released node = %v; want restored capacity and no task IDs", released)
	}
	if released.Metadata["rack"] != "west" || released.Metadata["owner"] != "scheduler-test" {
		t.Fatalf("release changed node metadata: %v", released.Metadata)
	}
}
