package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/calypr/funnel/internal/server/taskservice"

	"github.com/calypr/funnel/api/tes"
	"github.com/calypr/funnel/internal/tesrpc"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
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
	mux := runtime.NewServeMux(runtime.WithMarshalerOption(runtime.MIMEWildcard, NewMarshaler()), runtime.WithForwardResponseRewriter(RewriteTaskViewResponse))
	if err := tesrpc.RegisterTaskServiceHandlerServer(context.Background(), mux, &taskservice.Service{Read: reader}); err != nil {
		t.Fatal(err)
	}
	handler := WithTaskView(mux)
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
		runtime.WithForwardResponseRewriter(RewriteTaskViewResponse),
		runtime.WithErrorHandler(ErrorHandler),
	)
	if err := tesrpc.RegisterTaskServiceHandlerServer(context.Background(), mux, &taskservice.Service{Read: reader}); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"?view=HUGE", "?view=BASIC&view=MINIMAL"} {
		t.Run(query, func(t *testing.T) {
			res := httptest.NewRecorder()
			WithTaskView(mux).ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/v1/tasks/task-1"+query, nil))
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
