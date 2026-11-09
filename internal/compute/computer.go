package compute

import (
	"context"
	"sort"

	"github.com/calypr/funnel/internal/events"

	tes "github.com/calypr/funnel/api/tes"
	taskpolicy "github.com/calypr/funnel/internal/task"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Computer interface {
	events.Writer
	CheckBackendParameterSupport(task *tes.Task) error
}

type Backend struct {
	BackendParameters map[string]bool
}

func (b Backend) CheckBackendParameterSupport(task *tes.Task) error {
	_, err := taskpolicy.FilterBackendParameters(task.GetResources(), b.BackendParameterKeys())
	if err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}

	return nil
}

// WriteEvent does nothing and returns nil.
func (b Backend) WriteEvent(ctx context.Context, ev *events.Event) error {
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
