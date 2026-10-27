package task

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetIncludesDefaultValues(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"id":"task-1","logs":[{"logs":[{"exitCode":0}]}],"outputs":[{"path":"/output","type":"FILE"}]}`)
	}))
	defer server.Close()

	var output bytes.Buffer
	if err := Get(server.URL, []string{"task-1"}, "full", &output); err != nil {
		t.Fatal(err)
	}
	var result struct {
		ID   string `json:"id"`
		Logs []struct {
			Logs []map[string]any `json:"logs"`
		} `json:"logs"`
		Outputs []map[string]any `json:"outputs"`
	}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("invalid output %q: %v", output.String(), err)
	}
	if result.ID != "task-1" || len(result.Logs) != 1 || len(result.Logs[0].Logs) != 1 || len(result.Outputs) != 1 {
		t.Fatalf("unexpected task output: %s", output.String())
	}
	if exitCode, ok := result.Logs[0].Logs[0]["exitCode"]; !ok || exitCode != float64(0) {
		t.Errorf("exitCode = %v (present: %v), want 0", exitCode, ok)
	}
	if fileType := result.Outputs[0]["type"]; fileType != "FILE" {
		t.Errorf("output type = %v, want FILE", fileType)
	}
}
