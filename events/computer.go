package events

import (
	"context"
	"sort"

	tes "github.com/calypr/funnel/tes"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Computer interface {
	Writer
	CheckBackendParameterSupport(task *tes.Task) error
}

type Backend struct {
	BackendParameters map[string]bool
}

func (b Backend) CheckBackendParameterSupport(task *tes.Task) error {
	_, err := tes.FilterBackendParameters(task.GetResources(), b.BackendParameterKeys())
	if err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}

	return nil
}

// WriteEvent does nothing and returns nil.
func (b Backend) WriteEvent(ctx context.Context, ev *Event) error {
	return nil
}

func (b Backend) Close() {}

func (b Backend) BackendParameterKeys() []string {
	keys := make([]string, 0, len(b.BackendParameters))
	for key := range b.BackendParameters {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
