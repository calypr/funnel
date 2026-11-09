package postgres

import (
	"context"
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
