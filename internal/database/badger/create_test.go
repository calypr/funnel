package badger

import (
	"context"
	"testing"

	serverauth "github.com/calypr/funnel/internal/server/auth"

	"github.com/calypr/funnel/api/tes"
	"github.com/calypr/funnel/internal/events"
	"github.com/calypr/funnel/internal/identity"
	taskpolicy "github.com/calypr/funnel/internal/task"
	badger "github.com/dgraph-io/badger/v4"
	"google.golang.org/grpc"
)

func TestWriteEventRejectsDuplicateTask(t *testing.T) {
	store, err := badger.Open(badger.DefaultOptions("").WithInMemory(true).WithLogger(nil))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	}()
	db := &Badger{db: store}
	alice := identity.WithUser(t.Context(), &identity.UserInfo{Username: "alice"})
	bob := identity.WithUser(t.Context(), &identity.UserInfo{Username: "bob"})
	if err := db.WriteEvent(alice, events.NewTaskCreated(&tes.Task{Id: "task-1", Name: "original"})); err != nil {
		t.Fatal(err)
	}
	if err := db.WriteEvent(bob, events.NewTaskCreated(&tes.Task{Id: "task-1", Name: "replacement"})); err != errTaskExists {
		t.Errorf("duplicate task error = %v, want original sentinel %v", err, errTaskExists)
	}
	task, err := db.GetTask(t.Context(), &tes.GetTaskRequest{Id: "task-1"})
	if err != nil {
		t.Fatal(err)
	}
	if task.Name != "original" {
		t.Error("duplicate creation replaced the task")
	}
	if err := store.View(func(txn *badger.Txn) error {
		owner, err := getTaskOwner(txn, ownerKey("task-1"))
		if err != nil {
			return err
		}
		if owner != "alice" {
			t.Error("duplicate creation replaced the owner")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestWriteEventReturnsTerminalErrorsDirectly(t *testing.T) {
	store, err := badger.Open(badger.DefaultOptions("").WithInMemory(true).WithLogger(nil))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	}()
	db := &Badger{db: store}
	alice := identity.WithUser(t.Context(), &identity.UserInfo{Username: "alice"})
	if err := db.WriteEvent(alice, events.NewTaskCreated(&tes.Task{Id: "task-1", State: tes.State_QUEUED})); err != nil {
		t.Fatal(err)
	}

	if err := db.WriteEvent(alice, events.NewState("missing", tes.State_RUNNING)); err != taskpolicy.ErrNotFound {
		t.Errorf("missing task error = %v, want original sentinel %v", err, taskpolicy.ErrNotFound)
	}
	if err := db.WriteEvent(restrictedContext(t), events.NewState("task-1", tes.State_RUNNING)); err != taskpolicy.ErrNotPermitted {
		t.Errorf("permission error = %v, want original sentinel %v", err, taskpolicy.ErrNotPermitted)
	}
	transitionErr := db.WriteEvent(alice, events.NewState("task-1", tes.State_PREEMPTED))
	if transitionErr == nil {
		t.Fatal("invalid state transition succeeded")
	}
	if _, ok := transitionErr.(*taskpolicy.TransitionError); !ok {
		t.Errorf("transition error type = %T, want *taskpolicy.TransitionError", transitionErr)
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
