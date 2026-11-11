package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	serverauth "github.com/calypr/funnel/internal/server/auth"
)

func TestBrowserAPIRequestsReceiveJSON(t *testing.T) {
	s := &Server{}
	auth := serverauth.NewAuthentication(nil, nil, "", nil)
	handler := s.routes(auth, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"fixture","state":"EXECUTOR_ERROR"}`))
	}))
	for _, path := range []string{
		"/v1/tasks/fixture",
		"/v1/tasks/fixture?view=MINIMAL",
		"/v1/tasks/fixture?html",
		"/ga4gh/tes/v1/tasks/fixture",
	} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != http.StatusOK || !strings.HasPrefix(res.Header().Get("Content-Type"), "application/json") ||
				!strings.Contains(res.Body.String(), `"id":"fixture"`) {
				t.Fatalf("response = %d %s %s", res.Code, res.Header().Get("Content-Type"), res.Body.String())
			}
		})
	}
	req := httptest.NewRequest(http.MethodGet, "/tasks", nil)
	req.Header.Set("Accept", "text/html")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if !strings.Contains(res.Header().Get("Content-Type"), "text/html") {
		t.Fatal("dashboard route no longer serves HTML")
	}
}
