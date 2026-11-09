package boltdb

import (
	"context"
	"encoding/base64"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/server"
	"github.com/calypr/funnel/tes"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

func TestWriteEventRejectsCrossOwnerUpdates(t *testing.T) {
	db, err := NewBoltDB(&config.BoltDB{Path: filepath.Join(t.TempDir(), "tasks.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Init(); err != nil {
		t.Fatal(err)
	}
	auth := server.NewAuthentication([]*config.BasicCredential{{User: "alice", Password: "fixture"}, {User: "bob", Password: "fixture"}}, nil, server.AccessOwner)
	userContext := func(name string) context.Context {
		incoming := metadata.NewIncomingContext(t.Context(), metadata.Pairs("authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(name+":fixture"))))
		result, err := auth.Interceptor(incoming, nil, &grpc.UnaryServerInfo{}, func(ctx context.Context, _ interface{}) (interface{}, error) { return ctx, nil })
		if err != nil {
			t.Fatal(err)
		}
		return result.(context.Context)
	}
	alice, bob := userContext("alice"), userContext("bob")
	const id = "task-1"
	if err := db.WriteEvent(alice, events.NewTaskCreated(&tes.Task{Id: id, State: tes.Queued, Executors: []*tes.Executor{{Image: "example", Command: []string{"echo", "ok"}}}})); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, event := range []*events.Event{
		events.NewStartTime(id, 0, now), events.NewEndTime(id, 0, now),
		events.NewOutputs(id, 0, []*tes.OutputFileLog{{Path: "/result"}}), events.NewMetadata(id, 0, map[string]string{"key": "value"}),
		events.NewExecutorStartTime(id, 0, 0, now), events.NewExecutorEndTime(id, 0, 0, now), events.NewExitCode(id, 0, 0, 1),
		events.NewStdout(id, 0, 0, "output"), events.NewStderr(id, 0, 0, "error"), events.NewSystemLog(id, 0, 0, "INFO", "fixture", nil),
		events.NewResources(id, &tes.Resources{CpuCores: 2}), events.NewState(id, tes.Initializing),
	} {
		t.Run(event.Type.String(), func(t *testing.T) {
			before, err := db.GetTask(alice, &tes.GetTaskRequest{Id: id, View: "FULL"})
			if err != nil {
				t.Fatal(err)
			}
			if err := db.WriteEvent(bob, event); !errors.Is(err, tes.ErrNotPermitted) {
				t.Errorf("other owner write error = %v", err)
			}
			after, err := db.GetTask(alice, &tes.GetTaskRequest{Id: id, View: "FULL"})
			if err != nil {
				t.Fatal(err)
			}
			if !proto.Equal(before, after) {
				t.Error("rejected write changed the task")
			}
			if err := db.WriteEvent(alice, event); err != nil {
				t.Errorf("owner write rejected: %v", err)
			}
		})
	}
}
