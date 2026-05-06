package core

import (
	"context"
	"strings"
	"testing"

	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/server"
	"github.com/ohsu-comp-bio/funnel/tes"
	"github.com/ohsu-comp-bio/funnel/tests"
	"google.golang.org/grpc/status"
)

// These tests verify that access to tasks can be controlled through the
// configuration parameter: Server.TaskAccess (All, Owner, OwnerOrAdmin).

func TestTaskAccessAll(t *testing.T) {
	f := initServer(t, server.AccessAll)

	// STEP 1: Create a task for each user
	f.SwitchUser("User1")
	task1Id := f.Run(`--sh 'echo 1' --tag scope=TestTaskAccessAll`)
	f.SwitchUser("User2")
	task2Id := f.Run(`--sh 'echo 1' --tag scope=TestTaskAccessAll`)

	// STEP 2: Both users should see the tasks (get)
	checkTaskGet(t, f, "User1", task1Id, true)
	checkTaskGet(t, f, "User1", task2Id, true)

	checkTaskGet(t, f, "User2", task1Id, true)
	checkTaskGet(t, f, "User2", task2Id, true)

	// STEP 3: Both users should see the tasks (list)
	listTasksFilter := &tes.ListTasksRequest{
		TagKey:   []string{"scope"},
		TagValue: []string{"TestTaskAccessAll"},
	}

	checkTaskList(t, f, "User1", listTasksFilter, 2)
	checkTaskList(t, f, "User2", listTasksFilter, 2)

	// STEP 4: No user should get a permission denied error when cancelling the task
	checkTaskCancel(t, f, "User1", task1Id, true)
	checkTaskCancel(t, f, "User1", task2Id, true)

	checkTaskCancel(t, f, "User2", task1Id, true)
	checkTaskCancel(t, f, "User2", task2Id, true)
}

func TestTaskAccessOwner(t *testing.T) {
	f := initServer(t, server.AccessOwner)

	// STEP 1: Create a task for each user
	f.SwitchUser("User1")
	task1Id := f.Run(`--sh 'echo 1' --tag scope=TestTaskAccessOwner`)
	f.SwitchUser("User2")
	task2Id := f.Run(`--sh 'echo 1' --tag scope=TestTaskAccessOwner`)

	// STEP 2: Both users should see just their own tasks (get)
	checkTaskGet(t, f, "User1", task1Id, true)
	checkTaskGet(t, f, "User1", task2Id, false)

	checkTaskGet(t, f, "User2", task1Id, false)
	checkTaskGet(t, f, "User2", task2Id, true)

	// Even Admin-user cannot see the tasks:
	checkTaskGet(t, f, "Admin", task1Id, false)
	checkTaskGet(t, f, "Admin", task2Id, false)

	// STEP 3: Both users should see just their own tasks (list)
	listTasksFilter := &tes.ListTasksRequest{
		TagKey:   []string{"scope"},
		TagValue: []string{"TestTaskAccessOwner"},
	}

	checkTaskList(t, f, "User1", listTasksFilter, 1)
	checkTaskList(t, f, "User2", listTasksFilter, 1)
	checkTaskList(t, f, "Admin", listTasksFilter, 0)

	// STEP 4: Users get a permission denied error when cancelling a task of another user
	checkTaskCancel(t, f, "User1", task1Id, true)
	checkTaskCancel(t, f, "User1", task2Id, false)

	checkTaskCancel(t, f, "User2", task1Id, false)
	checkTaskCancel(t, f, "User2", task2Id, true)

	// Even Admin-user cannot cancel the tasks:
	checkTaskCancel(t, f, "Admin", task1Id, false)
	checkTaskCancel(t, f, "Admin", task2Id, false)
}

func TestTaskAccessOwnerOrAdmin(t *testing.T) {
	f := initServer(t, server.AccessOwnerOrAdmin)

	// STEP 1: Create a task for each user
	f.SwitchUser("User1")
	task1Id := f.Run(`--sh 'echo 1' --tag scope=TestTaskAccessOwnerOrAdmin`)
	f.SwitchUser("User2")
	task2Id := f.Run(`--sh 'echo 1' --tag scope=TestTaskAccessOwnerOrAdmin`)

	// STEP 2: Both users should see just their own tasks (get)
	checkTaskGet(t, f, "User1", task1Id, true)
	checkTaskGet(t, f, "User1", task2Id, false)

	checkTaskGet(t, f, "User2", task1Id, false)
	checkTaskGet(t, f, "User2", task2Id, true)

	// Admin-user can see ALL tasks:
	checkTaskGet(t, f, "Admin", task1Id, true)
	checkTaskGet(t, f, "Admin", task2Id, true)

	// STEP 3: Both users should see just their tasks (list)
	listTasksFilter := &tes.ListTasksRequest{
		TagKey:   []string{"scope"},
		TagValue: []string{"TestTaskAccessOwnerOrAdmin"},
	}

	checkTaskList(t, f, "User1", listTasksFilter, 1)
	checkTaskList(t, f, "User2", listTasksFilter, 1)
	checkTaskList(t, f, "Admin", listTasksFilter, 2)

	// STEP 4: Users get a permission denied error when cancelling a task of another user
	checkTaskCancel(t, f, "User1", task1Id, true)
	checkTaskCancel(t, f, "User1", task2Id, false)

	checkTaskCancel(t, f, "User2", task1Id, false)
	checkTaskCancel(t, f, "User2", task2Id, true)

	// Admin-user can cancel ALL tasks:
	checkTaskCancel(t, f, "Admin", task1Id, true)
	checkTaskCancel(t, f, "Admin", task2Id, true)
}

func initServer(t *testing.T, taskAccess string) *tests.Funnel {
	tests.SetLogOutput(log, t)

	c := tests.DefaultConfig()
	c.Compute = "noop"
	c.Server.TaskAccess = taskAccess

	c.Server.BasicAuth = []*config.BasicCredential{
		{User: "User1", Password: "user1-password"},
		{User: "User2", Password: "user2-password"},
		{User: "Admin", Password: "admin-password", Admin: true},
	}

	f := tests.NewFunnel(c)
	f.StartServer()
	return f
}

func checkTaskGet(t *testing.T, f *tests.Funnel, username string, taskId string, expectSuccess bool) {
	f.SwitchUser(username)

	// We are checking each task-view to be sure that view mode does not affect access.
	for _, view := range tes.View_name {
		t.Log("GetTask", taskId, "with", view, "view as", username)

		request := &tes.GetTaskRequest{Id: taskId, View: view}
		response, err := f.RPC.GetTask(context.Background(), request)

		if expectSuccess {
			if err != nil {
				t.Fatal("expected GetTask to succeed but got error:", err)
			} else if response.Id != taskId {
				t.Fatal("GetTask to returned a different task ID:", response.Id)
			}
		} else if err == nil {
			t.Fatal("expected GetTask to fail (permission denied) but there was no error", response)
		} else {
			checkPermissionDenied(t, err)
		}
	}
}

func checkTaskList(t *testing.T, f *tests.Funnel, username string, request *tes.ListTasksRequest, expectedCount int) {
	f.SwitchUser(username)
	t.Log("ListTasks as", username)

	response, err := f.RPC.ListTasks(context.Background(), request)

	if err != nil {
		t.Fatal("ListTasks returned an error:", err)
	}

	if len(response.Tasks) != expectedCount {
		t.Fatal("expected", expectedCount, "tasks, got", len(response.Tasks))
	}
}

func checkTaskCancel(t *testing.T, f *tests.Funnel, username string, taskId string, expectSuccess bool) {
	f.SwitchUser(username)
	t.Log("CancelTask", taskId, "as", username)

	_, err := f.RPC.CancelTask(context.Background(), &tes.CancelTaskRequest{Id: taskId})

	if expectSuccess {
		if err != nil {
			t.Fatal("expected CancelTask to fail with the state-change error but got:", err)
		}
	} else {
		checkPermissionDenied(t, err)
	}
}

func checkPermissionDenied(t *testing.T, err error) {
	s := status.Convert(err)
	if s == nil {
		t.Fatal("expected grpc status error but received:", err)
	}

	expectedPrefix := tes.ErrNotPermitted.Error()

	if !strings.HasPrefix(s.Message(), expectedPrefix) {
		t.Fatal("expected error-prefix [", expectedPrefix, "] but got:", s.Message())
	}
}
