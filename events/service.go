package events

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ExecutionClaimer atomically admits one worker to execute a task.
type ExecutionClaimer interface {
	ClaimExecution(context.Context, string) (bool, error)
}

// Service is a wrapper for providing a Writer as a gRPC service.
type Service struct {
	UnimplementedEventServiceServer
	Writer
	Claimer ExecutionClaimer
}

// WriteEvent accepts an RPC call and writes the event to the underlying server.
// WriteEventResponse is always empty, and the error value is the error from the
// undelrying Writer.
func (s *Service) WriteEvent(ctx context.Context, e *Event) (*WriteEventResponse, error) {
	return &WriteEventResponse{}, s.Writer.WriteEvent(ctx, e)
}

// ClaimExecution atomically admits one worker to execute the task.
func (s *Service) ClaimExecution(ctx context.Context, req *ClaimExecutionRequest) (*ClaimExecutionResponse, error) {
	if req == nil || req.GetTaskId() == "" {
		return nil, status.Error(codes.InvalidArgument, "task id is required")
	}
	if s.Claimer == nil {
		return nil, status.Error(codes.FailedPrecondition, "execution claims are not supported by this database")
	}
	claimed, err := s.Claimer.ClaimExecution(ctx, req.GetTaskId())
	if err != nil {
		return nil, err
	}
	return &ClaimExecutionResponse{Claimed: claimed}, nil
}
