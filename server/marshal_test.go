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
