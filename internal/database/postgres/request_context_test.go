package postgres

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	appconfig "github.com/calypr/funnel/internal/config"
	"github.com/calypr/funnel/internal/identity"
)

func TestRequestContextRetainsUser(t *testing.T) {
	db := &Postgres{conf: appconfig.DefaultConfig().Postgres}
	user := &identity.UserInfo{Username: "alice"}
	parent, cancel := context.WithCancel(identity.WithUser(t.Context(), user))
	defer cancel()
	ctx, done := db.requestContext(parent)
	defer done()
	if identity.GetUser(ctx) != user {
		t.Error("request context lost authenticated user")
	}
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 31*time.Second {
		t.Error("request context has no configured timeout")
	}
	cancel()
	if ctx.Err() != context.Canceled {
		t.Error("request context lost caller cancellation")
	}
}

func TestInitializationDeadlineClosesStalledConnection(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Error(err)
		}
	})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() {
			if err := conn.Close(); err != nil {
				t.Error(err)
			}
		}()
		// Accept the connection but never answer PostgreSQL's TLS negotiation.
		if _, err := io.Copy(io.Discard, conn); err != nil {
			t.Error(err)
		}
	}()
	conf := appconfig.DefaultConfig().Postgres
	conf.Host = listener.Addr().String()
	db, err := NewPostgres(conf)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if err := db.init(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("initialization error = %v; want deadline exceeded", err)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("initialization left the stalled connection open")
	}
}
