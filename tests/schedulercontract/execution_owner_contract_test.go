package schedulercontract

import (
	"context"
	"testing"

	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/tes"
)

func runExecutionOwnerContracts(t *testing.T, open storeFactory) {
	t.Run("quoted metadata key cannot alias the execution owner", func(t *testing.T) {
		db := open(t)
		ctx := testContext()
		task := createTask(t, db, ctx, "owner-quoted-key")
		owner := testExecutionOwner("worker-winner")
		if claimed, err := db.(events.ExecutionClaimer).ClaimExecution(ctx, task.Id, owner); err != nil || !claimed {
			t.Fatalf("claim = %v, %v", claimed, err)
		}
		key := `"` + events.ExecutionOwnerMetadataKey + `"`
		if err := db.WriteEvent(ctx, events.NewMetadata(task.Id, 0, map[string]string{key: "ordinary value"})); err != nil {
			t.Fatal(err)
		}
		stored := getFullTask(t, db, ctx, task.Id)
		assertExecutionOwner(t, stored, owner)
		if got := stored.Logs[0].Metadata[key]; got != "ordinary value" {
			t.Fatalf("quoted metadata key value = %q", got)
		}
	})
	t.Run("claim persists its owner and ordinary metadata preserves it", func(t *testing.T) {
		db := open(t)
		ctx := testContext()
		task := createTask(t, db, ctx, "owner-metadata")
		owner := testExecutionOwner("worker-winner")
		claimed, err := db.(events.ExecutionClaimer).ClaimExecution(ctx, task.Id, owner)
		if err != nil || !claimed {
			t.Fatalf("claim = %v, %v", claimed, err)
		}

		if err := db.WriteEvent(ctx, events.NewMetadata(task.Id, 0, map[string]string{"ordinary": "value"})); err != nil {
			t.Fatalf("write ordinary metadata: %v", err)
		}
		stored := getFullTask(t, db, ctx, task.Id)
		assertExecutionOwner(t, stored, owner)
		if got := stored.GetLogs()[0].GetMetadata()["ordinary"]; got != "value" {
			t.Fatalf("ordinary metadata = %q, want %q", got, "value")
		}
	})

	t.Run("losing claim cannot overwrite the winning owner", func(t *testing.T) {
		db := open(t)
		ctx := testContext()
		task := createTask(t, db, ctx, "owner-loser")
		winner := testExecutionOwner("worker-winner")
		if claimed, err := db.(events.ExecutionClaimer).ClaimExecution(ctx, task.Id, winner); err != nil || !claimed {
			t.Fatalf("winning claim = %v, %v", claimed, err)
		}
		loser := testExecutionOwner("worker-loser")
		if claimed, err := db.(events.ExecutionClaimer).ClaimExecution(ctx, task.Id, loser); err != nil || claimed {
			t.Fatalf("losing claim = %v, %v", claimed, err)
		}
		assertExecutionOwner(t, getFullTask(t, db, ctx, task.Id), winner)
	})

	t.Run("ordinary metadata cannot write the reserved owner key", func(t *testing.T) {
		db := open(t)
		ctx := testContext()
		task := createTask(t, db, ctx, "owner-reserved-metadata")
		owner := testExecutionOwner("worker-winner")
		if claimed, err := db.(events.ExecutionClaimer).ClaimExecution(ctx, task.Id, owner); err != nil || !claimed {
			t.Fatalf("claim = %v, %v", claimed, err)
		}

		err := db.WriteEvent(ctx, events.NewMetadata(task.Id, 0, map[string]string{
			events.ExecutionOwnerMetadataKey: `{"namespace":"other","pod_name":"other","pod_uid":"other"}`,
			"must-not-appear":                "value",
		}))
		if err == nil {
			t.Fatal("metadata write containing the reserved key succeeded")
		}
		stored := getFullTask(t, db, ctx, task.Id)
		assertExecutionOwner(t, stored, owner)
		if _, ok := stored.GetLogs()[0].GetMetadata()["must-not-appear"]; ok {
			t.Fatal("ordinary metadata from the rejected event was partially written")
		}
	})

	t.Run("nil owner preserves generic claim behavior", func(t *testing.T) {
		db := open(t)
		ctx := testContext()
		task := createTask(t, db, ctx, "owner-nil")
		if claimed, err := db.(events.ExecutionClaimer).ClaimExecution(ctx, task.Id, nil); err != nil || !claimed {
			t.Fatalf("generic claim = %v, %v", claimed, err)
		}
		stored := getFullTask(t, db, ctx, task.Id)
		owner, err := events.TaskExecutionOwner(stored)
		if err != nil {
			t.Fatal(err)
		}
		if owner != nil {
			t.Fatalf("generic claim owner = %v, want nil", owner)
		}
	})
}

func testExecutionOwner(podName string) *events.ExecutionOwner {
	return &events.ExecutionOwner{Namespace: "scheduler-contract", PodName: podName, PodUid: podName + "-uid"}
}

func getFullTask(t *testing.T, db store, ctx context.Context, id string) *tes.Task {
	t.Helper()
	task, err := db.GetTask(ctx, &tes.GetTaskRequest{Id: id, View: tes.View_FULL.String()})
	if err != nil {
		t.Fatalf("get full task %s: %v", id, err)
	}
	return task
}

func assertExecutionOwner(t *testing.T, task *tes.Task, want *events.ExecutionOwner) {
	t.Helper()
	got, err := events.TaskExecutionOwner(task)
	if err != nil {
		t.Fatalf("read execution owner: %v", err)
	}
	if got == nil || got.Namespace != want.Namespace || got.PodName != want.PodName || got.PodUid != want.PodUid {
		t.Fatalf("execution owner = %v, want %v", got, want)
	}
}
