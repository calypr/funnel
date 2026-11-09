package mongodb

import (
	"context"
	"strings"
	"testing"

	"github.com/calypr/funnel/tes"
	"github.com/calypr/funnel/tests/mongotest"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestListTasksCursorFollowsCreationOrder(t *testing.T) {
	db := newMongoTestDB(t, mongotest.StartStandalone(t), "review_order")
	for _, task := range []*tes.Task{
		{Id: "a", CreationTime: "2026-01-02T00:00:00Z", Name: strings.Repeat("x", 150)},
		{Id: "b", CreationTime: "2026-01-01T00:00:00Z", Name: strings.Repeat("x", 150)},
	} {
		if _, err := db.tasks().InsertOne(context.Background(), taskRecord{Task: task}); err != nil {
			t.Fatal(err)
		}
	}
	baseline, err := db.ListTasks(context.Background(), &tes.ListTasksRequest{View: "BASIC", PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("unbounded page IDs: %v %v", baseline.Tasks[0].Id, baseline.Tasks[1].Id)
	ctx := tes.WithTaskPageBytes(context.Background(), 350)
	first, err := db.ListTasks(ctx, &tes.ListTasksRequest{View: "BASIC", PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("bounded first IDs: %v token=%q", first.Tasks[0].Id, first.NextPageToken)
	second, err := db.ListTasks(ctx, &tes.ListTasksRequest{View: "BASIC", PageSize: 10, PageToken: first.NextPageToken})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Tasks) != 1 || second.Tasks[0].Id != "b" {
		t.Fatalf("second page = %v; want task b (total retrieval must match unbounded page)", second)
	}
}

func TestListTasksCursorTiesAndDeletion(t *testing.T) {
	db := newMongoTestDB(t, mongotest.StartStandalone(t), "cursor_ties")
	for _, id := range []string{"a", "b", "c"} {
		if _, err := db.tasks().InsertOne(t.Context(), taskRecord{Task: &tes.Task{Id: id, CreationTime: "2026-01-01T00:00:00Z"}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, view := range []string{"BASIC", "MINIMAL"} {
		first, err := db.ListTasks(t.Context(), &tes.ListTasksRequest{View: view, PageSize: 1})
		if err != nil {
			t.Fatal(err)
		}
		if first.Tasks[0].Id != "c" {
			t.Fatalf("tie ordering: %v", first)
		}
		if view == "MINIMAL" && first.Tasks[0].CreationTime != "" {
			t.Fatal("MINIMAL leaked timestamp")
		}
		for _, token := range []string{first.NextPageToken, "c"} {
			next, err := db.ListTasks(t.Context(), &tes.ListTasksRequest{View: view, PageSize: 1, PageToken: token})
			if err != nil {
				t.Fatal(err)
			}
			if next.Tasks[0].Id != "b" {
				t.Fatalf("cursor skipped tied task: %v", next)
			}
		}
	}
	first, err := db.ListTasks(t.Context(), &tes.ListTasksRequest{View: "MINIMAL", PageSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.tasks().DeleteOne(t.Context(), bson.M{"id": "c"}); err != nil {
		t.Fatal(err)
	}
	next, err := db.ListTasks(t.Context(), &tes.ListTasksRequest{View: "MINIMAL", PageSize: 1, PageToken: first.NextPageToken})
	if err != nil {
		t.Fatal(err)
	}
	if next.Tasks[0].Id != "b" {
		t.Fatalf("deleted anchor skipped task: %v", next)
	}
}
