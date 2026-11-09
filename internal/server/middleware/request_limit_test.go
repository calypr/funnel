package middleware

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGatewayRequestBodyLimit(t *testing.T) {
	const limit = 4 << 20
	for _, size := range []int{2, limit, limit + 1} {
		var readErr error
		var bytesRead int
		handler := MessageHandler(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			body, err := io.ReadAll(req.Body)
			bytesRead, readErr = len(body), err
		}))
		req := httptest.NewRequest(http.MethodPost, "/v1/tasks", strings.NewReader(strings.Repeat(" ", size)))
		handler.ServeHTTP(httptest.NewRecorder(), req)
		if size <= limit && readErr != nil {
			t.Fatalf("allowed request rejected: %v", readErr)
		}
		if size > limit {
			var tooLarge *http.MaxBytesError
			if !errors.As(readErr, &tooLarge) || bytesRead > limit {
				t.Errorf("oversize request read %d bytes, error %v", bytesRead, readErr)
			}
		}
	}
}
