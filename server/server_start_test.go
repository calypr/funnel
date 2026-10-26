package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/calypr/funnel/logger"
)

func TestServeReturnsHTTPBindErrorAndReleasesRPCListener(t *testing.T) {
	httpListener, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer httpListener.Close()

	rpcAddress := freeTCPAddress(t)
	s := newTestServer(rpcAddress, strconv.Itoa(httpListener.Addr().(*net.TCPAddr).Port))
	done := make(chan error, 1)
	go func() {
		done <- s.Serve(context.Background())
	}()

	select {
	case err := <-done:
		if !errors.Is(err, syscall.EADDRINUSE) {
			t.Fatalf("Serve error = %v, want HTTP address-in-use error", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not report the occupied HTTP port promptly")
	}

	rpcListener, err := net.Listen("tcp", rpcAddress)
	if err != nil {
		t.Fatalf("RPC listener was not released after HTTP bind failure: %v", err)
	}
	rpcListener.Close()
}

func TestServeCancellationWaitsForBothListenersToStop(t *testing.T) {
	rpcAddress := freeTCPAddress(t)
	httpPort := strconv.Itoa(tcpPort(t))
	s := newTestServer(rpcAddress, httpPort)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- s.Serve(ctx)
	}()

	url := fmt.Sprintf("http://127.0.0.1:%s/healthz", httpPort)
	deadline := time.After(5 * time.Second)
	for {
		response, err := http.Get(url)
		if err == nil {
			response.Body.Close()
			break
		}
		select {
		case <-deadline:
			t.Fatalf("HTTP listener did not start: %v", err)
		case <-time.After(10 * time.Millisecond):
		}
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve after context cancellation = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after context cancellation")
	}

	for _, address := range []string{rpcAddress, ":" + httpPort} {
		listener, err := net.Listen("tcp", address)
		if err != nil {
			t.Fatalf("listener at %s was not released: %v", address, err)
		}
		listener.Close()
	}
}

func newTestServer(rpcAddress, httpPort string) *Server {
	log := logger.NewLogger("server-test", logger.DefaultConfig())
	log.Discard()
	return &Server{
		RPCAddress: rpcAddress,
		HTTPPort:   httpPort,
		Tasks:      &TaskService{},
		Log:        log,
	}
}

func freeTCPAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func tcpPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}
