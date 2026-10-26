package storage

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ncw/swift/v2"
)

func TestSwiftAuthenticationAndBucketErrors(t *testing.T) {
	for _, tc := range []struct {
		name       string
		authStatus int
		bucketCode int
		want       string
	}{
		{"auth endpoint missing", 404, 200, "authentication failed"},
		{"invalid credentials", 401, 200, "authentication failed"},
		{"bucket missing", 200, 404, "looking for bucket"},
		{"accessible bucket", 200, 200, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/auth" {
					if tc.authStatus == 200 {
						w.Header().Set("X-Storage-Url", server.URL+"/storage")
						w.Header().Set("X-Auth-Token", "token")
					}
					w.WriteHeader(tc.authStatus)
					return
				}
				w.Header().Set("X-Container-Bytes-Used", "0")
				w.Header().Set("X-Container-Object-Count", "0")
				w.WriteHeader(tc.bucketCode)
			}))
			defer server.Close()
			backend := &Swift{conn: &swift.Connection{AuthUrl: server.URL + "/auth", AuthVersion: 1, UserName: "user", ApiKey: "password"}}
			mux := &Mux{Backends: []Storage{backend}}
			err := CheckGet(mux, "swift://bucket/file")
			if tc.want == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q, got %v", tc.want, err)
			}
		})
	}
}
