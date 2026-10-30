package mongodb

import (
	"context"
	"strings"
	"testing"

	"github.com/calypr/funnel/tes"
	"github.com/calypr/funnel/tests/mongotest"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestListTasksStopsAtResponseBudgetAndResumes(t *testing.T) {
	db := newMongoTestDB(t, mongotest.Start(t), "list_response_budget")
	ctx := tes.WithTaskPageBytes(context.Background(), 700)
	for _, task := range []*tes.Task{
		{Id: "a", CreationTime: "2026-01-01T00:00:00Z", State: tes.State_COMPLETE, Logs: []*tes.TaskLog{{Outputs: []*tes.OutputFileLog{{Path: "/a", Url: strings.Repeat("a", 450)}}}}},
		{Id: "b", CreationTime: "2026-01-02T00:00:00Z", State: tes.State_COMPLETE, Logs: []*tes.TaskLog{{Outputs: []*tes.OutputFileLog{{Path: "/b", Url: strings.Repeat("b", 450)}}}}},
	} {
		if _, err := db.tasks().InsertOne(ctx, taskRecord{Task: task, Owner: ""}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := db.ListTasks(ctx, &tes.ListTasksRequest{View: tes.View_BASIC.String(), PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Tasks) != 1 || first.Tasks[0].Id != "b" || cursorID(t, first.NextPageToken) != "b" {
		t.Fatalf("first page = %+v", first)
	}
	second, err := db.ListTasks(ctx, &tes.ListTasksRequest{View: tes.View_BASIC.String(), PageSize: 10, PageToken: first.NextPageToken})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Tasks) != 1 || second.Tasks[0].Id != "a" || second.NextPageToken != "" {
		t.Fatalf("second page = %+v", second)
	}
}

func TestListTasksOversizedFirstRecordReturnsResourceExhausted(t *testing.T) {
	db := newMongoTestDB(t, mongotest.Start(t), "list_oversized_record")
	task := &tes.Task{Id: "huge", CreationTime: "2026-01-01T00:00:00Z", State: tes.State_COMPLETE, Logs: []*tes.TaskLog{{Outputs: []*tes.OutputFileLog{{Path: "/huge", Url: strings.Repeat("x", 4096)}}}}}
	if _, err := db.tasks().InsertOne(context.Background(), taskRecord{Task: task}); err != nil {
		t.Fatal(err)
	}
	_, err := db.ListTasks(tes.WithTaskPageBytes(context.Background(), 512), &tes.ListTasksRequest{View: tes.View_BASIC.String(), PageSize: 10})
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("error = %v, want ResourceExhausted", err)
	}
	if !strings.Contains(err.Error(), "request MINIMAL view") {
		t.Fatalf("error lacks mitigation: %v", err)
	}
}
