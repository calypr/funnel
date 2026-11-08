package taskmodel

import (
	"context"

	"github.com/calypr/funnel/tes"
)

// ReadOnlyServer describes the TES interface with only the Get/List tasks methods
type ReadOnlyServer interface {
	ListTasks(ctx context.Context, in *tes.ListTasksRequest) (*tes.ListTasksResponse, error)
	GetTask(ctx context.Context, in *tes.GetTaskRequest) (*tes.Task, error)
	Close()
}
