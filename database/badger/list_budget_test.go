package badger

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/tes"
)

func TestListTasksStopsAtResponseBudgetWithoutSkipping(t *testing.T) {
	db, err := NewBadger(&config.Badger{Path: filepath.Join(t.TempDir(), "db")})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, id := range []string{"task-c", "task-b", "task-a"} {
		task := &tes.Task{Id: id, State: tes.State_COMPLETE, CreationTime: "2026-01-01T00:00:00Z", Outputs: []*tes.Output{{Path: "/out", Type: fileType(tes.FileType_FILE)}}}
		if err := db.WriteEvent(context.Background(), events.NewTaskCreated(task)); err != nil {
			t.Fatal(err)
		}
		if err := db.WriteEvent(context.Background(), events.NewOutputs(id, 0, []*tes.OutputFileLog{{Path: "/out", Url: "s3://bucket/" + id, SizeBytes: "1000"}})); err != nil {
			t.Fatal(err)
		}
	}
	ctx := tes.WithTaskPageBytes(context.Background(), 110)
	first, err := db.ListTasks(ctx, &tes.ListTasksRequest{View: tes.View_BASIC.String(), PageSize: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Tasks) != 1 || first.NextPageToken != "task-c" {
		t.Fatalf("first page = %+v", first)
	}
	second, err := db.ListTasks(ctx, &tes.ListTasksRequest{View: tes.View_BASIC.String(), PageSize: 3, PageToken: first.NextPageToken})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Tasks) != 1 || second.Tasks[0].Id != "task-b" || second.NextPageToken != "task-b" {
		t.Fatalf("second page = %+v", second)
	}
	third, err := db.ListTasks(ctx, &tes.ListTasksRequest{View: tes.View_BASIC.String(), PageSize: 3, PageToken: second.NextPageToken})
	if err != nil {
		t.Fatal(err)
	}
	if len(third.Tasks) != 1 || third.Tasks[0].Id != "task-a" || third.NextPageToken != "" {
		t.Fatalf("third page = %+v", third)
	}
}

func fileType(value tes.FileType) *tes.FileType { return &value }
