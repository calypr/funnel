package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/calypr/funnel/tes"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
)

type recordingViewReader struct {
	views []string
}

func (r *recordingViewReader) GetTask(_ context.Context, req *tes.GetTaskRequest) (*tes.Task, error) {
	r.views = append(r.views, req.View)
	return &tes.Task{Id: req.Id, State: tes.Running}, nil
}
func (r *recordingViewReader) ListTasks(_ context.Context, req *tes.ListTasksRequest) (*tes.ListTasksResponse, error) {
	r.views = append(r.views, req.View)
	return &tes.ListTasksResponse{Tasks: []*tes.Task{{Id: "task-1", State: tes.Running}}}, nil
}
func (*recordingViewReader) Close() {}

func TestTaskViewHTTPValidation(t *testing.T) {
	reader := &recordingViewReader{}
	mux := runtime.NewServeMux(runtime.WithMarshalerOption(runtime.MIMEWildcard, NewMarshaler()))
	if err := tes.RegisterTaskServiceHandlerServer(context.Background(), mux, &TaskService{Read: reader}); err != nil {
		t.Fatal(err)
	}
	for _, route := range []string{"/tasks", "/v1/tasks", "/ga4gh/tes/v1/tasks"} {
		for _, suffix := range []string{"", "/task-1"} {
			for _, tc := range []struct {
				query, want string
				status      int
			}{
				{"", "MINIMAL", http.StatusOK},
				{"?view=mInImAl", "MINIMAL", http.StatusOK},
				{"?view=bAsIc", "BASIC", http.StatusOK},
				{"?view=full", "FULL", http.StatusOK},
				{"?view=HUGE", "", http.StatusBadRequest},
				{"?view=BASIC&view=MINIMAL", "", http.StatusBadRequest},
			} {
				t.Run(fmt.Sprintf("%s%s%s", route, suffix, tc.query), func(t *testing.T) {
					before := len(reader.views)
					response := httptest.NewRecorder()
					mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, route+suffix+tc.query, nil))
					if response.Code != tc.status {
						t.Fatalf("status = %d, want %d: %s", response.Code, tc.status, response.Body.String())
					}
					if tc.want == "" {
						if len(reader.views) != before {
							t.Fatal("invalid view reached the backend")
						}
					} else if len(reader.views) != before+1 || reader.views[before] != tc.want {
						t.Fatalf("backend views = %v, want %q", reader.views[before:], tc.want)
					}
				})
			}
		}
	}
}

func TestTaskViewNormalizationDoesNotMutateRequests(t *testing.T) {
	reader := &recordingViewReader{}
	service := &TaskService{Read: reader}
	get := &tes.GetTaskRequest{Id: "task-1", View: "full"}
	if _, err := service.GetTask(context.Background(), get); err != nil {
		t.Fatal(err)
	}
	list := &tes.ListTasksRequest{View: "basic", PageSize: 5, TagKey: []string{"key"}, TagValue: []string{"value"}}
	if _, err := service.ListTasks(context.Background(), list); err != nil {
		t.Fatal(err)
	}
	if get.View != "full" || list.View != "basic" {
		t.Fatalf("original requests mutated: get=%q list=%q", get.View, list.View)
	}
	if len(reader.views) != 2 || reader.views[0] != "FULL" || reader.views[1] != "BASIC" {
		t.Fatalf("normalized backend views: %v", reader.views)
	}
}
