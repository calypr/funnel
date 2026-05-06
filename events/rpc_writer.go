package events

import (
	"context"

	"github.com/ohsu-comp-bio/funnel/config"
	util "github.com/ohsu-comp-bio/funnel/util/rpc"
	"google.golang.org/grpc"
)

// RPCWriter is a type which writes Events to RPC.
type RPCWriter struct {
	client EventServiceClient
	conn   *grpc.ClientConn
}

// NewRPCWriter returns a new RPCWriter instance.
func NewRPCWriter(ctx context.Context, conf *config.RPCClient) (*RPCWriter, error) {
	conn, err := util.Dial(ctx, conf)
	if err != nil {
		return nil, err
	}
	cli := NewEventServiceClient(conn)

	return &RPCWriter{cli, conn}, nil
}

// WriteEvent writes the event to the server via gRPC.
// The RPC call may timeout, based on the timeout given by the configuration in NewRPCWriter.
func (r *RPCWriter) WriteEvent(ctx context.Context, e *Event) error {
	_, err := r.client.WriteEvent(ctx, e)
	return err
}

// Close closes the connection.
func (r *RPCWriter) Close() {
	r.conn.Close()
}
