package core

import (
	"context"
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

func TestRootStreamOutputsDocker(t *testing.T) {
	if os.Getenv("FUNNEL_TEST_DOCKER_INTEGRATION") != "1" {
		t.Skip("set FUNNEL_TEST_DOCKER_INTEGRATION=1 to run Docker integration tests")
	}
	c := tests.DefaultConfig()
	c.Database = "boltdb"
	c.Compute = "local"
	f := tests.NewFunnel(c)
	f.StartServer()
	t.Cleanup(f.Cleanup)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	for _, fields := range []string{
		`"outputs":[{"path":"/result.json","url":"file:///unused","type":"FILE"}]`,
		`"outputs":[{"path":"/","url":"file:///unused","type":"DIRECTORY"}]`,
		`"volumes":["/"]`,
		`"inputs":[{"path":"/input.txt","content":"input","type":"FILE"}]`,
	} {
		body := `{"executors":[{"image":"alpine:latest","command":["echo","should not run"]}],` + fields + `}`
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
		if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(data), "mount") {
			t.Fatalf("root mapping response = %d %s, want actionable HTTP 400", resp.StatusCode, data)
		}
	}
	listed, err := f.HTTP.ListTasks(ctx, &tes.ListTasksRequest{})
	if err != nil || len(listed.GetTasks()) != 0 {
		t.Fatalf("rejected tasks persisted: response %v, error %v", listed, err)
	}
	paths := map[string]string{}
	var outputs []*tes.Output
	for _, name := range []string{"captured-out", "captured-err", "outputs/copied"} {
		p, err := filepath.Abs(filepath.Join(f.StorageDir, name))
		if err != nil {
			t.Fatal(err)
		}
		paths[name] = p
		outputs = append(outputs, &tes.Output{Path: "/" + name, Url: (&url.URL{Scheme: "file", Path: p}).String(), Type: tes.File.Enum()})
	}
	task := &tes.Task{
		Inputs:  []*tes.Input{{Path: "/mnt/command", Content: "printf 'hello\\n'; printf 'warning\\n' >&2", Type: tes.File}},
		Outputs: outputs,
		Executors: []*tes.Executor{
			{Image: "alpine:latest", Command: []string{"sh", "/mnt/command"}, Stdout: "/captured-out", Stderr: "/captured-err"},
			{Image: "alpine:latest", Command: []string{"sh", "-c", "cat /captured-out /captured-err > /outputs/copied"}},
		},
	}
	created, err := f.HTTP.CreateTask(ctx, task)
	if err != nil {
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
			if got.State != tes.State_COMPLETE {
				t.Fatalf("task state = %s, logs = %v", got.State, got.Logs)
			}
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}
	for name, want := range map[string]string{"captured-out": "hello\n", "captured-err": "warning\n", "outputs/copied": "hello\nwarning\n"} {
		got, err := os.ReadFile(paths[name])
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Fatalf("output %s = %q, want %q", name, got, want)
		}
	}
}
