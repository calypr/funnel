package postgres

import (
	"strings"
	"testing"

	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/tes"
	"github.com/testcontainers/testcontainers-go"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestPostgresListByteBudget(t *testing.T) {
	db, container := getTestPostgres(t)
	defer testcontainers.TerminateContainer(container)
	defer db.Close()
	ctx := getUserContext()
	for _, id := range []string{"a", "b", "c"} {
		task := &tes.Task{Id: id, Name: strings.Repeat("x", 150), State: tes.State_COMPLETE, CreationTime: "2026-01-01T00:00:00Z"}
		if err := db.WriteEvent(ctx, events.NewTaskCreated(task)); err != nil {
			t.Fatal(err)
		}
	}
	ctx = tes.WithTaskPageBytes(ctx, 350)
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
	_, err := db.ListTasks(tes.WithTaskPageBytes(ctx, 20), &tes.ListTasksRequest{View: "BASIC"})
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
