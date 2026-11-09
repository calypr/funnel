package boltdb

import (
	"context"
	"testing"

	"github.com/calypr/funnel/compute/scheduler"
	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/logger"
	"github.com/calypr/funnel/server"
	"github.com/calypr/funnel/tes"
	bolt "go.etcd.io/bbolt"
)

func TestPersistedLegacyTrapCanBeCanceledOrCleaned(t *testing.T) {
	db := openTryAssignDB(t)
	ctx := executionClaimContext("owner")
	for _, legacy := range []tes.State{tes.State_PREEMPTED, tes.State_CANCELING} {
		for _, target := range []tes.State{tes.State_CANCELED, tes.State_SYSTEM_ERROR} {
			name := legacy.String() + "_to_" + target.String()
			t.Run(name, func(t *testing.T) {
				incomingID := "incoming-" + name
				incoming := createTryAssignTask(t, db, incomingID)
				if err := db.WriteEvent(ctx, events.NewState(incoming.Id, legacy)); err == nil {
					t.Errorf("QUEUED -> %s succeeded; want unsupported state rejection", legacy)
				}
				if stored, err := db.GetTask(ctx, &tes.GetTaskRequest{Id: incomingID}); err != nil || stored.State != tes.State_QUEUED {
					t.Errorf("state after rejected transition = %v, %v; want QUEUED", stored, err)
				}

				id := "legacy-" + name
				task := createTryAssignTask(t, db, id)
				node := createTryAssignNode(t, db, "node-"+id)
				if owner, err := db.TryAssign(ctx, task, node); err != nil || owner != node.Id {
					t.Fatalf("TryAssign = %q, %v; want %s", owner, err, node.Id)
				}
				if claimed, err := db.ClaimExecution(ctx, id, nil); err != nil || !claimed {
					t.Fatalf("ClaimExecution = %t, %v; want true", claimed, err)
				}
				if err := seedBoltLegacyTaskState(db, id, legacy); err != nil {
					t.Fatal(err)
				}
				assertBoltLegacyReservation(t, db, ctx, id, node.Id, 3, 7, 19, 1)

				if target == tes.State_CANCELED {
					log := logger.NewLogger("legacy-transition", logger.DefaultConfig())
					log.Discard()
					service := &server.TaskService{
						Read: db, Event: db, Compute: events.Backend{},
						Config: config.DefaultConfig(), Log: log,
					}
					if _, err := service.CancelTask(ctx, &tes.CancelTaskRequest{Id: id}); err != nil {
						t.Fatalf("CancelTask from persisted %s: %v", legacy, err)
					}
				} else if err := db.WriteEvent(ctx, events.NewState(id, target)); err != nil {
					t.Fatalf("scheduler cleanup from persisted %s: %v", legacy, err)
				}
				if err := db.WriteEvent(ctx, events.NewState(id, target)); err != nil {
					t.Fatalf("repeat terminal event %s: %v", target, err)
				}
				assertBoltLegacyReservation(t, db, ctx, id, node.Id, 4, 8, 20, 0)
				stored, err := db.GetTask(ctx, &tes.GetTaskRequest{Id: id})
				if err != nil || stored.State != target {
					t.Fatalf("stored task = %v, %v; want state %s", stored, err, target)
				}
				if claimed, err := db.ClaimExecution(ctx, id, nil); err != nil || claimed {
					t.Fatalf("ClaimExecution after recovery = %t, %v; want durable marker to deny another execution", claimed, err)
				}
				var owner string
				var marker bool
				if err := db.db.View(func(tx *bolt.Tx) error {
					owner = string(tx.Bucket(TaskClaims).Get([]byte(id)))
					marker = tx.Bucket(ExecutionClaims).Get([]byte(id)) != nil
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				if owner != node.Id || !marker {
					t.Fatalf("historical owner/marker = %q/%t; want %q/true", owner, marker, node.Id)
				}
			})
		}
	}
}

func TestUnknownTaskStateCanReturnToQueue(t *testing.T) {
	db := openTryAssignDB(t)
	ctx := executionClaimContext("owner")
	task := createTryAssignTask(t, db, "unknown-requeue")
	if err := db.db.Update(func(tx *bolt.Tx) error {
		if err := tx.Bucket(TaskState).Put([]byte(task.Id), []byte(tes.State_UNKNOWN.String())); err != nil {
			return err
		}
		return tx.Bucket(TasksQueued).Delete([]byte(task.Id))
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.WriteEvent(ctx, events.NewState(task.Id, tes.State_QUEUED)); err != nil {
		t.Fatalf("UNKNOWN -> QUEUED: %v", err)
	}
	stored, err := db.GetTask(ctx, &tes.GetTaskRequest{Id: task.Id})
	if err != nil || stored.State != tes.State_QUEUED {
		t.Fatalf("stored task = %v, %v; want QUEUED", stored, err)
	}
	for _, queued := range db.ReadQueue(100, nil) {
		if queued.Id == task.Id {
			return
		}
	}
	t.Fatal("UNKNOWN -> QUEUED did not add the task to the scheduler queue")
}

func seedBoltLegacyTaskState(db *BoltDB, id string, state tes.State) error {
	return db.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(TaskState).Put([]byte(id), []byte(state.String()))
	})
}

func assertBoltLegacyReservation(t *testing.T, db *BoltDB, ctx context.Context, id, nodeID string, cpus uint32, ramGb, diskGb float64, taskIDs int) {
	t.Helper()
	node, err := db.GetNode(ctx, &scheduler.GetNodeRequest{Id: nodeID})
	if err != nil {
		t.Fatal(err)
	}
	if node.Available.Cpus != cpus || node.Available.RamGb != ramGb || node.Available.DiskGb != diskGb || len(node.TaskIds) != taskIDs {
		t.Fatalf("reservation after %s = available %v, task IDs %v; want CPUs/RAM/disk %d and %d task IDs", id, node.Available, node.TaskIds, cpus, taskIDs)
	}
	if taskIDs == 0 {
		for _, task := range db.ReadQueue(100, nil) {
			if task.Id == id {
				t.Fatalf("terminal task %s remains in the scheduler queue", id)
			}
		}
	}
}
