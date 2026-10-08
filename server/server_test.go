package server

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestCustomErrorHandlerStatusCodes(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{
			name: "invalid argument maps to 400",
			err:  status.Errorf(codes.InvalidArgument, "task.Outputs[0].Path: must be an absolute path"),
			want: http.StatusBadRequest,
		},
		{
			name: "unauthenticated maps to 401",
			err:  status.Errorf(codes.Unauthenticated, "missing credentials"),
			want: http.StatusUnauthorized,
		},
		{
			name: "permission denied maps to 403",
			err:  status.Errorf(codes.PermissionDenied, "not your task"),
			want: http.StatusForbidden,
		},
		{
			name: "not found maps to 404",
			err:  status.Errorf(codes.NotFound, "some other not-found condition"),
			want: http.StatusNotFound,
		},
		{
			name: "not found with 'task not found' message maps to 404",
			err:  status.Errorf(codes.NotFound, "task not found: taskID: abc"),
			want: http.StatusNotFound,
		},
		{
			name: "already exists maps to 409",
			err:  status.Errorf(codes.AlreadyExists, "task already exists"),
			want: http.StatusConflict,
		},
		{
			name: "canceled maps to 499",
			err:  status.Errorf(codes.Canceled, "client went away"),
			want: 499,
		},
		{
			name: "deadline exceeded maps to 504",
			err:  status.Errorf(codes.DeadlineExceeded, "timed out"),
			want: http.StatusGatewayTimeout,
		},
		{
			name: "internal (e.g. recovered panic) maps to 500",
			err:  status.Errorf(codes.Internal, "panic"),
			want: http.StatusInternalServerError,
		},
		{
			// CheckBackendParameterSupport errors are wrapped as InvalidArgument
			// in TaskService.CreateTask, so this goes through the InvalidArgument
			// case above, not string-matching.
			name: "unsupported backend parameters (wrapped as InvalidArgument) maps to 400",
			err:  status.Errorf(codes.InvalidArgument, "error from backend: backend parameters not supported"),
			want: http.StatusBadRequest,
		},
		{
			// A plain (non-gRPC-status) error falls into the status.FromError
			// !ok branch and short-circuits to 500 before the switch runs.
			name: "plain non-status error defaults to 500",
			err:  errors.New("some unexpected failure"),
			want: http.StatusInternalServerError,
		},
	}

	marshaler := NewMarshaler()
	req := httptest.NewRequest(http.MethodPost, "/v1/tasks", nil)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			customErrorHandler(context.Background(), nil, marshaler, w, req, tt.err)
			if w.Code != tt.want {
				t.Errorf("got status %d, want %d (body: %s)", w.Code, tt.want, w.Body.String())
			}
		})
	}
}
