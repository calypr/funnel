package mongodb

import (
	"context"
	"errors"
	"testing"

	"github.com/calypr/funnel/tes"
	"github.com/calypr/funnel/tests/mongotest"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestGetTaskMalformedDocumentIsNotNotFound(t *testing.T) {
	db := newMongoTestDB(t, mongotest.Start(t), "get_task_malformed")
	ctx := context.Background()
	if _, err := db.tasks().InsertOne(ctx, bson.M{
		"id":        "malformed",
		"owner":     "owner",
		"resources": "malformed resource document",
	}); err != nil {
		t.Fatalf("insert malformed task: %v", err)
	}

	_, err := db.GetTask(ctx, &tes.GetTaskRequest{Id: "malformed"})
	if err == nil {
		t.Fatal("GetTask returned no error for malformed task document")
	}
	if errors.Is(err, tes.ErrNotFound) {
		t.Fatalf("GetTask error = %v, want BSON decode error rather than ErrNotFound", err)
	}
}

func TestGetTaskMissingDocumentIsNotFound(t *testing.T) {
	db := newMongoTestDB(t, mongotest.Start(t), "get_task_missing")

	_, err := db.GetTask(context.Background(), &tes.GetTaskRequest{Id: "missing"})
	if !errors.Is(err, tes.ErrNotFound) {
		t.Fatalf("GetTask error = %v, want ErrNotFound", err)
	}
}
