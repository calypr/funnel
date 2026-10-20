package tests

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	docker "github.com/moby/moby/client"
)

func TestWaitForDockerDestroy(t *testing.T) {
	tests := []struct {
		name         string
		statusCodes  []int
		wantRequests int32
		wantPanic    bool
	}{
		{
			name:         "already deleted",
			statusCodes:  []int{http.StatusNotFound},
			wantRequests: 1,
		},
		{
			name:         "eventual deletion",
			statusCodes:  []int{http.StatusOK, http.StatusOK, http.StatusNotFound},
			wantRequests: 3,
		},
		{
			name:         "unexpected inspect error",
			statusCodes:  []int{http.StatusInternalServerError},
			wantRequests: 1,
			wantPanic:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/events") {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					w.(http.Flusher).Flush()
					<-r.Context().Done()
					return
				}

				if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/containers/container-id/json") {
					http.NotFound(w, r)
					return
				}

				request := int(requests.Add(1))
				status := tt.statusCodes[len(tt.statusCodes)-1]
				if request <= len(tt.statusCodes) {
					status = tt.statusCodes[request-1]
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				if status == http.StatusOK {
					_ = json.NewEncoder(w).Encode(map[string]string{"Id": "container-id"})
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]string{"message": "container inspect failed"})
			}))
			defer server.Close()

			cli, err := docker.New(
				docker.WithHost("tcp://"+strings.TrimPrefix(server.URL, "http://")),
				docker.WithVersion("1.44"),
			)
			if err != nil {
				t.Fatal(err)
			}
			defer cli.Close()

			f := &Funnel{Docker: cli, rate: time.Millisecond}
			panicked := false
			func() {
				defer func() {
					panicked = recover() != nil
				}()
				f.WaitForDockerDestroy("container-id")
			}()

			if panicked != tt.wantPanic {
				t.Fatalf("WaitForDockerDestroy panicked = %t, want %t", panicked, tt.wantPanic)
			}
			if got := requests.Load(); got != tt.wantRequests {
				t.Fatalf("Docker container inspections = %d, want %d", got, tt.wantRequests)
			}
		})
	}
}
