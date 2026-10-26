package events

import (
	"context"

	"github.com/calypr/funnel/logger"
)

// ErrLogger writes an error message to the given logger when an event write fails.
type ErrLogger struct {
	Writer
	Log *logger.Logger
}

// WriteEvent writes the event to the underlying event writer. If an error is returned
// from that write, ErrLogger will log the error to the give logger, and return the error.
func (e *ErrLogger) WriteEvent(ctx context.Context, ev *Event) error {
	err := e.Writer.WriteEvent(ctx, ev)
	if err != nil {
		e.Log.Error("error writing event", "error", err, "event_type", ev.Type.String(), "event_data", ev.Data)
	}
	return err
}
