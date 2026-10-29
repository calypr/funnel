package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/tes"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type taskReadStub struct {
	task *tes.Task
	err  error
}

func (s taskReadStub) ListTasks(context.Context, *tes.ListTasksRequest) (*tes.ListTasksResponse, error) {
	return &tes.ListTasksResponse{}, nil
}

func (s taskReadStub) GetTask(context.Context, *tes.GetTaskRequest) (*tes.Task, error) {
	return s.task, s.err
}

func (taskReadStub) Close() {}

func taskGateway(t *testing.T, read tes.ReadOnlyServer) http.Handler {
	t.Helper()

	mux := runtime.NewServeMux(
		runtime.WithMarshalerOption(runtime.MIMEWildcard, NewMarshaler()),
		runtime.WithErrorHandler(customErrorHandler),
	)
	taskService := &TaskService{
		Read:    read,
		Event:   events.Noop{},
		Compute: events.Backend{},
		Config:  &config.Config{},
	}
	if err := tes.RegisterTaskServiceHandlerServer(context.Background(), mux, taskService); err != nil {
		t.Fatalf("register task gateway: %v", err)
	}
	return mux
}

func TestTaskGatewayPermissionDeniedOnInitialRead(t *testing.T) {
	handler := taskGateway(t, taskReadStub{err: tes.ErrNotPermitted})

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{name: "get task", method: http.MethodGet, path: "/tasks/task-1"},
		{name: "cancel task", method: http.MethodPost, path: "/tasks/task-1:cancel"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			request := httptest.NewRequest(tt.method, tt.path, nil)
			handler.ServeHTTP(response, request)

			if response.Code != http.StatusForbidden {
				t.Fatalf("HTTP status = %d, want %d; body: %s", response.Code, http.StatusForbidden, response.Body)
			}
			var body JSONError
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode error response: %v", err)
			}
			if body.Error == "" {
				t.Fatalf("expected permission error message, got empty error in %s", response.Body)
			}
		})
	}
}

func TestTaskGatewaySuccessfulGetAndCancel(t *testing.T) {
	handler := taskGateway(t, taskReadStub{task: &tes.Task{Id: "task-1", State: tes.State_RUNNING}})

	for _, tt := range []struct {
		name   string
		method string
		path   string
	}{
		{name: "get task", method: http.MethodGet, path: "/tasks/task-1"},
		{name: "cancel task", method: http.MethodPost, path: "/tasks/task-1:cancel"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(tt.method, tt.path, nil))
			if response.Code != http.StatusOK {
				t.Fatalf("HTTP status = %d, want %d; body: %s", response.Code, http.StatusOK, response.Body)
			}
		})
	}
}

func TestCustomErrorHandlerHTTPStatusCodes(t *testing.T) {
	tests := []struct {
		name string
		code codes.Code
		want int
	}{
		{name: "invalid argument", code: codes.InvalidArgument, want: http.StatusBadRequest},
		{name: "permission denied", code: codes.PermissionDenied, want: http.StatusForbidden},
		{name: "not found", code: codes.NotFound, want: http.StatusNotFound},
		{name: "resource exhausted", code: codes.ResourceExhausted, want: http.StatusTooManyRequests},
		{name: "unavailable", code: codes.Unavailable, want: http.StatusServiceUnavailable},
		{name: "unimplemented", code: codes.Unimplemented, want: http.StatusNotImplemented},
		{name: "internal fallback", code: codes.Internal, want: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			customErrorHandler(request.Context(), nil, NewMarshaler(), response, request, status.Error(tt.code, "request failed"))
			if response.Code != tt.want {
				t.Fatalf("HTTP status = %d, want %d; body: %s", response.Code, tt.want, response.Body)
			}
			var body JSONError
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode error response: %v", err)
			}
			if body.Error != "request failed" {
				t.Fatalf("error message = %q, want %q", body.Error, "request failed")
			}
		})
	}
}
