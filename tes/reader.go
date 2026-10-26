package tes

import (
	"context"

	"google.golang.org/grpc"
)

// ReadOnlyServer describes the TES interface with only the Get/List tasks methods
type ReadOnlyServer interface {
	ListTasks(ctx context.Context, in *ListTasksRequest) (*ListTasksResponse, error)
	GetTask(ctx context.Context, in *GetTaskRequest) (*Task, error)
	Close()
}

// ReadOnlyClient describes the TES interface with only the Get/List tasks methods
type ReadOnlyClient interface {
	ListTasks(ctx context.Context, in *ListTasksRequest, opts ...grpc.CallOption) (*ListTasksResponse, error)
	GetTask(ctx context.Context, in *GetTaskRequest, opts ...grpc.CallOption) (*Task, error)
}
