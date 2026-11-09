package worker

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/tes"
	util "github.com/calypr/funnel/util/rpc"
	grpc_retry "github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/retry"
	"google.golang.org/grpc"
)

// RPCTaskReader provides read access to tasks from the funnel server over gRPC.
type RPCTaskReader struct {
	client      tes.TaskServiceClient
	eventClient events.EventServiceClient
	conn        *grpc.ClientConn
	taskID      string
}

// NewRPCTaskReader returns a new RPC-based task reader.
func NewRPCTaskReader(ctx context.Context, conf *config.RPCClient, taskID string) (*RPCTaskReader, error) {
	conn, err := util.Dial(ctx, conf)
	if err != nil {
		return nil, err
	}
	cli := tes.NewTaskServiceClient(conn)
	return &RPCTaskReader{
		client:      cli,
		eventClient: events.NewEventServiceClient(conn),
		conn:        conn,
		taskID:      taskID,
	}, nil
}

// Task returns the task descriptor.
func (r *RPCTaskReader) Task(ctx context.Context) (*tes.Task, error) {
	return r.client.GetTask(ctx, &tes.GetTaskRequest{
		Id:   r.taskID,
		View: tes.View_FULL.String(),
	})
}

// State returns the current state of the task.
func (r *RPCTaskReader) State(ctx context.Context) (tes.State, error) {
	t, err := r.client.GetTask(ctx, &tes.GetTaskRequest{
		Id:   r.taskID,
		View: tes.View_MINIMAL.String(),
	})
	return t.GetState(), err
}

// ClaimExecution asks the server to atomically admit this worker for the task.
func (r *RPCTaskReader) ClaimExecution(ctx context.Context, taskID string, owner *events.ExecutionOwner) (bool, error) {
	if taskID != r.taskID {
		return false, fmt.Errorf("task reader is bound to task %q, not %q", r.taskID, taskID)
	}
	response, err := r.eventClient.ClaimExecution(ctx, &events.ClaimExecutionRequest{TaskId: taskID, Owner: owner}, grpc_retry.Disable())
	if err != nil {
		return false, err
	}
	if response == nil {
		return false, errors.New("claim execution response was empty")
	}
	return response.GetClaimed(), nil
}

// Close closes the connection.
func (r *RPCTaskReader) Close() {
	err := r.conn.Close()
	if err != nil {
		log.Printf("close task RPC connection: %v", err)
	}
}
