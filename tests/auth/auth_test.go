package auth

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/tes"
	"github.com/ohsu-comp-bio/funnel/tests"
)

var extask = &tes.Task{
	Executors: []*tes.Executor{
		{
			Image:   "alpine",
			Command: []string{"echo", "hello world"},
		},
	},
}

func TestBasicAuthFail(t *testing.T) {
	tests.ParseConfig()
	ctx := context.Background()
	conf := tests.DefaultConfig()
	conf.Server.BasicAuth = []*config.BasicCredential{{User: "funnel", Password: "abc123"}}
	fun := tests.NewFunnel(conf)
	fun.StartServer()

	// HTTP client
	_, err := fun.HTTP.GetTask(ctx, &tes.GetTaskRequest{
		Id:   "1",
		View: tes.View_MINIMAL.String(),
	})
	if err == nil || !strings.Contains(err.Error(), "STATUS CODE - 401") {
		t.Fatal("expected error", err)
	}

	_, err = fun.HTTP.ListTasks(ctx, &tes.ListTasksRequest{
		View: tes.View_MINIMAL.String(),
	})
	if err == nil || !strings.Contains(err.Error(), "STATUS CODE - 401") {
		t.Fatal("expected error", err)
	}

	_, err = fun.HTTP.CreateTask(ctx, extask)
	if err == nil || !strings.Contains(err.Error(), "STATUS CODE - 401") {
		t.Fatal("expected error", err)
	}

	_, err = fun.HTTP.CancelTask(ctx, &tes.CancelTaskRequest{
		Id: "1",
	})
	if err == nil || !strings.Contains(err.Error(), "STATUS CODE - 401") {
		t.Fatal("expected error", err)
	}

	// RPC client
	_, err = fun.RPC.GetTask(ctx, &tes.GetTaskRequest{
		Id:   "1",
		View: tes.View_MINIMAL.String(),
	})
	if err == nil || !strings.Contains(err.Error(), "Unauthenticated") {
		t.Fatal("expected error", err)
	}

	_, err = fun.RPC.ListTasks(ctx, &tes.ListTasksRequest{
		View: tes.View_MINIMAL.String(),
	})
	if err == nil || !strings.Contains(err.Error(), "Unauthenticated") {
		t.Fatal("expected error", err)
	}

	_, err = fun.RPC.CreateTask(ctx, tests.HelloWorld())
	if err == nil || !strings.Contains(err.Error(), "Unauthenticated") {
		t.Fatal("expected error", err)
	}

	_, err = fun.RPC.CancelTask(ctx, &tes.CancelTaskRequest{
		Id: "1",
	})
	if err == nil || !strings.Contains(err.Error(), "Unauthenticated") {
		t.Fatal("expected error", err)
	}
}

func TestBasicAuthed(t *testing.T) {
	os.Setenv("FUNNEL_SERVER_USER", "funnel")
	os.Setenv("FUNNEL_SERVER_PASSWORD", "abc123")
	defer os.Unsetenv("FUNNEL_SERVER_USER")
	defer os.Unsetenv("FUNNEL_SERVER_PASSWORD")

	conf := tests.DefaultConfig()
	conf.Server.BasicAuth = []*config.BasicCredential{{User: "funnel", Password: "abc123"}}
	conf.RPCClient.Credential.User = "funnel"
	conf.RPCClient.Credential.Password = "abc123"
	fun := tests.NewFunnel(conf)
	fun.StartServer()

	var err error

	// Run a task to completion
	id2 := fun.Run(`--sh 'echo hello'`)
	t2 := fun.Wait(id2)
	if t2.State != tes.State_COMPLETE {
		t.Fatal("expected task to complete")
	}

	// HTTP client
	_, err = fun.HTTP.GetTask(context.Background(), &tes.GetTaskRequest{
		Id:   id2,
		View: tes.View_MINIMAL.String(),
	})
	if err != nil {
		t.Fatal("unexpected error:", err)
	}

	_, err = fun.HTTP.ListTasks(context.Background(), &tes.ListTasksRequest{
		View: tes.View_MINIMAL.String(),
	})
	if err != nil {
		t.Fatal("unexpected error:", err)
	}

	resp, err := fun.HTTP.CreateTask(context.Background(), extask)
	if err != nil {
		t.Fatal("unexpected error:", err)
	}

	_, err = fun.HTTP.CancelTask(context.Background(), &tes.CancelTaskRequest{
		Id: resp.Id,
	})
	if err != nil {
		t.Fatal("unexpected error:", err)
	}

	// RPC client
	_, err = fun.RPC.GetTask(context.Background(), &tes.GetTaskRequest{
		Id:   id2,
		View: tes.View_MINIMAL.String(),
	})
	if err != nil {
		t.Fatal("unexpected error:", err)
	}

	_, err = fun.RPC.ListTasks(context.Background(), &tes.ListTasksRequest{
		View: tes.View_MINIMAL.String(),
	})
	if err != nil {
		t.Fatal("unexpected error:", err)
	}

	resp, err = fun.RPC.CreateTask(context.Background(), extask)
	if err != nil {
		t.Fatal("unexpected error:", err)
	}

	_, err = fun.RPC.CancelTask(context.Background(), &tes.CancelTaskRequest{
		Id: resp.Id,
	})
	if err != nil {
		t.Fatal("unexpected error:", err)
	}
}
