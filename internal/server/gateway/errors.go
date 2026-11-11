// Package gateway contains the HTTP gateway's marshaling and response helpers.
package gateway

import (
	"context"
	"net/http"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ErrorHandler maps gRPC errors to HTTP statuses for the gRPC gateway.
// Returns '400' for invalid backend parameters and '500' for all other errors
// Required for TES Compliance Tests
func ErrorHandler(ctx context.Context, mux *runtime.ServeMux, marshaler runtime.Marshaler, w http.ResponseWriter, r *http.Request, err error) {
	const fallback = `{"error": "failed to process the request"}`
	w.Header().Set("Content-Type", "application/json")

	st, ok := status.FromError(err)
	if !ok {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(fallback)) //nolint:errcheck // The fallback response cannot be replaced after a client write failure.
		return
	}

	switch st.Code() {
	case codes.InvalidArgument, codes.FailedPrecondition, codes.OutOfRange: // 400
		w.WriteHeader(http.StatusBadRequest)
	case codes.Unauthenticated:
		w.WriteHeader(http.StatusUnauthorized) // 401
	case codes.PermissionDenied:
		w.WriteHeader(http.StatusForbidden) // 403
	case codes.NotFound:
		w.WriteHeader(http.StatusNotFound) // 404
	case codes.AlreadyExists, codes.Aborted: // 409
		w.WriteHeader(http.StatusConflict)
	case codes.Canceled:
		w.WriteHeader(499)
	case codes.DeadlineExceeded: // 504
		w.WriteHeader(http.StatusGatewayTimeout)
	case codes.ResourceExhausted: // 429
		w.WriteHeader(http.StatusTooManyRequests)
	case codes.Unavailable: // 503
		w.WriteHeader(http.StatusServiceUnavailable)
	case codes.Unimplemented: // 501
		w.WriteHeader(http.StatusNotImplemented)
	default:
		w.WriteHeader(http.StatusInternalServerError) // 500
	}

	// Write the error message
	jErr := JSONError{Error: st.Message()}
	jErrBytes, mErr := marshaler.Marshal(jErr)
	if mErr != nil {
		_, _ = w.Write([]byte(fallback)) //nolint:errcheck // The fallback response cannot be replaced after a client write failure.
		return
	}
	_, _ = w.Write(jErrBytes) //nolint:errcheck // The error response cannot be replaced after a client write failure.
}

type JSONError struct {
	Error string `json:"error"`
}
