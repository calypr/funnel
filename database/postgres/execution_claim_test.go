package postgres

import (
	"context"
	"sync"
	"testing"

	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/server"
	"github.com/calypr/funnel/tes"
	"github.com/testcontainers/testcontainers-go"
)

func executionClaimContext(owner string) context.Context {
	return context.WithValue(context.Background(), server.UserInfoKey, &server.UserInfo{Username: owner})
}

func TestPostgresClaimExecutionIsDurableAndOneShot(t *testing.T) {
	db, container := getTestPostgres(t)
	t.Cleanup(func() {
		db.Close()
		if err := testcontainers.TerminateContainer(container); err != nil {
			t.Logf("terminate PostgreSQL container: %v", err)
		}
	})
	ctx := executionClaimContext("owner")
	for _, id := range []string{"concurrent", "rollback", "private", "legacy-terminal"} {
		if err := db.WriteEvent(ctx, events.NewTaskCreated(newTestTask(id, id, "owner"))); err != nil {
			t.Fatal(err)
		}
	}

	const workers = 32
	start := make(chan struct{})
	results := make(chan bool, workers)
	errors := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			claimed, err := db.ClaimExecution(ctx, "concurrent")
			results <- claimed
			errors <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errors)
	wins := 0
	for claimed := range results {
		if claimed {
			wins++
		}
	}
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if wins != 1 {
		t.Fatalf("concurrent claims succeeded %d times, want once", wins)
	}
	assertPostgresClaimState(t, db, ctx, "concurrent", tes.State_INITIALIZING, true)
	if claimed, err := db.ClaimExecution(ctx, "concurrent"); err != nil || claimed {
		t.Fatalf("retry claim = %t, %v; want false, nil", claimed, err)
	}
	if err := db.WriteEvent(ctx, events.NewState("concurrent", tes.State_COMPLETE)); err != nil {
		t.Fatal(err)
	}
	assertPostgresClaimState(t, db, ctx, "concurrent", tes.State_COMPLETE, true)
	if err := db.WriteEvent(ctx, events.NewTaskCreated(newTestTask("concurrent", "duplicate", "owner"))); err == nil {
		t.Fatal("task creation reused an existing execution ID")
	}
	if claimed, err := db.ClaimExecution(ctx, "concurrent"); err != nil || claimed {
		t.Fatalf("claim after terminal state = %t, %v; want false, nil", claimed, err)
	}

	if _, err := db.client.Exec(ctx, `ALTER TABLE tasks ADD CONSTRAINT test_block_initializing CHECK (state <> 'INITIALIZING' OR id <> 'rollback')`); err != nil {
		t.Fatal(err)
	}
	if claimed, err := db.ClaimExecution(ctx, "rollback"); err == nil || claimed {
		t.Fatalf("claim with rejecting constraint = %t, %v; want false, error", claimed, err)
	}
	assertPostgresClaimState(t, db, ctx, "rollback", tes.State_QUEUED, false)
	if _, err := db.client.Exec(ctx, `ALTER TABLE tasks DROP CONSTRAINT test_block_initializing`); err != nil {
		t.Fatal(err)
	}
	if claimed, err := db.ClaimExecution(ctx, "rollback"); err != nil || !claimed {
		t.Fatalf("claim after rollback repair = %t, %v; want true, nil", claimed, err)
	}

	unauthorized := context.Background()
	_, err := server.NewAuthentication(nil, nil, server.AccessOwner).Interceptor(unauthorized, nil, nil,
		func(authCtx context.Context, _ interface{}) (interface{}, error) {
			unauthorized = authCtx
			return nil, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := db.ClaimExecution(unauthorized, "private"); err != tes.ErrNotPermitted || claimed {
		t.Fatalf("unauthorized claim = %t, %v; want false, not permitted", claimed, err)
	}
	assertPostgresClaimState(t, db, ctx, "private", tes.State_QUEUED, false)

	if _, err := db.client.Exec(ctx, `UPDATE tasks SET state='SYSTEM_ERROR',execution_claimed=false WHERE id='legacy-terminal'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.client.Exec(ctx, `DELETE FROM scheduler_migrations WHERE name='execution-claims-v1'`); err != nil {
		t.Fatal(err)
	}
	if err := db.migrateExecutionClaims(ctx); err != nil {
		t.Fatal(err)
	}
	if claimed, err := db.ClaimExecution(ctx, "legacy-terminal"); err != nil || claimed {
		t.Fatalf("legacy terminal claim = %t, %v; want false, nil", claimed, err)
	}
	assertPostgresClaimState(t, db, ctx, "legacy-terminal", tes.State_SYSTEM_ERROR, true)
}

func assertPostgresClaimState(t *testing.T, db *Postgres, ctx context.Context, id string, state tes.State, marker bool) {
	t.Helper()
	task, err := db.GetTask(ctx, &tes.GetTaskRequest{Id: id, View: tes.View_FULL.String()})
	if err != nil {
		t.Fatal(err)
	}
	if task.State != state {
		t.Fatalf("task %q state = %s, want %s", id, task.State, state)
	}
	var got bool
	if err := db.client.QueryRow(ctx, `SELECT execution_claimed FROM tasks WHERE id=$1`, id).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != marker {
		t.Fatalf("task %q execution marker = %t, want %t", id, got, marker)
	}
}
