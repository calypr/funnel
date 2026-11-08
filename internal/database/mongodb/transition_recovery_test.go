package mongodb

import (
	"context"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/calypr/funnel/internal/compute/scheduler"
	appconfig "github.com/calypr/funnel/internal/config"
	"github.com/calypr/funnel/internal/events"
	"github.com/calypr/funnel/internal/logger"
	"github.com/calypr/funnel/internal/server"
	"github.com/calypr/funnel/tes"
	"github.com/calypr/funnel/tests/mongotest"
)

func TestPersistedLegacyTrapCanBeCanceledOrCleaned(t *testing.T) {
	db := newMongoTestDB(t, mongotest.Start(t), "legacy_transition")
	if err := db.Init(); err != nil {
		t.Fatal(err)
	}
	ctx := executionClaimContext("owner")
	for _, legacy := range []tes.State{tes.State_PREEMPTED, tes.State_CANCELING} {
		for _, target := range []tes.State{tes.State_CANCELED, tes.State_SYSTEM_ERROR} {
			name := legacy.String() + "_to_" + target.String()
			t.Run(name, func(t *testing.T) {
				incomingID := "incoming-" + name
				incoming := &tes.Task{Id: incomingID, Resources: &tes.Resources{CpuCores: 1, RamGb: 1, DiskGb: 1}}
				if err := db.WriteEvent(ctx, events.NewTaskCreated(incoming)); err != nil {
					t.Fatal(err)
				}
				if err := db.WriteEvent(ctx, events.NewState(incoming.Id, legacy)); err == nil {
					t.Errorf("QUEUED -> %s succeeded; want unsupported state rejection", legacy)
				}
				if stored, err := db.GetTask(ctx, &tes.GetTaskRequest{Id: incomingID}); err != nil || stored.State != tes.State_QUEUED {
					t.Errorf("state after rejected transition = %v, %v; want QUEUED", stored, err)
				}

				id := "legacy-" + name
				task := &tes.Task{Id: id, Resources: &tes.Resources{CpuCores: 1, RamGb: 1, DiskGb: 1}}
				if err := db.WriteEvent(ctx, events.NewTaskCreated(task)); err != nil {
					t.Fatal(err)
				}
				node := &scheduler.Node{Id: "node-" + id, State: scheduler.NodeState_ALIVE, Resources: &scheduler.Resources{Cpus: 2, RamGb: 2, DiskGb: 2}}
				if _, err := db.PutNode(ctx, node); err != nil {
					t.Fatal(err)
				}
				node, err := db.GetNode(ctx, &scheduler.GetNodeRequest{Id: node.Id})
				if err != nil {
					t.Fatal(err)
				}
				if owner, err := db.TryAssign(ctx, task, node); err != nil || owner != node.Id {
					t.Fatalf("TryAssign = %q, %v; want %s", owner, err, node.Id)
				}
				if claimed, err := db.ClaimExecution(ctx, id, nil); err != nil || !claimed {
					t.Fatalf("ClaimExecution = %t, %v; want true", claimed, err)
				}
				if _, err := db.tasks().UpdateOne(ctx, bson.M{"id": id}, bson.M{"$set": bson.M{"state": legacy}}); err != nil {
					t.Fatal(err)
				}
				assertMongoLegacyReservation(t, ctx, db, id, node.Id, 1, 1, 1, 1)

				if target == tes.State_CANCELED {
					log := logger.NewLogger("legacy-transition", logger.DefaultConfig())
					log.Discard()
					service := &server.TaskService{Read: db, Event: db, Compute: events.Backend{}, Config: appconfig.DefaultConfig(), Log: log}
					if _, err := service.CancelTask(ctx, &tes.CancelTaskRequest{Id: id}); err != nil {
						t.Fatalf("CancelTask from persisted %s: %v", legacy, err)
					}
				} else if err := db.WriteEvent(ctx, events.NewState(id, target)); err != nil {
					t.Fatalf("scheduler cleanup from persisted %s: %v", legacy, err)
				}
				if err := db.WriteEvent(ctx, events.NewState(id, target)); err != nil {
					t.Fatalf("repeat terminal event %s: %v", target, err)
				}
				assertMongoLegacyReservation(t, ctx, db, id, node.Id, 2, 2, 2, 0)
				stored, err := db.GetTask(ctx, &tes.GetTaskRequest{Id: id})
				if err != nil || stored.State != target {
					t.Fatalf("stored task = %v, %v; want state %s", stored, err, target)
				}
				if claimed, err := db.ClaimExecution(ctx, id, nil); err != nil || claimed {
					t.Fatalf("ClaimExecution after recovery = %t, %v; want durable marker to deny another execution", claimed, err)
				}
				var record taskRecord
				if err := db.tasks().FindOne(ctx, bson.M{"id": id}).Decode(&record); err != nil {
					t.Fatal(err)
				}
				if record.ClaimOwner != node.Id || record.ClaimActive || !record.ExecutionClaimed {
					t.Fatalf("historical owner/active/marker = %q/%t/%t; want %q/false/true", record.ClaimOwner, record.ClaimActive, record.ExecutionClaimed, node.Id)
				}
			})
		}
	}
}

func assertMongoLegacyReservation(t *testing.T, ctx context.Context, db *MongoDB, id, nodeID string, cpus uint32, ramGb, diskGb float64, taskIDs int) {
	t.Helper()
	node, err := db.GetNode(ctx, &scheduler.GetNodeRequest{Id: nodeID})
	if err != nil {
		t.Fatal(err)
	}
	if node.Available.Cpus != cpus || node.Available.RamGb != ramGb || node.Available.DiskGb != diskGb || len(node.TaskIds) != taskIDs {
		t.Fatalf("reservation after %s = available %v, task IDs %v; want %d/%g/%g and %d task IDs", id, node.Available, node.TaskIds, cpus, ramGb, diskGb, taskIDs)
	}
	queued := false
	for _, task := range db.ReadQueue(100, nil) {
		if task.Id == id {
			queued = true
			break
		}
	}
	if queued {
		t.Fatalf("terminal task %s remains in the scheduler queue", id)
	}
}
