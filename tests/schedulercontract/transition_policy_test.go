package schedulercontract

import (
	"fmt"
	"testing"

	"github.com/calypr/funnel/internal/compute/scheduler"
	"github.com/calypr/funnel/internal/events"
	"github.com/calypr/funnel/tes"
)

func runUnallocatedErrorRetryContracts(t *testing.T, open storeFactory) {
	for _, from := range []tes.State{tes.State_EXECUTOR_ERROR, tes.State_SYSTEM_ERROR} {
		for _, to := range []tes.State{tes.State_QUEUED, tes.State_INITIALIZING} {
			caseName := fmt.Sprintf("%s_to_%s_preserves_execution_marker", from, to)
			t.Run(caseName, func(t *testing.T) {
				db := open(t)
				ctx := testContext()
				id := "retry-" + caseName
				task := createTask(t, db, ctx, id)
				claimer, ok := db.(events.ExecutionClaimer)
				if !ok {
					t.Fatal("store does not support durable execution claims")
				}
				if claimed, err := claimer.ClaimExecution(ctx, task.Id, nil); err != nil || !claimed {
					t.Fatalf("initial execution claim = %t, %v; want true", claimed, err)
				}
				if err := db.WriteEvent(ctx, events.NewState(task.Id, from)); err != nil {
					t.Fatalf("write error state %s: %v", from, err)
				}
				if err := db.WriteEvent(ctx, events.NewState(task.Id, to)); err != nil {
					t.Fatalf("retry transition %s -> %s: %v", from, to, err)
				}
				if got := getTask(t, db, ctx, task.Id).State; got != to {
					t.Fatalf("state after retry = %s; want %s", got, to)
				}

				queued := false
				for _, item := range db.ReadQueue(100, nil) {
					if item.Id == task.Id {
						queued = true
						break
					}
				}
				if queued != (to == tes.State_QUEUED) {
					t.Fatalf("queue contains task = %t; want %t", queued, to == tes.State_QUEUED)
				}
				if claimed, err := claimer.ClaimExecution(ctx, task.Id, nil); err != nil || claimed {
					t.Fatalf("claim after retry = %t, %v; want durable marker to deny another execution", claimed, err)
				}
			})
		}
	}
	t.Run("assigned error task cannot retry after its reservation retires", func(t *testing.T) {
		db := open(t)
		ctx := testContext()
		task := createTask(t, db, ctx, "assigned-error-retry")
		node := createNode(t, db, ctx, "assigned-error-node", 2)
		node, err := db.GetNode(ctx, &scheduler.GetNodeRequest{Id: node.Id})
		if err != nil {
			t.Fatal(err)
		}
		if owner, err := db.TryAssign(ctx, task, node); err != nil || owner != node.Id {
			t.Fatalf("TryAssign = %q, %v; want %s", owner, err, node.Id)
		}
		claimer := db.(events.ExecutionClaimer)
		if claimed, err := claimer.ClaimExecution(ctx, task.Id, nil); err != nil || !claimed {
			t.Fatalf("initial execution claim = %t, %v; want true", claimed, err)
		}
		if err := db.WriteEvent(ctx, events.NewState(task.Id, tes.State_SYSTEM_ERROR)); err != nil {
			t.Fatalf("write SYSTEM_ERROR: %v", err)
		}
		if err := db.WriteEvent(ctx, events.NewState(task.Id, tes.State_QUEUED)); err == nil {
			t.Fatal("assigned task retried after its allocation was retired")
		}
		if got := getTask(t, db, ctx, task.Id).State; got != tes.State_SYSTEM_ERROR {
			t.Fatalf("state after denied retry = %s; want SYSTEM_ERROR", got)
		}
		for _, item := range db.ReadQueue(100, nil) {
			if item.Id == task.Id {
				t.Fatal("denied retry added assigned task to the queue")
			}
		}
		currentNode, err := db.GetNode(ctx, &scheduler.GetNodeRequest{Id: node.Id})
		if err != nil {
			t.Fatal(err)
		}
		if currentNode.Available.Cpus != 2 || currentNode.Available.RamGb != 2 || currentNode.Available.DiskGb != 2 || len(currentNode.TaskIds) != 0 {
			t.Fatalf("retired reservation = %v; want full capacity and no task IDs", currentNode)
		}
		if claimed, err := claimer.ClaimExecution(ctx, task.Id, nil); err != nil || claimed {
			t.Fatalf("claim after denied retry = %t, %v; want execution marker to deny another execution", claimed, err)
		}
	})
}
