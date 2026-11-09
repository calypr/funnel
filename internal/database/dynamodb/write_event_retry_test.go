package dynamodb

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"

	serverauth "github.com/calypr/funnel/internal/server/auth"

	"github.com/calypr/funnel/api/tes"
	"github.com/calypr/funnel/internal/events"
	"github.com/calypr/funnel/internal/identity"
	taskpolicy "github.com/calypr/funnel/internal/task"
	"google.golang.org/grpc"
)

func TestWriteEventReturnsTerminalStateErrorsDirectly(t *testing.T) {
	tests := []struct {
		name      string
		response  string
		ctx       context.Context
		wantErr   error
		wantState bool
	}{
		{
			name:     "missing task",
			response: `{}`,
			wantErr:  taskpolicy.ErrNotFound,
		},
		{
			name:     "inaccessible task",
			response: `{"Item":{"id":{"S":"task-1"},"owner":{"S":"alice"}}}`,
			ctx:      restrictedContext(t),
			wantErr:  taskpolicy.ErrNotPermitted,
		},
		{
			name: "invalid transition",
			response: fmt.Sprintf(`{"Item":{"id":{"S":"task-1"},"owner":{"S":"alice"},"state":{"N":"%d"},"version":{"N":"0"}}}`,
				tes.State_CANCELED),
			wantState: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var reads, updates atomic.Int32
			db := fixtureDB(t, func(operation string, _ map[string]json.RawMessage) string {
				switch operation {
				case "GetItem":
					reads.Add(1)
					return tt.response
				case "UpdateItem":
					updates.Add(1)
					return `{}`
				default:
					t.Errorf("unexpected DynamoDB operation %s", operation)
					return `{}`
				}
			})

			ctx := tt.ctx
			if ctx == nil {
				ctx = t.Context()
			}
			err := db.WriteEvent(ctx, events.NewState("task-1", tes.State_RUNNING))
			if tt.wantState {
				transitionErr, ok := err.(*taskpolicy.TransitionError)
				if !ok || transitionErr.From != tes.State_CANCELED || transitionErr.To != tes.State_RUNNING {
					t.Fatalf("transition error = %#v, want direct *taskpolicy.TransitionError from CANCELED to RUNNING", err)
				}
			} else if err != tt.wantErr {
				t.Fatalf("WriteEvent error = %v, want original sentinel %v", err, tt.wantErr)
			}
			if reads.Load() != 1 || updates.Load() != 0 {
				t.Fatalf("GetItem calls = %d and UpdateItem calls = %d, want 1 and 0", reads.Load(), updates.Load())
			}
		})
	}
}

func restrictedContext(t *testing.T) context.Context {
	t.Helper()
	var restricted context.Context
	_, err := serverauth.NewAuthentication(nil, nil, identity.AccessOwner, nil).Interceptor(
		t.Context(),
		nil,
		&grpc.UnaryServerInfo{FullMethod: "/tes.TaskService/GetTask"},
		func(ctx context.Context, _ interface{}) (interface{}, error) {
			restricted = ctx
			return nil, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return restricted
}
