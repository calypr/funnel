package events

import "context"

// Service is a wrapper for providing a Writer as a gRPC service.
type Service struct {
	UnimplementedEventServiceServer
	Writer
}

// WriteEvent accepts an RPC call and writes the event to the underlying server.
// WriteEventResponse is always empty, and the error value is the error from the
// undelrying Writer.
func (s *Service) WriteEvent(ctx context.Context, e *Event) (*WriteEventResponse, error) {
	return &WriteEventResponse{}, s.Writer.WriteEvent(ctx, e)
}
