package schedulercontract

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/server"
	"github.com/calypr/funnel/tes"
)

func runExecutionClaimContracts(t *testing.T, open storeFactory) {
	t.Run("lost successful execution response never authorizes a retry", func(t *testing.T) { testLostExecutionResponse(t, open) })
	t.Run("concurrent workers launch exactly one executor", func(t *testing.T) { testConcurrentExecutionWorkers(t, open) })
	t.Run("one execution claim wins concurrent requests", func(t *testing.T) {
		db := open(t)
		task := createTask(t, db, testContext(), "execution-race")
		claimer := db.(events.ExecutionClaimer)
		const contenders = 16
		start := make(chan struct{})
		results := make(chan bool, contenders)
		errs := make(chan error, contenders)
		var wg sync.WaitGroup
		for i := 0; i < contenders; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				acquired, err := claimer.ClaimExecution(testContext(), task.Id)
				results <- acquired
				errs <- err
			}()
		}
		close(start)
		wg.Wait()
		close(results)
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		wins := 0
		for acquired := range results {
			if acquired {
				wins++
			}
		}
		if wins != 1 {
			t.Fatalf("execution winners = %d; want 1", wins)
		}
		if state := getTask(t, db, testContext(), task.Id).State; state != tes.Running {
			t.Fatalf("claimed state = %s", state)
		}
		if acquired, err := claimer.ClaimExecution(testContext(), task.Id); err != nil || acquired {
			t.Fatalf("retry after lost response = %v, %v", acquired, err)
		}
	})
	t.Run("execution claim preserves resource reservation and liveness", func(t *testing.T) {
		db := open(t)
		ctx := testContext()
		createNode(t, db, ctx, "executor-node", 2)
		task := reserveContractTask(t, db, "reserved-execution", "executor-node")
		before := readContractNode(t, db, "executor-node")
		acquired, err := db.(events.ExecutionClaimer).ClaimExecution(ctx, task.Id)
		if err != nil || !acquired {
			t.Fatalf("claim = %v, %v", acquired, err)
		}
		after := readContractNode(t, db, "executor-node")
		if len(after.TaskIds) != 1 || after.TaskIds[0] != task.Id || after.Available.Cpus != before.Available.Cpus || after.LastPing != before.LastPing {
			t.Fatalf("claim changed reservation/liveness: %v", after)
		}
		if err := db.WriteEvent(ctx, events.NewState(task.Id, tes.Initializing)); err == nil {
			t.Fatal("late scheduler initialization overwrote execution")
		}
		completeContractTask(t, db, task.Id)
		if node := readContractNode(t, db, "executor-node"); len(node.TaskIds) != 0 || node.Available.Cpus != 2 {
			t.Fatalf("completion did not release allocation: %v", node)
		}
	})
	t.Run("execution marker survives task state edits and recreation", func(t *testing.T) {
		db := open(t)
		ctx := testContext()
		claimer := db.(events.ExecutionClaimer)
		task := createTask(t, db, ctx, "claimed-id")
		if acquired, err := claimer.ClaimExecution(ctx, task.Id); err != nil || !acquired {
			t.Fatalf("claim = %v, %v", acquired, err)
		}
		if err := db.WriteEvent(ctx, events.NewState(task.Id, tes.SystemError)); err != nil {
			t.Fatal(err)
		}
		// Legacy retry transitions may be rejected. If accepted, they must not grant
		// another launch for an ID whose execution was already authorized.
		_ = db.WriteEvent(ctx, events.NewState(task.Id, tes.Queued))
		if acquired, err := claimer.ClaimExecution(ctx, task.Id); acquired {
			t.Fatalf("state edit granted another execution: %v", err)
		}
		if err := db.WriteEvent(ctx, events.NewTaskCreated(testTask(task.Id))); err == nil {
			t.Fatal("recreated an execution-claimed ID")
		}
		if acquired, err := claimer.ClaimExecution(ctx, task.Id); acquired {
			t.Fatalf("recreation granted another execution: %v", err)
		}
	})
	t.Run("unauthorized or canceled acquisition cannot launch", func(t *testing.T) {
		db := open(t)
		ctx := testContext()
		claimer := db.(events.ExecutionClaimer)
		task := createTask(t, db, ctx, "protected-execution")
		stranger := context.Background()
		auth := server.NewAuthentication(nil, nil, server.AccessOwner)
		_, err := auth.Interceptor(stranger, nil, nil, func(ctx context.Context, _ interface{}) (interface{}, error) { stranger = ctx; return nil, nil })
		if err != nil {
			t.Fatal(err)
		}
		if acquired, err := claimer.ClaimExecution(stranger, task.Id); acquired || !errors.Is(err, tes.ErrNotPermitted) {
			t.Fatalf("unauthorized claim = %v, %v", acquired, err)
		}
		if state := getTask(t, db, ctx, task.Id).State; state != tes.Queued {
			t.Fatalf("unauthorized claim changed state: %s", state)
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if acquired, err := claimer.ClaimExecution(canceled, task.Id); acquired || err == nil {
			t.Fatalf("canceled claim = %v, %v", acquired, err)
		}
		if acquired, err := claimer.ClaimExecution(ctx, task.Id); err != nil || !acquired {
			t.Fatalf("failed acquisition consumed claim: %v, %v", acquired, err)
		}
	})
	t.Run("execution acquisition races cancellation safely", func(t *testing.T) {
		db := open(t)
		ctx := testContext()
		claimer := db.(events.ExecutionClaimer)
		for i := 0; i < 8; i++ {
			task := createTask(t, db, ctx, fmt.Sprintf("cancel-race-%d", i))
			start := make(chan struct{})
			errs := make(chan error, 2)
			go func() { <-start; _, err := claimer.ClaimExecution(ctx, task.Id); errs <- err }()
			go func() { <-start; errs <- db.WriteEvent(ctx, events.NewState(task.Id, tes.Canceled)) }()
			close(start)
			for j := 0; j < 2; j++ {
				if err := <-errs; err != nil {
					t.Fatal(err)
				}
			}
			if state := getTask(t, db, ctx, task.Id).State; state != tes.Canceled {
				t.Fatalf("cancellation was overwritten: %s", state)
			}
			if acquired, err := claimer.ClaimExecution(ctx, task.Id); err != nil || acquired {
				t.Fatalf("claim after cancellation=%v,%v", acquired, err)
			}
		}
	})

	t.Run("terminal tasks cannot acquire execution", func(t *testing.T) {
		for _, state := range []tes.State{tes.Complete, tes.Canceled, tes.SystemError, tes.ExecutorError} {
			t.Run(state.String(), func(t *testing.T) {
				db := open(t)
				ctx := testContext()
				task := createTask(t, db, ctx, "terminal")
				if err := db.WriteEvent(ctx, events.NewState(task.Id, state)); err != nil {
					t.Fatal(err)
				}
				if acquired, err := db.(events.ExecutionClaimer).ClaimExecution(ctx, task.Id); acquired {
					t.Fatalf("terminal task acquired execution: %v", err)
				}
				if got := getTask(t, db, ctx, task.Id).State; got != state {
					t.Fatalf("claim changed terminal state: %s", got)
				}
			})
		}
	})
}
