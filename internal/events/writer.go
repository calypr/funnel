package events

import (
	"context"
)

// Writer provides write access to a task's events
type Writer interface {
	WriteEvent(context.Context, *Event) error
	Close()
}

// MultiWriter allows writing an event to a list of Writers.
// Writing stops on the first error.
type MultiWriter []Writer

// WriteEvent writes an event to all the writers. Writing stops on the first error.
func (mw *MultiWriter) WriteEvent(ctx context.Context, ev *Event) error {
	for _, w := range *mw {
		err := w.WriteEvent(ctx, ev)
		if err != nil {
			return err
		}
	}
	return nil
}

func (mw *MultiWriter) Close() {
	for _, w := range *mw {
		w.Close()
	}
}

// Noop provides an event writer that does nothing.
type Noop struct{}

// WriteEvent does nothing and returns nil.
func (n Noop) WriteEvent(ctx context.Context, ev *Event) error {
	return nil
}

func (n Noop) Close() {}
