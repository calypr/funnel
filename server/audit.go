package server

import (
	"github.com/ohsu-comp-bio/funnel/logger"
	"github.com/ohsu-comp-bio/funnel/tes"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Message used for every audit log entry, so that the audit trail can be
// extracted from the rest of the logs by filtering on it.
const auditMessage = "AUDIT"

// Return a new gRPC interceptor function that writes an audit log entry, at
// the Info level, for every API request. Each entry records the ID of the user
// who made the request (see GetUserID), so that actions can be
// attributed to the user who requested them.
//
// This interceptor must be chained after the authentication interceptor,
// otherwise the user is not yet known. Requests rejected by authentication
// never reach this interceptor and are logged by the authentication
// interceptor itself.
func newAuditInterceptor(log *logger.Logger) grpc.UnaryServerInterceptor {
	// Return a function that is the interceptor.
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler) (interface{}, error) {

		// A panicking handler still gets an audit entry; the panic is then
		// passed on to the recovery interceptor.
		defer func() {
			if r := recover(); r != nil {
				logAuditEntry(log, info.FullMethod, GetUserID(ctx),
					auditTaskID(req, nil), status.Error(codes.Internal, "panic"))
				panic(r)
			}
		}()

		resp, err := handler(ctx, req)
		logAuditEntry(log, info.FullMethod, GetUserID(ctx),
			auditTaskID(req, resp), err)

		return resp, err
	}
}

// Writes a single audit log entry. The taskID is omitted from the entry when
// the request does not concern a specific task.
func logAuditEntry(log *logger.Logger, method string, userID string, taskID string, err error) {
	args := []interface{}{
		"method", method,
		"userID", userID,
		"code", status.Code(err).String(),
	}

	if taskID != "" {
		args = append(args, "taskID", taskID)
	}
	if err != nil {
		args = append(args, "error", err)
	}

	log.Info(auditMessage, args...)
}

// Returns the ID of the task a request concerns, or an empty string for
// requests which do not concern a specific task. For task creation the ID is
// only known once the task has been created, hence it is taken from the
// response.
func auditTaskID(req interface{}, resp interface{}) string {
	switch r := req.(type) {
	case *tes.GetTaskRequest:
		return r.Id
	case *tes.CancelTaskRequest:
		return r.Id
	}

	if r, ok := resp.(*tes.CreateTaskResponse); ok && r != nil {
		return r.Id
	}

	return ""
}
