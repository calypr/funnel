package worker

import (
	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/tes"
	util "github.com/ohsu-comp-bio/funnel/util/rpc"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
)

// RPCTaskReader provides read access to tasks from the funnel server over gRPC.
type RPCTaskReader struct {
	client tes.TaskServiceClient
	conn   *grpc.ClientConn
	taskID string
}

// NewRPCTaskReader returns a new RPC-based task reader.
func NewRPCTaskReader(ctx context.Context, conf *config.RPCClient, taskID string) (*RPCTaskReader, error) {
	conn, err := util.Dial(ctx, conf)
	if err != nil {
		return nil, err
	}
	cli := tes.NewTaskServiceClient(conn)
	return &RPCTaskReader{cli, conn, taskID}, nil
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

// Close closes the connection.
func (r *RPCTaskReader) Close() {

	r.conn.Close()
}
