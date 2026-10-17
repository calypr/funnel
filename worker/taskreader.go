package worker

import (
	"context"

	"github.com/calypr/funnel/tes"
)

// GenericTaskReader provides read access to tasks.
type GenericTaskReader struct {
	get    func(ctx context.Context, in *tes.GetTaskRequest) (*tes.Task, error)
	close  func()
	taskID string
	active bool
}

// NewGenericTaskReader returns a new generic task reader.
func NewGenericTaskReader(get func(ctx context.Context, in *tes.GetTaskRequest) (*tes.Task, error), taskID string, close func()) *GenericTaskReader {
	return &GenericTaskReader{get, close, taskID, true}
}

// Task returns the task descriptor.
func (r *GenericTaskReader) Task(ctx context.Context) (*tes.Task, error) {
	return r.get(ctx, &tes.GetTaskRequest{
		Id:   r.taskID,
		View: tes.View_FULL.String(),
	})
}

// State returns the current state of the task.
func (r *GenericTaskReader) State(ctx context.Context) (tes.State, error) {
	if !r.active {
		return tes.State_SYSTEM_ERROR, nil
	}
	t, err := r.get(ctx, &tes.GetTaskRequest{
		Id:   r.taskID,
		View: tes.View_MINIMAL.String(),
	})
	return t.GetState(), err
}

func (r *GenericTaskReader) Close() {
	r.active = false
	r.close()
}
