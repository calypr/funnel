package server

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/calypr/funnel/internal/tesrpc"
)

type closingTaskService struct {
	tesrpc.UnimplementedTaskServiceServer
	closes atomic.Int32
}

func (s *closingTaskService) Close() { s.closes.Add(1) }

func TestServeClosesGenericTaskServiceOnStartupFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close() //nolint:errcheck // The occupied listener is a fixture.
	tasks := &closingTaskService{}
	srv := newTestServer(listener.Addr().String(), "0")
	srv.Tasks = tasks
	if err := srv.Serve(t.Context()); err == nil {
		t.Fatal("expected RPC bind failure")
	}
	if got := tasks.closes.Load(); got != 1 {
		t.Fatalf("task service close count = %d, want 1", got)
	}
}

func TestServeClosesGenericTaskServiceOnceOnShutdown(t *testing.T) {
	tasks := &closingTaskService{}
	address := freeTCPAddress(t)
	srv := newTestServer(address, "0")
	srv.Tasks = tasks
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("serve: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("server did not stop")
		}
		if got := tasks.closes.Load(); got != 1 {
			t.Errorf("task service close count = %d, want 1", got)
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", address, 50*time.Millisecond)
		if err == nil {
			if err := conn.Close(); err != nil {
				t.Fatal(err)
			}
			return
		}
		select {
		case err := <-done:
			done <- err
			t.Fatalf("server exited before listening: %v", err)
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("server did not start")
}
