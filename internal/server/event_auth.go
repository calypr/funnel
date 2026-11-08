package server

import (
	"context"

	"github.com/calypr/funnel/api/tes"
	"github.com/calypr/funnel/internal/events"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *Server) authorizeEventWrites(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	if info.FullMethod != events.EventService_WriteEvent_FullMethodName {
		return handler(ctx, req)
	}
	event, ok := req.(*events.Event)
	if !ok || event == nil || event.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "task event and task id are required")
	}
	if event.Type == events.Type_TASK_CREATED {
		user := GetUser(ctx)
		if !user.IsAdmin && !user.IsPublic {
			return nil, status.Error(codes.PermissionDenied, "create tasks through the TES task API")
		}
	} else {
		_, err := s.Tasks.GetTask(ctx, &tes.GetTaskRequest{Id: event.Id, View: tes.View_MINIMAL.String()})
		if err != nil {
			return nil, err
		}
	}
	return handler(ctx, req)
}
