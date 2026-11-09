package postgres

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/calypr/funnel/internal/server/taskservice"

	"github.com/calypr/funnel/internal/compute"

	"github.com/calypr/funnel/api/tes"
	"github.com/calypr/funnel/internal/compute/scheduler"
	appconfig "github.com/calypr/funnel/internal/config"
	"github.com/calypr/funnel/internal/events"
	"github.com/calypr/funnel/internal/logger"
)

func TestPersistedLegacyTrapCanBeCanceledOrCleaned(t *testing.T) {
	db, container := getTestPostgres(t)
	t.Cleanup(func() {
		db.Close()
		terminateTestContainer(t, container)
	})
	ctx := executionClaimContext("owner")
	for _, legacy := range []tes.State{tes.State_PREEMPTED, tes.State_CANCELING} {
		for _, target := range []tes.State{tes.State_CANCELED, tes.State_SYSTEM_ERROR} {
			name := legacy.String() + "_to_" + target.String()
			t.Run(name, func(t *testing.T) {
				incomingID := "incoming-" + name
				incoming := newTestTask(incomingID, incomingID, "owner")
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
				task := newTestTask(id, id, "owner")
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
				if err := seedPostgresLegacyTaskState(ctx, db, id, legacy); err != nil {
					t.Fatal(err)
				}
				assertPostgresLegacyReservation(t, ctx, db, id, node.Id, 1, 1, 1, 1)

				if target == tes.State_CANCELED {
					log := logger.NewLogger("legacy-transition", logger.DefaultConfig())
					log.Discard()
					service := &taskservice.Service{Read: db, Event: db, Compute: compute.Backend{}, Config: appconfig.DefaultConfig(), Log: log}
					if _, err := service.CancelTask(ctx, &tes.CancelTaskRequest{Id: id}); err != nil {
						t.Fatalf("CancelTask from persisted %s: %v", legacy, err)
					}
				} else if err := db.WriteEvent(ctx, events.NewState(id, target)); err != nil {
					t.Fatalf("scheduler cleanup from persisted %s: %v", legacy, err)
				}
				if err := db.WriteEvent(ctx, events.NewState(id, target)); err != nil {
					t.Fatalf("repeat terminal event %s: %v", target, err)
				}
				assertPostgresLegacyReservation(t, ctx, db, id, node.Id, 2, 2, 2, 0)
				stored, err := db.GetTask(ctx, &tes.GetTaskRequest{Id: id})
				if err != nil || stored.State != target {
					t.Fatalf("stored task = %v, %v; want state %s", stored, err, target)
				}
				if claimed, err := db.ClaimExecution(ctx, id, nil); err != nil || claimed {
					t.Fatalf("ClaimExecution after recovery = %t, %v; want durable marker to deny another execution", claimed, err)
				}
				var owner string
				var active, marker bool
				if err := db.client.QueryRow(ctx, `SELECT COALESCE(claim_owner,''),claim_active,execution_claimed FROM tasks WHERE id=$1`, id).Scan(&owner, &active, &marker); err != nil {
					t.Fatal(err)
				}
				if owner != node.Id || active || !marker {
					t.Fatalf("historical owner/active/marker = %q/%t/%t; want %q/false/true", owner, active, marker, node.Id)
				}
			})
		}
	}
}

func seedPostgresLegacyTaskState(ctx context.Context, db *Postgres, id string, state tes.State) error {
	stateJSON, err := json.Marshal(int32(state))
	if err != nil {
		return err
	}
	_, err = db.client.Exec(ctx, `UPDATE tasks SET state=$2,data=jsonb_set(data,'{state}',$3::jsonb) WHERE id=$1`, id, state.String(), stateJSON)
	return err
}

func assertPostgresLegacyReservation(t *testing.T, ctx context.Context, db *Postgres, id, nodeID string, cpus uint32, ramGb, diskGb float64, taskIDs int) {
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
