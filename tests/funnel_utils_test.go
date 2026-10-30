package tests

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	servercmd "github.com/calypr/funnel/cmd/server"
	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/logger"
	"github.com/calypr/funnel/server"
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
				docker.WithAPIVersion("1.44"),
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

func TestPollForServerStartReturnsRunErrorImmediately(t *testing.T) {
	want := errors.New("HTTP listener already in use")
	done := make(chan error, 1)
	done <- want

	start := time.Now()
	err := (&Funnel{}).pollForServerStart(done)
	if !errors.Is(err, want) {
		t.Fatalf("poll error = %v, want wrapped %v", err, want)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("poll took %s to report an exited server", elapsed)
	}
}

func TestPollForServerStartClosesHTTPResponseBody(t *testing.T) {
	bodyClosed := make(chan struct{})
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			t.Errorf("startup probe requested %q, want /healthz", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
			close(bodyClosed)
		case <-time.After(3 * time.Second):
		}
	}))
	defer httpServer.Close()

	serverURL, err := url.Parse(httpServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	conf := config.DefaultConfig()
	conf.Server.HostName = serverURL.Hostname()
	conf.Server.HTTPPort = serverURL.Port()
	f := &Funnel{Conf: conf}

	if err := f.PollForServerStart(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-bodyClosed:
	case <-time.After(time.Second):
		t.Fatal("poll left the HTTP response body open")
	}
}

func TestFunnelCleanupStopsInProcessServer(t *testing.T) {
	f := newServerTestFunnel(t, freeTCPPort(t, "127.0.0.1"), freeTCPPort(t, ""))
	t.Cleanup(f.Cleanup)
	f.StartServer()
	f.Cleanup()

	for _, address := range []string{f.Conf.Server.RPCAddress(), ":" + f.Conf.Server.HTTPPort} {
		listener, err := net.Listen("tcp", address)
		if err != nil {
			t.Fatalf("listener at %s remained open after cleanup: %v", address, err)
		}
		listener.Close()
	}
}

func TestStartServerReturnsHTTPBindErrorImmediately(t *testing.T) {
	httpListener, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer httpListener.Close()
	f := newServerTestFunnel(t, freeTCPPort(t, "127.0.0.1"), strconv.Itoa(httpListener.Addr().(*net.TCPAddr).Port))
	t.Cleanup(f.Cleanup)

	start := time.Now()
	var recovered any
	func() {
		defer func() {
			recovered = recover()
		}()
		f.StartServer()
	}()
	if recovered == nil {
		t.Fatal("StartServer returned without reporting the occupied HTTP port")
	}
	startErr, ok := recovered.(error)
	if !ok || !errors.Is(startErr, syscall.EADDRINUSE) {
		t.Fatalf("StartServer panic = %v, want HTTP address-in-use error", recovered)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("StartServer took %s to report the occupied HTTP port", elapsed)
	}

	f.Cleanup()
	rpcListener, err := net.Listen("tcp", f.Conf.Server.RPCAddress())
	if err != nil {
		t.Fatalf("RPC listener remained open after startup failure: %v", err)
	}
	rpcListener.Close()
}

func newServerTestFunnel(t *testing.T, rpcPort, httpPort string) *Funnel {
	t.Helper()
	conf := config.DefaultConfig()
	conf.Server.HostName = "127.0.0.1"
	conf.Server.RPCPort = rpcPort
	conf.Server.HTTPPort = httpPort
	conf.RPCClient.ServerAddress = conf.Server.RPCAddress()
	conf.LocalStorage.AllowedDirs = []string{t.TempDir()}
	conf.Worker.WorkDir = t.TempDir()

	log := logger.NewLogger("server-lifecycle-test", logger.DefaultConfig())
	log.Discard()
	apiServer := &server.Server{
		RPCAddress: conf.Server.RPCAddress(),
		HTTPPort:   conf.Server.HTTPPort,
		Tasks:      &server.TaskService{},
		Log:        log,
	}
	return &Funnel{
		Conf:       conf,
		StorageDir: conf.LocalStorage.AllowedDirs[0],
		Server:     apiServer,
		Srv:        &servercmd.Server{Server: apiServer},
	}
}

func freeTCPPort(t *testing.T, host string) string {
	t.Helper()
	listener, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return strconv.Itoa(port)
}
