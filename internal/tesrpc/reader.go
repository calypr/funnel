package tesrpc

import (
	"context"

	"github.com/calypr/funnel/tes"
	"google.golang.org/grpc"
)

// ReadOnlyClient describes the TES interface with only the Get/List tasks methods
type ReadOnlyClient interface {
	ListTasks(ctx context.Context, in *tes.ListTasksRequest, opts ...grpc.CallOption) (*tes.ListTasksResponse, error)
	GetTask(ctx context.Context, in *tes.GetTaskRequest, opts ...grpc.CallOption) (*tes.Task, error)
}
