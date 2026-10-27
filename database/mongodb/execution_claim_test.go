package mongodb

import (
	"context"
	"sync"
	"testing"

	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/server"
	"github.com/calypr/funnel/tes"
	"github.com/calypr/funnel/tests/mongotest"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func executionClaimContext(owner string) context.Context {
	return context.WithValue(context.Background(), server.UserInfoKey, &server.UserInfo{Username: owner})
}

func TestMongoClaimExecutionIsDurableAndOneShot(t *testing.T) {
	db := newMongoTestDB(t, mongotest.Start(t), "execution_claims")
	if err := db.Init(); err != nil {
		t.Fatal(err)
	}
	ctx := executionClaimContext("owner")
	for _, id := range []string{"concurrent", "rollback", "private", "legacy-terminal"} {
		if err := db.insertTask(ctx, &tes.Task{Id: id, State: tes.State_QUEUED}); err != nil {
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
	assertMongoClaimState(t, db, ctx, "concurrent", tes.State_RUNNING, true)
	if claimed, err := db.ClaimExecution(ctx, "concurrent"); err != nil || claimed {
		t.Fatalf("retry claim = %t, %v; want false, nil", claimed, err)
	}
	if err := db.WriteEvent(ctx, events.NewState("concurrent", tes.State_COMPLETE)); err != nil {
		t.Fatal(err)
	}
	assertMongoClaimState(t, db, ctx, "concurrent", tes.State_COMPLETE, true)
	if err := db.insertTask(ctx, &tes.Task{Id: "concurrent"}); err == nil {
		t.Fatal("task creation reused an existing execution ID")
	}
	if claimed, err := db.ClaimExecution(ctx, "concurrent"); err != nil || claimed {
		t.Fatalf("claim after terminal state = %t, %v; want false, nil", claimed, err)
	}

	if err := db.database.RunCommand(ctx, bson.D{
		{Key: "collMod", Value: "tasks"},
		{Key: "validator", Value: bson.M{"state": bson.M{"$ne": tes.State_RUNNING}}},
	}).Err(); err != nil {
		t.Fatal(err)
	}
	if claimed, err := db.ClaimExecution(ctx, "rollback"); err == nil || claimed {
		t.Fatalf("claim with rejecting validator = %t, %v; want false, error", claimed, err)
	}
	assertMongoClaimState(t, db, ctx, "rollback", tes.State_QUEUED, false)
	if err := db.database.RunCommand(ctx, bson.D{
		{Key: "collMod", Value: "tasks"},
		{Key: "validator", Value: bson.M{}},
	}).Err(); err != nil {
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
	assertMongoClaimState(t, db, ctx, "private", tes.State_QUEUED, false)

	if _, err := db.tasks().UpdateOne(ctx, bson.M{"id": "legacy-terminal"}, bson.M{
		"$set":   bson.M{"state": tes.State_SYSTEM_ERROR},
		"$unset": bson.M{"execution_claimed": ""},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.collection("scheduler_migrations").DeleteOne(ctx, bson.M{"_id": "execution-claims-v1"}); err != nil {
		t.Fatal(err)
	}
	if err := db.migrateExecutionClaims(ctx); err != nil {
		t.Fatal(err)
	}
	if claimed, err := db.ClaimExecution(ctx, "legacy-terminal"); err != nil || claimed {
		t.Fatalf("legacy terminal claim = %t, %v; want false, nil", claimed, err)
	}
	assertMongoClaimState(t, db, ctx, "legacy-terminal", tes.State_SYSTEM_ERROR, true)
}

func assertMongoClaimState(t *testing.T, db *MongoDB, ctx context.Context, id string, state tes.State, marker bool) {
	t.Helper()
	var stored taskRecord
	if err := db.tasks().FindOne(ctx, bson.M{"id": id}).Decode(&stored); err != nil {
		t.Fatal(err)
	}
	if stored.Task.State != state {
		t.Fatalf("task %q state = %s, want %s", id, stored.Task.State, state)
	}
	if stored.ExecutionClaimed != marker {
		t.Fatalf("task %q execution marker = %t, want %t", id, stored.ExecutionClaimed, marker)
	}
}
