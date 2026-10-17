package events

import (
	"context"

	"github.com/calypr/funnel/util"
)

type retrier struct {
	*util.Retrier
	Writer Writer
}

func (r *retrier) WriteEvent(ctx context.Context, e *Event) error {
	return r.Retry(ctx, func() error {
		return r.Writer.WriteEvent(ctx, e)
	})
}
