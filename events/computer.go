package events

import (
	"context"
	"errors"

	tes "github.com/ohsu-comp-bio/funnel/tes"
)

type Computer interface {
	Writer
	CheckBackendParameterSupport(task *tes.Task) error
}

type Backend struct {
	BackendParameters map[string]bool
}

func (b Backend) CheckBackendParameterSupport(task *tes.Task) error {
	taskBackendParameters := task.Resources.GetBackendParameters()
	for k := range taskBackendParameters {
		_, ok := b.BackendParameters[k]
		if !ok {
			return errors.New("backend parameters not supported")
		}
	}

	return nil
}

// WriteEvent does nothing and returns nil.
func (b Backend) WriteEvent(ctx context.Context, ev *Event) error {
	return nil
}

func (b Backend) Close() {}
