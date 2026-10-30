package tes

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

const DefaultResponseBytes = 4 * 1024 * 1024

type taskPageBytesKey struct{}

func WithTaskPageBytes(ctx context.Context, size int) context.Context {
	return context.WithValue(ctx, taskPageBytesKey{}, size)
}

type TaskPage struct {
	response ListTasksResponse
	size     int
	limit    int
}

func NewTaskPage(ctx context.Context) *TaskPage {
	limit, _ := ctx.Value(taskPageBytesKey{}).(int)
	if limit <= 0 {
		limit = DefaultResponseBytes
	}
	return &TaskPage{limit: limit}
}

// Add retains the exclusive cursor after each accepted task, so a refused task is retried.
func (p *TaskPage) Add(task *Task, nextToken string) (bool, error) {
	size := p.size + protowire.SizeTag(1) + protowire.SizeBytes(proto.Size(task))
	total := size
	if nextToken != "" {
		total += protowire.SizeTag(2) + protowire.SizeBytes(len(nextToken))
	}
	if total > p.limit {
		if len(p.response.Tasks) == 0 {
			return false, status.Errorf(codes.ResourceExhausted, "task %q exceeds the task response budget (%d bytes); request MINIMAL view or increase Server.MaxResponseBytes and the gRPC client's receive limit", task.Id, p.limit)
		}
		return false, nil
	}
	p.response.Tasks = append(p.response.Tasks, task)
	p.response.NextPageToken = nextToken
	p.size = size
	return true, nil
}

func (p *TaskPage) Finish(hasMore bool) *ListTasksResponse {
	if !hasMore {
		p.response.NextPageToken = ""
	}
	return &p.response
}
