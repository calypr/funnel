package scheduler

import (
	"context"

	"github.com/calypr/funnel/api/config"
	"github.com/calypr/funnel/internal/events"
	"github.com/calypr/funnel/internal/rpcclient"
	"google.golang.org/grpc"
)

// Client is a client for the scheduler and event gRPC services.
type Client interface {
	events.EventServiceClient
	SchedulerServiceClient
	Close() error
}

type client struct {
	events.EventServiceClient
	SchedulerServiceClient
	conn *grpc.ClientConn
}

// NewClient returns a new Client instance connected to the
// scheduler and task logger services at a given address
// (e.g. "localhost:9090")
func NewClient(ctx context.Context, conf *config.RPCClient) (Client, error) {
	conn, err := rpcclient.Dial(ctx, conf)
	if err != nil {
		return nil, err
	}
	e := events.NewEventServiceClient(conn)
	s := NewSchedulerServiceClient(conn)
	return &client{e, s, conn}, nil
}

// Close closes the client connection.
func (client *client) Close() error {
	return client.conn.Close()
}
