package mongodb

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/calypr/funnel/compute/scheduler"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/server"
	"github.com/calypr/funnel/tes"
	"github.com/calypr/funnel/tests/mongotest"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func readStoredNodeDocument(t *testing.T, db *MongoDB, ctx context.Context, nodeID string) bson.M {
	t.Helper()
	var document bson.M
	if err := db.nodes().FindOne(ctx, bson.M{"id": nodeID}).Decode(&document); err != nil {
		t.Fatalf("read stored node %q: %v", nodeID, err)
	}
	return document
}

func descriptiveNodeDocument(document bson.M) bson.M {
	mutable := map[string]struct{}{
		"reserved": {}, "version": {}, "available": {},
		"taskids": {}, "task_ids": {}, "taskIds": {},
	}
	payload := make(bson.M, len(document))
	for key, value := range document {
		if _, isMutable := mutable[key]; !isMutable {
			payload[key] = value
		}
	}
	return payload
}

func containsTaskID(value any, taskID string) bool {
	switch value := value.(type) {
	case string:
		return value == taskID
	case bson.A:
		for _, item := range value {
			if containsTaskID(item, taskID) {
				return true
			}
		}
	case []string:
		for _, item := range value {
			if item == taskID {
				return true
			}
		}
	case map[string]any:
		for _, item := range value {
			if containsTaskID(item, taskID) {
				return true
			}
		}
	}
	return false
}

func assertTaskIDNotStored(t *testing.T, document bson.M, taskID string) {
	t.Helper()
	for _, field := range []string{"taskids", "task_ids", "taskIds"} {
		if value, exists := document[field]; exists && containsTaskID(value, taskID) {
			t.Fatalf("stored node field %q contains assigned task ID %q: %#v", field, taskID, value)
		}
	}
}

func TestReservationAssignmentAndReleaseLeaveNodePayloadUntouched(t *testing.T) {
	uri := mongotest.Start(t)
	db := newMongoTestDB(t, uri, "budget_projection")
	if err := db.Init(); err != nil {
		t.Fatalf("initialize MongoDB: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), server.UserInfoKey, server.UserInfo{Username: "owner", IsAdmin: true}), 10*time.Second)
	defer cancel()
	const nodeID = "budget-projection-node"
	const taskID = "budget-projection-task"

	task := &tes.Task{Id: taskID, Resources: &tes.Resources{CpuCores: 1, RamGb: 1, DiskGb: 1}}
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
	payloadBefore := descriptiveNodeDocument(readStoredNodeDocument(t, db, ctx, nodeID))

	owner, err := db.TryAssign(ctx, task, candidate)
	if err != nil || owner != nodeID {
		t.Fatalf("TryAssign = %q, %v; want %q", owner, err, nodeID)
	}
	storedAfterAssign := readStoredNodeDocument(t, db, ctx, nodeID)
	assertTaskIDNotStored(t, storedAfterAssign, taskID)
	if payloadAfter := descriptiveNodeDocument(storedAfterAssign); !reflect.DeepEqual(payloadBefore, payloadAfter) {
		t.Fatalf("assignment changed descriptive node fields:\nbefore: %#v\nafter:  %#v", payloadBefore, payloadAfter)
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
	storedAfterRelease := readStoredNodeDocument(t, db, ctx, nodeID)
	assertTaskIDNotStored(t, storedAfterRelease, taskID)
	if payloadAfter := descriptiveNodeDocument(storedAfterRelease); !reflect.DeepEqual(payloadBefore, payloadAfter) {
		t.Fatalf("terminal release changed descriptive node fields:\nbefore: %#v\nafter:  %#v", payloadBefore, payloadAfter)
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
