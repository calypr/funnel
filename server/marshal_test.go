package server

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ohsu-comp-bio/funnel/tes"
)

func TestNormalizeNilTagValues_RemovesNullTagValues(t *testing.T) {
	input := []byte(`{"tags":{"workflow_id":"wf-1","parent_workflow_id":null,"empty":""}}`)

	normalized, err := normalizeNilTagValues(input)
	if err != nil {
		t.Fatalf("normalizeNilTagValues returned error: %v", err)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(normalized, &payload); err != nil {
		t.Fatalf("failed to unmarshal normalized payload: %v", err)
	}

	rawTags, ok := payload["tags"]
	if !ok {
		t.Fatalf("expected tags object to exist")
	}

	tags, ok := rawTags.(map[string]interface{})
	if !ok {
		t.Fatalf("expected tags to be an object, got %T", rawTags)
	}

	if _, exists := tags["parent_workflow_id"]; exists {
		t.Fatalf("expected parent_workflow_id to be removed")
	}

	if got, ok := tags["workflow_id"].(string); !ok || got != "wf-1" {
		t.Fatalf("expected workflow_id=wf-1, got %v", tags["workflow_id"])
	}

	if got, ok := tags["empty"].(string); !ok || got != "" {
		t.Fatalf("expected empty tag to remain as empty string, got %v", tags["empty"])
	}
}

func TestNormalizeNilTagValues_RemovesTagsWhenAllValuesNull(t *testing.T) {
	input := []byte(`{"tags":{"parent_workflow_id":null}}`)

	normalized, err := normalizeNilTagValues(input)
	if err != nil {
		t.Fatalf("normalizeNilTagValues returned error: %v", err)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(normalized, &payload); err != nil {
		t.Fatalf("failed to unmarshal normalized payload: %v", err)
	}

	if _, ok := payload["tags"]; ok {
		t.Fatalf("expected tags field to be removed when all values are null")
	}
}

func TestCustomMarshalDecoder_TaskAcceptsNullTags(t *testing.T) {
	m := NewMarshaler()
	input := `{"name":"n1","tags":{"workflow_id":"wf-1","parent_workflow_id":null,"empty":""}}`

	var task tes.Task
	if err := m.NewDecoder(strings.NewReader(input)).Decode(&task); err != nil {
		t.Fatalf("decoder returned error: %v", err)
	}

	if task.Tags == nil {
		t.Fatalf("expected task tags map to be initialized")
	}

	if _, exists := task.Tags["parent_workflow_id"]; exists {
		t.Fatalf("expected null-valued tag to be removed")
	}

	if got := task.Tags["workflow_id"]; got != "wf-1" {
		t.Fatalf("expected workflow_id=wf-1, got %q", got)
	}

	if got := task.Tags["empty"]; got != "" {
		t.Fatalf("expected empty tag value to remain empty string, got %q", got)
	}
}

// A task with a CreationTime set but no logs (e.g. just after submission) must
// not panic when its view is detected. Regression test for an index-out-of-range
// panic at task.Logs[0] in DetectView.
func TestDetectView_EmptyLogsDoesNotPanic(t *testing.T) {
	c := NewMarshaler().(*CustomMarshal)

	task := &tes.Task{
		Id:           "task-1",
		CreationTime: "2026-06-12T00:00:00Z",
		// Logs intentionally empty.
	}

	view, err := c.DetectView(task)
	if err != nil {
		t.Fatalf("DetectView returned error: %v", err)
	}
	if view != tes.View_BASIC {
		t.Fatalf("expected View_BASIC for a task with empty logs, got %v", view)
	}
}

// MarshalList must not panic when the first task has a CreationTime but no logs.
// This is the path that crashed the GRPC gateway when listing freshly submitted
// tasks.
func TestMarshalList_FirstTaskWithEmptyLogs(t *testing.T) {
	c := NewMarshaler().(*CustomMarshal)

	list := &tes.ListTasksResponse{
		Tasks: []*tes.Task{
			{
				Id:           "task-1",
				State:        tes.State_INITIALIZING,
				CreationTime: "2026-06-12T00:00:00Z",
				// Logs intentionally empty.
			},
		},
	}

	out, err := c.MarshalList(list)
	if err != nil {
		t.Fatalf("MarshalList returned error: %v", err)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("failed to unmarshal MarshalList output: %v", err)
	}
	if _, ok := payload["tasks"]; !ok {
		t.Fatalf("expected tasks field in marshaled list output")
	}
}

func TestCustomMarshalDecoder_NonTaskPassthrough(t *testing.T) {
	m := NewMarshaler()
	input := []byte(`{"id":"task-123"}`)

	var req tes.CancelTaskRequest
	if err := m.NewDecoder(bytes.NewReader(input)).Decode(&req); err != nil {
		t.Fatalf("decoder returned error for non-task message: %v", err)
	}

	if req.Id != "task-123" {
		t.Fatalf("expected id=task-123, got %q", req.Id)
	}
}
