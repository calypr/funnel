package dockerutil

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	mobyclient "github.com/moby/moby/client"
)

func TestNewDockerClientUsesServerAPIVersion(t *testing.T) {
	t.Setenv("DOCKER_API_VERSION", "")
	var tooNewRequests atomic.Int32
	var compatibleRequests atomic.Int32
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/_ping":
			w.WriteHeader(http.StatusOK)
		case "/v1.56/version":
			tooNewRequests.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"message":"client version 1.56 is too new. Maximum supported API version is 1.39"}`) //nolint:errcheck // Fixture response is unused once the test client disconnects.
		case "/v1.39/version":
			compatibleRequests.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"Version":"18.09.9","ApiVersion":"1.39"}`) //nolint:errcheck // Fixture response is unused once the test client disconnects.
		default:
			t.Errorf("unexpected Docker API request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer daemon.Close()
	t.Setenv("DOCKER_HOST", "tcp://"+strings.TrimPrefix(daemon.URL, "http://"))

	client, err := NewDockerClient()
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close() //nolint:errcheck // Best-effort test transport cleanup; no task output depends on closure.
	if got := client.ClientVersion(); got != "1.39" {
		t.Fatalf("client API version = %q, want 1.39", got)
	}
	version, err := client.ServerVersion(t.Context(), mobyclient.ServerVersionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if version.APIVersion != "1.39" {
		t.Fatalf("server API version = %q, want 1.39", version.APIVersion)
	}
	if got := tooNewRequests.Load(); got != 1 {
		t.Fatalf("requests using unsupported API version = %d, want 1", got)
	}
	if got := compatibleRequests.Load(); got != 1 {
		t.Fatalf("requests using server API version = %d, want 1", got)
	}
	if got := os.Getenv("DOCKER_API_VERSION"); got != "1.39" {
		t.Fatalf("DOCKER_API_VERSION = %q, want 1.39", got)
	}
}
