package events

import (
	"context"
)

// SystemLogFilter is an event writer implementation that filters system log events.
type SystemLogFilter struct {
	Writer Writer
	Level  string
}

// WriteEvent writes an event to the writer. Writing stops on the first error.
func (w *SystemLogFilter) WriteEvent(ctx context.Context, ev *Event) error {
	switch ev.Type {
	case Type_SYSTEM_LOG:
		lvl := ev.GetSystemLog().Level
		if (w.Level == "debug") ||
			(w.Level == "info" && lvl != "debug") ||
			(w.Level == "warn" && lvl != "debug" && lvl != "info") ||
			(w.Level == "error" && lvl != "debug" && lvl != "info" && lvl != "warn") {
			return w.Writer.WriteEvent(ctx, ev)
		}

	default:
		return w.Writer.WriteEvent(ctx, ev)
	}

	return nil
}

func (w *SystemLogFilter) Close() {
	w.Writer.Close()
}
