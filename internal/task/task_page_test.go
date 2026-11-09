package task

import (
	"context"
	"strings"
	"testing"

	"github.com/calypr/funnel/api/tes"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestTaskPageBudgetAndCursor(t *testing.T) {
	first := &tes.Task{Id: "first", Name: strings.Repeat("x", 100)}
	second := &tes.Task{Id: "second", Name: strings.Repeat("y", 100)}
	limit := proto.Size(&tes.ListTasksResponse{Tasks: []*tes.Task{first}, NextPageToken: "first"})
	page := NewTaskPage(WithTaskPageBytes(context.Background(), limit))
	if ok, err := page.Add(first, "first"); !ok || err != nil {
		t.Fatalf("first: %v %v", ok, err)
	}
	if ok, err := page.Add(second, "second"); ok || err != nil {
		t.Fatalf("second: %v %v", ok, err)
	}
	result := page.Finish(true)
	if len(result.Tasks) != 1 || result.NextPageToken != "first" || proto.Size(result) > limit {
		t.Fatalf("bad page: %v", result)
	}
}
func TestTaskPageAccountsForWholeResponse(t *testing.T) {
	tasks := []*tes.Task{{Id: "a"}, {Id: "b"}, {Id: "c"}}
	token := strings.Repeat("t", 140)
	expected := &tes.ListTasksResponse{Tasks: tasks, NextPageToken: token}
	page := NewTaskPage(WithTaskPageBytes(context.Background(), proto.Size(expected)))
	for _, task := range tasks {
		if ok, err := page.Add(task, token); !ok || err != nil {
			t.Fatalf("add: %v %v", ok, err)
		}
	}
	if proto.Size(page.Finish(true)) != proto.Size(expected) {
		t.Fatal("size mismatch")
	}
}
func TestTaskPageOversizedTask(t *testing.T) {
	page := NewTaskPage(WithTaskPageBytes(context.Background(), 20))
	_, err := page.Add(&tes.Task{Id: "large", Name: strings.Repeat("x", 100)}, "large")
	if status.Code(err) != codes.ResourceExhausted || !strings.Contains(err.Error(), "MINIMAL") {
		t.Fatalf("got %v", err)
	}
}
