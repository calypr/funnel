package mongodb

import (
	"context"
	"strings"
	"testing"

	"github.com/calypr/funnel/tes"
	"github.com/calypr/funnel/tests/mongotest"
	"go.mongodb.org/mongo-driver/v2/bson"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestMongoListByteBudget(t *testing.T) {
	db := newMongoTestDB(t, mongotest.Start(t), "list_budget")
	for _, id := range []string{"a", "b", "c"} {
		_, err := db.tasks().InsertOne(t.Context(), bson.M{"id": id, "name": strings.Repeat("x", 150), "creationtime": id, "state": tes.State_COMPLETE})
		if err != nil {
			t.Fatal(err)
		}
	}
	ctx := tes.WithTaskPageBytes(context.Background(), 350)
	token := ""
	for _, want := range []string{"c", "b", "a"} {
		page, err := db.ListTasks(ctx, &tes.ListTasksRequest{View: "BASIC", PageSize: 3, PageToken: token})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Tasks) != 1 || page.Tasks[0].Id != want {
			t.Fatalf("expected %s got %v", want, page)
		}
		token = page.NextPageToken
	}
	if token != "" {
		t.Fatalf("unexpected final token %q", token)
	}
	_, err := db.ListTasks(tes.WithTaskPageBytes(context.Background(), 20), &tes.ListTasksRequest{View: "BASIC"})
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("got %v", err)
	}
}

func cursorID(t *testing.T, token string) string {
	t.Helper()
	cursor, _, err := tes.DecodeTaskCursor(token)
	if err != nil {
		t.Fatal(err)
	}
	return cursor.ID
}
