package util

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCheckHTTPResponseErrors(t *testing.T) {
	for _, tc := range []struct {
		status     int
		body, want string
	}{
		{401, `{"error":"Bad credentials"}`, `[STATUS CODE - 401] Unauthorized: Bad credentials`},
		{403, `{"message":"Access denied"}`, `[STATUS CODE - 403] Forbidden: Access denied`},
		{403, `{"code":7}`, `[STATUS CODE - 403] Forbidden: {"code":7}`},
		{403, `{"error":"Access denied","detail":"contact admin"}`, `[STATUS CODE - 403] Forbidden: {"error":"Access denied","detail":"contact admin"}`},
		{500, `<html>Site not found</html>`, `[STATUS CODE - 500] Internal Server Error: <html>Site not found</html>`},
		{503, "", `[STATUS CODE - 503] Service Unavailable`},
		{599, "oops", `[STATUS CODE - 599] Unknown Status: oops`},
	} {
		t.Run(tc.want, func(t *testing.T) {
			resp := &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body))}
			_, err := CheckHTTPResponse(resp, nil)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}
}
