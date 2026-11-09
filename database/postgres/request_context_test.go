package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/server"
)

func TestRequestContextRetainsUser(t *testing.T) {
	db := &Postgres{conf: config.DefaultConfig().Postgres}
	user := &server.UserInfo{Username: "alice"}
	parent, cancel := context.WithCancel(context.WithValue(t.Context(), server.UserInfoKey, user))
	defer cancel()
	ctx, done := db.requestContext(parent)
	defer done()
	if server.GetUser(ctx) != user {
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
