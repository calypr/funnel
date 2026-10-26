// Package noop contains a compute backend that does nothing, for testing purposes.
package noop

import (
	"context"

	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/tes"
)

// NewBackend returns a new noop Backend instance.
func NewBackend() *Backend {
	return &Backend{}
}

// Backend is a scheduler backend that doesn't do anything
// which is useful for testing.
type Backend struct {
	events.Computer
}

func (b Backend) CheckBackendParameterSupport(task *tes.Task) error {
	return nil
}

// WriteEvent is a noop and returns nil.
func (b *Backend) WriteEvent(context.Context, *events.Event) error {
	return nil
}

func (b *Backend) Close() {}
