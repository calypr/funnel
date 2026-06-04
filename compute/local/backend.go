// Package local contains code for accessing compute resources via the local computer, for Funnel development and debugging.
package local

import (
	"context"
	"fmt"

	"github.com/imdario/mergo"
	workerCmd "github.com/ohsu-comp-bio/funnel/cmd/worker"
	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/events"
	"github.com/ohsu-comp-bio/funnel/logger"
	"github.com/ohsu-comp-bio/funnel/plugins/proto"
	"github.com/ohsu-comp-bio/funnel/tes"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// NewBackend returns a new local Backend instance.
func NewBackend(ctx context.Context, conf *config.Config, log *logger.Logger) (*Backend, error) {
	// Using nil for the backendParameters here until those are specified
	return &Backend{conf, log, nil, nil}, nil
}

// Backend represents the local backend.
type Backend struct {
	conf              *config.Config
	log               *logger.Logger
	backendParameters map[string]string
	events.Computer
}

func (b Backend) CheckBackendParameterSupport(task *tes.Task) error {
	if !task.Resources.GetBackendParametersStrict() {
		return nil
	}

	taskBackendParameters := task.Resources.GetBackendParameters()
	for k := range taskBackendParameters {
		_, ok := b.backendParameters[k]
		if !ok {
			return status.Errorf(codes.InvalidArgument, "backend parameters not supported: %s", k)
		}
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
		w.Run(ctx)
		w.Close()
	}()

	return nil
}
