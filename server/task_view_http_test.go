package server

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/calypr/funnel/tes"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
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

func TestTaskViewHTTPResponseShapes(t *testing.T) {
	reader := &responseViewReader{task: &tes.Task{
		Id: "task-1", State: tes.Running, Name: "example",
		Inputs:    []*tes.Input{{Path: "/input", Content: "input secret"}},
		Executors: []*tes.Executor{{Image: "alpine", Stdout: "/output", Stderr: "/error"}},
		Logs:      []*tes.TaskLog{{Logs: []*tes.ExecutorLog{{Stdout: "output secret", Stderr: "error secret"}}}},
	}}
	mux := runtime.NewServeMux(runtime.WithMarshalerOption(runtime.MIMEWildcard, NewMarshaler()), runtime.WithForwardResponseRewriter(rewriteTaskViewResponse))
	if err := tes.RegisterTaskServiceHandlerServer(context.Background(), mux, &TaskService{Read: reader}); err != nil {
		t.Fatal(err)
	}
	handler := withTaskView(mux)
	for _, route := range []string{"/tasks", "/v1/tasks", "/ga4gh/tes/v1/tasks"} {
		for _, suffix := range []string{"", "/task-1"} {
			for _, view := range []string{"", "minimal", "basic", "full"} {
				t.Run(route+suffix+"/"+view, func(t *testing.T) {
					res := httptest.NewRecorder()
					handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, route+suffix+"?view="+view, nil))
					if res.Code != http.StatusOK {
						t.Fatalf("status %d: %s", res.Code, res.Body.String())
					}
					var data map[string]any
					if err := json.Unmarshal(res.Body.Bytes(), &data); err != nil {
						t.Fatal(err)
					}
					if suffix == "" {
						if data["next_page_token"] != "next" {
							t.Fatalf("missing page token: %v", data)
						}
						tasks := data["tasks"].([]any)
						data = tasks[0].(map[string]any)
					}
					if view == "" || view == "minimal" {
						if len(data) != 2 || data["id"] != "task-1" || data["state"] != "RUNNING" {
							t.Fatalf("minimal view: %v", data)
						}
						return
					}
					if data["name"] != "example" {
						t.Fatalf("missing basic fields: %v", data)
					}
					input := data["inputs"].([]any)[0].(map[string]any)
					log := data["logs"].([]any)[0].(map[string]any)
					executor := log["logs"].([]any)[0].(map[string]any)
					if view == "basic" {
						if executor["exit_code"] != float64(0) || input["type"] != "FILE" {
							t.Fatalf("basic lost zero-valued fields: %v %v", executor, input)
						}
						config := data["executors"].([]any)[0].(map[string]any)
						if config["stdout"] != "/output" || config["stderr"] != "/error" {
							t.Fatalf("basic lost executor output paths: %v", config)
						}
						if _, ok := input["content"]; ok {
							t.Fatalf("basic includes input content: %v", input)
						}
						if _, ok := log["system_logs"]; ok {
							t.Fatalf("basic includes system logs: %v", log)
						}
						if _, ok := executor["stdout"]; ok {
							t.Fatalf("basic includes stdout: %v", executor)
						}
						if _, ok := executor["stderr"]; ok {
							t.Fatalf("basic includes stderr: %v", executor)
						}
					} else if input["content"] != "input secret" || executor["stdout"] != "output secret" || executor["stderr"] != "error secret" {
						t.Fatalf("full view lost data: %v", data)
					}
				})
			}
		}
	}
}

func TestTaskViewHTTPResponseErrors(t *testing.T) {
	reader := &responseViewReader{task: &tes.Task{Id: "task-1", State: tes.Running}}
	mux := runtime.NewServeMux(
		runtime.WithMarshalerOption(runtime.MIMEWildcard, NewMarshaler()),
		runtime.WithForwardResponseRewriter(rewriteTaskViewResponse),
		runtime.WithErrorHandler(customErrorHandler),
	)
	if err := tes.RegisterTaskServiceHandlerServer(context.Background(), mux, &TaskService{Read: reader}); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"?view=HUGE", "?view=BASIC&view=MINIMAL"} {
		t.Run(query, func(t *testing.T) {
			res := httptest.NewRecorder()
			withTaskView(mux).ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/v1/tasks/task-1"+query, nil))
			if res.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", res.Code, res.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil || body["error"] == "" {
				t.Fatalf("missing JSON error: %s (%v)", res.Body.String(), err)
			}
		})
	}
}

func TestServeTaskViewsThroughGRPCGateway(t *testing.T) {
	rpcAddress := freeTCPAddress(t)
	httpPort := strconv.Itoa(tcpPort(t))
	s := newTestServer(rpcAddress, httpPort)
	s.Tasks = &TaskService{Read: &responseViewReader{task: &tes.Task{
		Id: "task-1", State: tes.Running, Name: "example",
		Inputs: []*tes.Input{{Content: "input secret"}},
		Logs:   []*tes.TaskLog{{Logs: []*tes.ExecutorLog{{Stdout: "output secret"}}}},
	}}, Log: s.Log}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx) }()
	t.Cleanup(func() {
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
	client := &http.Client{Timeout: time.Second}
	base := "http://127.0.0.1:" + httpPort
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := client.Get(base + "/healthz")
		if err == nil {
			resp.Body.Close()
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
			defer resp.Body.Close()
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
