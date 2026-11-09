package server

import (
	"context"

	"github.com/calypr/funnel/logger"
	"github.com/calypr/funnel/tes"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const anonymousUserID = "ANONYMOUS"

func GetUserID(ctx context.Context) string {
	if username := GetUsername(ctx); username != "" {
		return username
	}
	return anonymousUserID
}

// newAuditInterceptor runs after authentication; rejected authentication is
// recorded by Authentication.Interceptor instead.
func newAuditInterceptor(log *logger.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		defer func() {
			if value := recover(); value != nil {
				logAuditEntry(log, info.FullMethod, GetUserID(ctx), auditTaskID(req, nil), status.Error(codes.Internal, "internal server error"))
				panic(value)
			}
		}()
		resp, err := handler(ctx, req)
		logAuditEntry(log, info.FullMethod, GetUserID(ctx), auditTaskID(req, resp), err)
		return resp, err
	}
}

// Audit records contain identifiers and status codes, never error or task payloads.
func logAuditEntry(log *logger.Logger, method, userID, taskID string, err error) {
	fields := []interface{}{"method", method, "userID", userID, "code", status.Code(err).String()}
	if taskID != "" {
		fields = append(fields, "taskID", taskID)
	}
	log.Info("AUDIT", fields...)
}

func auditTaskID(req, resp interface{}) string {
	switch r := req.(type) {
	case *tes.GetTaskRequest:
		return r.GetId()
	case *tes.CancelTaskRequest:
		return r.GetId()
	}
	if r, ok := resp.(*tes.CreateTaskResponse); ok {
		return r.GetId()
	}
	return ""
}
