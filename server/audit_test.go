package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/tes"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestAuditRequests(t *testing.T) {
	const secret = "AUDIT_SECRET_SENTINEL"
	for _, tc := range []struct {
		name, method, authorization, user, taskID string
		public, missingMetadata, panicHandler     bool
		req, resp                                 interface{}
		err                                       error
		code                                      codes.Code
	}{
		{name: "create", req: &tes.Task{Description: secret}, resp: &tes.CreateTaskResponse{Id: "created"}, user: "alice", taskID: "created"},
		{name: "get failure", req: &tes.GetTaskRequest{Id: "requested"}, err: status.Error(codes.NotFound, secret), code: codes.NotFound, user: "alice", taskID: "requested"},
		{name: "typed nil response", resp: (*tes.CreateTaskResponse)(nil), err: errors.New(secret), code: codes.Unknown, user: "alice"},
		{name: "panic", req: &tes.CancelTaskRequest{Id: "cancelled"}, panicHandler: true, code: codes.Internal, user: "alice", taskID: "cancelled"},
		{name: "missing metadata", missingMetadata: true, code: codes.InvalidArgument, user: anonymousUserID},
		{name: "missing token", authorization: "missing", code: codes.Unauthenticated, user: anonymousUserID},
		{name: "invalid token", authorization: "Bearer " + secret, code: codes.Unauthenticated, user: anonymousUserID},
		{name: "scheduler denied", method: "/scheduler.SchedulerService/PutNode", code: codes.PermissionDenied, user: "alice"},
		{name: "public", public: true, user: anonymousUserID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log, output := serverCaptureLogger(t)
			creds := []*config.BasicCredential{{User: "alice", Password: secret}}
			if tc.public {
				creds = nil
			}
			auth := NewAuthentication(creds, nil, AccessOwner)
			auth.log = log
			ctx := context.Background()
			if !tc.missingMetadata {
				authorization := tc.authorization
				if authorization == "" {
					authorization = "Basic " + base64.StdEncoding.EncodeToString([]byte("alice:"+secret))
				}
				md := metadata.MD{}
				if authorization != "missing" {
					md.Set("authorization", authorization)
				}
				ctx = metadata.NewIncomingContext(ctx, md)
			}
			method := tc.method
			if method == "" {
				method = "/tes.TaskService/Test"
			}
			info := &grpc.UnaryServerInfo{FullMethod: method}
			called := false
			resp, err := auth.Interceptor(ctx, tc.req, info, func(ctx context.Context, req interface{}) (interface{}, error) {
				return newAuditInterceptor(log)(ctx, req, info, func(ctx context.Context, req interface{}) (interface{}, error) {
					return newDebugInterceptor(log)(ctx, req, info, func(context.Context, interface{}) (interface{}, error) {
						called = true
						if tc.panicHandler {
							panic(secret)
						}
						return tc.resp, tc.err
					})
				})
			})
			if status.Code(err) != tc.code {
				t.Fatalf("status = %v, want %v", err, tc.code)
			}
			if called && !tc.panicHandler && (resp != tc.resp || err != tc.err) {
				t.Fatal("audit changed the handler result")
			}
			if strings.Contains(output.String(), secret) {
				t.Fatalf("sensitive payload in logs: %s", output)
			}
			var entries []map[string]interface{}
			for _, line := range bytes.Split(bytes.TrimSpace(output.Bytes()), []byte("\n")) {
				var entry map[string]interface{}
				if err := json.Unmarshal(line, &entry); err != nil {
					t.Fatal(err)
				}
				if entry["msg"] == "AUDIT" {
					entries = append(entries, entry)
				}
			}
			if len(entries) != 1 {
				t.Fatalf("got %d audit entries, want one: %s", len(entries), output)
			}
			entry := entries[0]
			if entry["userID"] != tc.user || entry["method"] != method || entry["code"] != tc.code.String() {
				t.Fatalf("incorrect audit metadata: %v", entry)
			}
			if got, _ := entry["taskID"].(string); got != tc.taskID {
				t.Fatalf("audit task ID = %q, want %q", got, tc.taskID)
			}
		})
	}
}

func TestAuditInternalUser(t *testing.T) {
	if got := GetUserID(t.Context()); got != "SYSTEM" {
		t.Fatalf("internal user = %q", got)
	}
}
