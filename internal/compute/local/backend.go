// Package local contains code for accessing compute resources via the local computer, for Funnel development and debugging.
package local

import (
	"context"
	"fmt"
	"sort"

	"github.com/calypr/funnel/internal/compute"

	"dario.cat/mergo"
	"github.com/calypr/funnel/api/config"
	proto "github.com/calypr/funnel/api/plugin"
	"github.com/calypr/funnel/api/tes"
	workerCmd "github.com/calypr/funnel/internal/cli/worker"
	"github.com/calypr/funnel/internal/events"
	"github.com/calypr/funnel/internal/logger"
	taskpolicy "github.com/calypr/funnel/internal/task"
	protobuf "google.golang.org/protobuf/proto"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// NewBackend returns a new local Backend instance.
func NewBackend(ctx context.Context, conf *config.Config, log *logger.Logger) (*Backend, error) {
	return &Backend{conf, log, nil, nil}, nil
}

// Backend represents the local backend.
type Backend struct {
	conf              *config.Config
	log               *logger.Logger
	backendParameters map[string]string
	compute.Computer
}

func (b Backend) BackendParameterKeys() []string {
	keys := []string{"gpu_count", "gpu_devices"}
	for key := range b.backendParameters {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func (b Backend) CheckBackendParameterSupport(task *tes.Task) error {
	if _, err := taskpolicy.FilterBackendParameters(task.GetResources(), b.BackendParameterKeys()); err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	count, err := taskpolicy.GPUCount(task.GetResources())
	if err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	if _, err := taskpolicy.GPUDevices(task.GetResources(), count); err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	return nil
}

// WriteEvent writes an event to the compute backend.
// Currently, only TASK_CREATED is handled, which calls Submit.
func (b *Backend) WriteEvent(ctx context.Context, ev *events.Event) error {
	switch ev.Type {
	case events.Type_TASK_CREATED:
		conf, err := b.configForRequest(ctx)
		if err != nil {
			return err
		}
		requestBackend := *b
		requestBackend.conf = conf
		return requestBackend.Submit(ev.GetTask())
	}
	return nil
}

func (b *Backend) configForRequest(ctx context.Context) (*config.Config, error) {
	conf := protobuf.Clone(b.conf).(*config.Config)
	if conf.Plugins == nil {
		return conf, nil
	}
	resp, ok := ctx.Value("pluginResponse").(*proto.JobResponse)
	if !ok {
		return nil, fmt.Errorf("missing plugin response")
	}
	if resp.Config != nil {
		if err := mergo.Merge(conf, resp.Config, mergo.WithOverwriteWithEmptyValue); err != nil {
			return nil, fmt.Errorf("error merging configs: %w", err)
		}
	}
	return conf, nil
}

func (b *Backend) Close() {}

// Submit submits a task. For the Local backend this results in the task
// running immediately.
func (b *Backend) Submit(task *tes.Task) error {
	ctx := context.Background()

	w, err := workerCmd.NewWorker(ctx, b.conf, b.log, &workerCmd.Options{
		TaskID: task.Id,
	})
	if err != nil {
		return err
	}

	go func() {
		err := w.Run(ctx)
		if err != nil {
			b.log.Error("local worker failed", "taskID", task.Id, "error", err)
		}
		w.Close()
	}()

	return nil
}
