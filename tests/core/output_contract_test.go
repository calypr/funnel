package core

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/calypr/funnel/tes"
	"github.com/calypr/funnel/tests"
)

func TestOutputContractDocker(t *testing.T) {
	if os.Getenv("FUNNEL_TEST_DOCKER_INTEGRATION") != "1" {
		t.Skip("set FUNNEL_TEST_DOCKER_INTEGRATION=1 to run Docker integration tests")
	}
	c := tests.DefaultConfig()
	c.Database = "boltdb"
	c.Compute = "local"
	f := tests.NewFunnel(c)
	f.StartServer()
	t.Cleanup(f.Cleanup)

	for _, tt := range []struct {
		name        string
		typeField   string
		path        string
		command     string
		state       tes.State
		child       string
		warningPath string
	}{
		{"omitted directory", "", "/outputs/result", "mkdir -p /outputs/result; echo hello > /outputs/result/child.txt", tes.State_COMPLETE, "child.txt", ""},
		{"omitted file", "", "/outputs/result", "echo hello > /outputs/result", tes.State_COMPLETE, "", ""},
		{"explicit file", `,"type":"FILE"`, "/outputs/result", "echo hello > /outputs/result", tes.State_COMPLETE, "", ""},
		{"explicit directory", `,"type":"DIRECTORY"`, "/outputs/result", "echo hello > /outputs/result/child.txt", tes.State_COMPLETE, "child.txt", ""},
		{"directory declared file", `,"type":"FILE"`, "/outputs/result", "mkdir -p /outputs/result; echo hello > /outputs/result/child.txt", tes.State_SYSTEM_ERROR, "", ""},
		{"file declared directory", `,"type":"DIRECTORY"`, "/outputs/result", "rm -rf /outputs/result; echo hello > /outputs/result", tes.State_SYSTEM_ERROR, "", ""},
		{"matched pattern", `,"type":"FILE"`, "/outputs/*.txt", "echo hello > /outputs/matched.txt", tes.State_COMPLETE, "matched.txt", ""},
		{"unmatched pattern", "", "/outputs/missing-*.txt", "echo hello > /outputs/other.txt", tes.State_COMPLETE, "", "/outputs/missing-*.txt"},
		{"pattern beneath file", "", "/outputs/result/*", "rm -rf /outputs/result; echo hello > /outputs/result", tes.State_COMPLETE, "", "/outputs/result/*"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			dest, err := filepath.Abs(filepath.Join(f.StorageDir, strings.ReplaceAll(tt.name, " ", "-")))
			if err != nil {
				t.Fatal(err)
			}
			outputURL, err := json.Marshal((&url.URL{Scheme: "file", Path: dest}).String())
			if err != nil {
				t.Fatal(err)
			}
			command, err := json.Marshal(tt.command)
			if err != nil {
				t.Fatal(err)
			}
			body := `{"executors":[{"image":"alpine:latest","command":["sh","-c",` + string(command) + `]}],` +
				`"volumes":["/outputs"],"outputs":[{"path":"` + tt.path + `","path_prefix":"/outputs","url":` + string(outputURL) + tt.typeField + `}]}`
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Server.HTTPAddress()+"/v1/tasks", strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			data, readErr := io.ReadAll(resp.Body)
			closeErr := resp.Body.Close()
			if readErr != nil || closeErr != nil {
				t.Fatalf("read response: %v; close response: %v", readErr, closeErr)
			}
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("create task: HTTP %d %s", resp.StatusCode, data)
			}
			var created tes.CreateTaskResponse
			if err := json.Unmarshal(data, &created); err != nil {
				t.Fatal(err)
			}
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			for {
				got, err := f.HTTP.GetTask(ctx, &tes.GetTaskRequest{Id: created.Id, View: "FULL"})
				if err != nil {
					t.Fatal(err)
				}
				if tes.TerminalState(got.State) {
					if got.State != tt.state {
						t.Fatalf("state = %s, want %s; logs = %v", got.State, tt.state, got.Logs)
					}
					if len(got.Logs) != 1 || len(got.Logs[0].Logs) != 1 || got.Logs[0].Logs[0].ExitCode != 0 {
						t.Fatalf("executor did not succeed: %v", got.Logs)
					}
					if tt.warningPath != "" {
						warningFound := false
						for _, systemLog := range got.Logs[0].SystemLogs {
							if strings.Contains(systemLog, "output pattern matched no files") && strings.Contains(systemLog, "path='"+tt.warningPath+"'") {
								warningFound = true
								break
							}
						}
						if !warningFound {
							t.Fatalf("missing persisted unmatched output warning for %q: %v", tt.warningPath, got.Logs[0].SystemLogs)
						}
					}
					if (got.Outputs[0].Type != nil) != (tt.typeField != "") {
						t.Fatalf("output type presence changed during task storage: %v", got.Outputs[0])
					}
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-ticker.C:
				}
			}
			if tt.state == tes.State_SYSTEM_ERROR || tt.warningPath != "" {
				if _, err := os.Stat(dest); !os.IsNotExist(err) {
					t.Fatalf("failed task published output: stat error = %v", err)
				}
				return
			}
			got, err := os.ReadFile(filepath.Join(dest, tt.child))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != "hello\n" {
				t.Fatalf("output = %q, want hello followed by newline", got)
			}
		})
	}
}
