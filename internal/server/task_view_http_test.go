package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/calypr/funnel/internal/server/taskservice"

	"github.com/calypr/funnel/api/tes"
)

type responseViewReader struct {
	task *tes.Task
}

func (r *responseViewReader) GetTask(context.Context, *tes.GetTaskRequest) (*tes.Task, error) {
	return r.task, nil
}

func (r *responseViewReader) ListTasks(context.Context, *tes.ListTasksRequest) (*tes.ListTasksResponse, error) {
	return &tes.ListTasksResponse{Tasks: []*tes.Task{r.task}, NextPageToken: "next"}, nil
}

func (*responseViewReader) Close() {}

func TestServeTaskViewsThroughGRPCGateway(t *testing.T) {
	rpcAddress := freeTCPAddress(t)
	httpPort := strconv.Itoa(tcpPort(t))
	s := newTestServer(rpcAddress, httpPort)
	s.Tasks = &taskservice.Service{Read: &responseViewReader{task: &tes.Task{
		Id: "task-1", State: tes.Running, Name: "example",
		Inputs: []*tes.Input{{Content: "input secret"}},
		Logs:   []*tes.TaskLog{{Logs: []*tes.ExecutorLog{{Stdout: "output secret"}}}},
	}}, Log: s.Log}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{}}
	go func() { done <- s.Serve(ctx) }()
	t.Cleanup(func() {
		client.CloseIdleConnections()
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("server stopped: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("server did not stop")
		}
	})
	base := "http://127.0.0.1:" + httpPort
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := client.Get(base + "/healthz")
		if err == nil {
			_, err := io.Copy(io.Discard, resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			err = resp.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("server did not start: %v", err)
		}
		time.Sleep(25 * time.Millisecond)
	}
	for _, tc := range []struct {
		query        string
		status       int
		want, absent string
	}{
		{"", 200, `"state":"RUNNING"`, `"name"`},
		{"?view=bAsIc", 200, `"name":"example"`, `"input secret"`},
		{"?view=full", 200, `"output secret"`, ""},
		{"?view=HUGE", 400, `invalid view`, ""},
		{"?view=FULL&view=BASIC", 400, `view`, ""},
	} {
		t.Run(tc.query, func(t *testing.T) {
			resp, err := client.Get(base + "/v1/tasks/task-1" + tc.query)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close() //nolint:errcheck // Read-only HTTP response cleanup cannot change the test assertions.
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			var compact bytes.Buffer
			if err := json.Compact(&compact, body); err != nil {
				t.Fatalf("invalid JSON %q: %v", body, err)
			}
			if resp.StatusCode != tc.status || !strings.Contains(compact.String(), tc.want) || (tc.absent != "" && strings.Contains(compact.String(), tc.absent)) {
				t.Fatalf("status=%d body=%s", resp.StatusCode, body)
			}
		})
	}
}
