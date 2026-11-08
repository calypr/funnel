package badger

import (
	"context"
	"testing"

	"github.com/calypr/funnel/api/tes"
	"github.com/calypr/funnel/internal/events"
	"github.com/calypr/funnel/internal/server"
	badger "github.com/dgraph-io/badger/v4"
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
	alice := context.WithValue(t.Context(), server.UserInfoKey, &server.UserInfo{Username: "alice"})
	bob := context.WithValue(t.Context(), server.UserInfoKey, &server.UserInfo{Username: "bob"})
	if err := db.WriteEvent(alice, events.NewTaskCreated(&tes.Task{Id: "task-1", Name: "original"})); err != nil {
		t.Fatal(err)
	}
	if err := db.WriteEvent(bob, events.NewTaskCreated(&tes.Task{Id: "task-1", Name: "replacement"})); err == nil {
		t.Error("duplicate task creation succeeded")
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
