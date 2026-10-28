package boltdb

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/server"
	"github.com/calypr/funnel/tes"
	bolt "go.etcd.io/bbolt"
)

func executionClaimContext(owner string) context.Context {
	return context.WithValue(context.Background(), server.UserInfoKey, &server.UserInfo{Username: owner})
}

func TestClaimExecutionIsDurableAndOneShot(t *testing.T) {
	db, err := NewBoltDB(&config.BoltDB{Path: t.TempDir() + "/tasks.db"})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Init(); err != nil {
		t.Fatal(err)
	}
	ctx := executionClaimContext("owner")
	for _, id := range []string{"concurrent", "initializing", "rollback", "private"} {
		if err := db.WriteEvent(ctx, events.NewTaskCreated(&tes.Task{Id: id})); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.WriteEvent(ctx, events.NewState("initializing", tes.State_INITIALIZING)); err != nil {
		t.Fatal(err)
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
	assertBoltClaimState(t, db, ctx, "concurrent", tes.State_INITIALIZING, true)
	if claimed, err := db.ClaimExecution(ctx, "concurrent"); err != nil || claimed {
		t.Fatalf("retry claim = %t, %v; want false, nil", claimed, err)
	}
	if err := db.WriteEvent(ctx, events.NewState("concurrent", tes.State_COMPLETE)); err != nil {
		t.Fatal(err)
	}
	assertBoltClaimState(t, db, ctx, "concurrent", tes.State_COMPLETE, true)
	if err := db.WriteEvent(ctx, events.NewTaskCreated(&tes.Task{Id: "concurrent"})); err == nil {
		t.Fatal("task creation reused an existing execution ID")
	}
	if claimed, err := db.ClaimExecution(ctx, "concurrent"); err != nil || claimed {
		t.Fatalf("claim after terminal state = %t, %v; want false, nil", claimed, err)
	}

	if claimed, err := db.ClaimExecution(ctx, "initializing"); err != nil || !claimed {
		t.Fatalf("initializing claim = %t, %v; want true, nil", claimed, err)
	}
	assertBoltClaimState(t, db, ctx, "initializing", tes.State_INITIALIZING, true)

	unauthorized := context.Background()
	_, err = server.NewAuthentication(nil, nil, server.AccessOwner).Interceptor(unauthorized, nil, nil,
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
	assertBoltClaimState(t, db, ctx, "private", tes.State_QUEUED, false)

	if err := db.db.Update(func(tx *bolt.Tx) error {
		_, err := tx.Bucket(ExecutionClaims).CreateBucket([]byte("rollback"))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if claimed, err := db.ClaimExecution(ctx, "rollback"); err == nil || claimed {
		t.Fatalf("claim with invalid marker slot = %t, %v; want false, error", claimed, err)
	}
	assertBoltClaimState(t, db, ctx, "rollback", tes.State_QUEUED, false)
	if err := db.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(ExecutionClaims).DeleteBucket([]byte("rollback"))
	}); err != nil {
		t.Fatal(err)
	}
	if claimed, err := db.ClaimExecution(ctx, "rollback"); err != nil || !claimed {
		t.Fatalf("claim after rollback repair = %t, %v; want true, nil", claimed, err)
	}
}

func TestInitBackfillsLegacyExecutionClaims(t *testing.T) {
	db, err := NewBoltDB(&config.BoltDB{Path: t.TempDir() + "/tasks.db"})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Init(); err != nil {
		t.Fatal(err)
	}
	ctx := executionClaimContext("owner")
	if err := db.WriteEvent(ctx, events.NewTaskCreated(&tes.Task{Id: "legacy-running"})); err != nil {
		t.Fatal(err)
	}
	if err := db.db.Update(func(tx *bolt.Tx) error {
		if err := tx.Bucket(TaskState).Put([]byte("legacy-running"), []byte(tes.State_RUNNING.String())); err != nil {
			return err
		}
		return tx.DeleteBucket(ExecutionClaims)
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Init(); err != nil {
		t.Fatal(err)
	}
	if claimed, err := db.ClaimExecution(ctx, "legacy-running"); err != nil || claimed {
		t.Fatalf("legacy running claim = %t, %v; want false, nil", claimed, err)
	}
	assertBoltClaimState(t, db, ctx, "legacy-running", tes.State_RUNNING, true)
}

func assertBoltClaimState(t *testing.T, db *BoltDB, ctx context.Context, id string, state tes.State, marker bool) {
	t.Helper()
	task, err := db.GetTask(ctx, &tes.GetTaskRequest{Id: id})
	if err != nil {
		t.Fatal(err)
	}
	if task.State != state {
		t.Fatalf("task %q state = %s, want %s", id, task.State, state)
	}
	if err := db.db.View(func(tx *bolt.Tx) error {
		got := tx.Bucket(ExecutionClaims).Get([]byte(id)) != nil
		if got != marker {
			return fmt.Errorf("task %q execution marker = %t, want %t", id, got, marker)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
