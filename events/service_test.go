package events

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/calypr/funnel/tes"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type executionClaimerFunc func(context.Context, string, *ExecutionOwner) (bool, error)

func (f executionClaimerFunc) ClaimExecution(ctx context.Context, taskID string, owner *ExecutionOwner) (bool, error) {
	return f(ctx, taskID, owner)
}

func TestClaimExecutionMapsNotPermitted(t *testing.T) {
	service := &Service{Claimer: executionClaimerFunc(func(context.Context, string, *ExecutionOwner) (bool, error) {
		return false, fmt.Errorf("claim database record: %w", tes.ErrNotPermitted)
	})}

	_, err := service.ClaimExecution(context.Background(), &ClaimExecutionRequest{TaskId: "private-task"})
	if got := status.Code(err); got != codes.PermissionDenied {
		t.Fatalf("ClaimExecution() code = %s, want %s", got, codes.PermissionDenied)
	}
	if got := status.Convert(err).Message(); got != "permission denied" {
		t.Fatalf("ClaimExecution() message = %q, want a generic permission denial", got)
	}
}

func TestClaimExecutionPreservesOtherErrors(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		code      codes.Code
		checkCode bool
	}{
		{name: "grpc status", err: status.Error(codes.Unavailable, "database unavailable"), code: codes.Unavailable, checkCode: true},
		{name: "context cancellation", err: context.Canceled},
		{name: "other error", err: errors.New("database unavailable"), code: codes.Unknown, checkCode: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := &Service{Claimer: executionClaimerFunc(func(context.Context, string, *ExecutionOwner) (bool, error) {
				return false, tc.err
			})}

			_, got := service.ClaimExecution(context.Background(), &ClaimExecutionRequest{TaskId: "task"})
			if !errors.Is(got, tc.err) {
				t.Fatalf("ClaimExecution() error = %v, want preserved %v", got, tc.err)
			}
			if tc.checkCode && status.Code(got) != tc.code {
				t.Fatalf("ClaimExecution() code = %s, want %s", status.Code(got), tc.code)
			}
		})
	}
}
