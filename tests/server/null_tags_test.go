package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/ohsu-comp-bio/funnel/tests"
)

func TestCreateTaskWithNullTagValue(t *testing.T) {
	tests.SetLogOutput(log, t)

	payload := `{
		"name": "task-with-null-tag",
		"executors": [
			{
				"image": "alpine",
				"command": ["echo", "hello"]
			}
		],
		"tags": {
			"workflow_id": "wf-1",
			"parent_workflow_id": null
		}
	}`

	resp, err := http.Post(fun.Conf.Server.HTTPAddress()+"/v1/tasks", "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("failed to create task: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed reading create response body: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200 from create, got %d. body=%s", resp.StatusCode, string(body))
	}

	var createResp struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &createResp); err != nil {
		t.Fatalf("failed to decode create response: %v. body=%s", err, string(body))
	}
	if createResp.ID == "" {
		t.Fatalf("expected created task id, got empty. body=%s", string(body))
	}

	task := fun.Get(createResp.ID)
	if got := task.Tags["workflow_id"]; got != "wf-1" {
		t.Fatalf("expected workflow_id tag value wf-1, got %q", got)
	}
	if _, exists := task.Tags["parent_workflow_id"]; exists {
		t.Fatalf("expected null tag key parent_workflow_id to be removed")
	}
}

func TestCreateTaskWithAllNullTagValues(t *testing.T) {
	tests.SetLogOutput(log, t)

	payload := `{
		"name": "task-with-all-null-tags",
		"executors": [
			{
				"image": "alpine",
				"command": ["echo", "hello"]
			}
		],
		"tags": {
			"parent_workflow_id": null,
			"optional": null
		}
	}`

	resp, err := http.Post(fun.Conf.Server.HTTPAddress()+"/v1/tasks", "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("failed to create task: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed reading create response body: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200 from create, got %d. body=%s", resp.StatusCode, string(body))
	}

	var createResp struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &createResp); err != nil {
		t.Fatalf("failed to decode create response: %v. body=%s", err, string(body))
	}
	if createResp.ID == "" {
		t.Fatalf("expected created task id, got empty. body=%s", string(body))
	}

	task := fun.Get(createResp.ID)
	if len(task.Tags) != 0 {
		t.Fatalf("expected tags map to be empty after removing all null tag values, got %#v", task.Tags)
	}
}
