// Package local contains code for accessing compute resources via the local computer, for Funnel development and debugging.
package local

import (
	"context"
	"fmt"
	"sort"

	"dario.cat/mergo"
	workerCmd "github.com/calypr/funnel/cmd/worker"
	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/logger"
	"github.com/calypr/funnel/plugins/proto"
	"github.com/calypr/funnel/tes"

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
	events.Computer
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
	if _, err := tes.FilterBackendParameters(task.GetResources(), b.BackendParameterKeys()); err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	count, err := tes.GPUCount(task.GetResources())
	if err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	if _, err := tes.GPUDevices(task.GetResources(), count); err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	return nil
}

// WriteEvent writes an event to the compute backend.
// Currently, only TASK_CREATED is handled, which calls Submit.
func (b *Backend) WriteEvent(ctx context.Context, ev *events.Event) error {
	// TODO: Should this be moved to the switch statement so it's only run on TASK_CREATED?
	if b.conf.Plugins != nil {
		err := b.UpdateConfig(ctx)
		if err != nil {
			return fmt.Errorf("error updating config from plugin response: %v", err)
		}
	}

	switch ev.Type {
	case events.Type_TASK_CREATED:
		return b.Submit(ev.GetTask())
	}
	return nil
}

func (b *Backend) UpdateConfig(ctx context.Context) error {
	resp, ok := ctx.Value("pluginResponse").(*proto.JobResponse)
	if !ok {
		return fmt.Errorf("Failed to unmarshal plugin response %v", ctx.Value("pluginResponse"))
	}

	// TODO: Review all security implications of merging configs (injections, shared state, etc.)
	if resp.Config != nil {
		err := b.MergeConfigs(resp.Config)
		if err != nil {
			b.log.Error("error merging configs from plugin", "error", err)
		}
	}

	return nil
}

func (b *Backend) MergeConfigs(c *config.Config) error {
	err := mergo.MergeWithOverwrite(b.conf, c)
	if err != nil {
		return fmt.Errorf("error merging configs: %v", err)
	}

	return nil
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
