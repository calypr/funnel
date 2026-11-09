package testenv

import (
	"bytes"
	"context"
	"fmt"
	"text/template"
	"time"

	"google.golang.org/grpc"

	"github.com/calypr/funnel/api/tes"
	runlib "github.com/calypr/funnel/internal/cli/run"
	taskpolicy "github.com/calypr/funnel/internal/task"
)

// Changes the user who will be making the RPC calls.
// Panics if that user is not defined in the configuration.
func (f *Funnel) SwitchUser(username string) {
	for _, cred := range f.Conf.Server.BasicAuth {
		if cred.User == username {
			if f.Conf.RPCClient.Credential.User != username {
				f.Conf.RPCClient.Credential.User = cred.User
				f.Conf.RPCClient.Credential.Password = cred.Password
				f.addRPCClient()
			}
			return
		}
	}
	panic("Cannot switch to an undefined user: " + username)
}

const dockerDestroyWaitTimeout = 30 * time.Second

// Cancel cancels a task by ID
func (f *Funnel) Cancel(id string) error {
	_, err := f.RPC.CancelTask(context.Background(), &tes.CancelTaskRequest{
		Id: id,
	})
	return err
}

// GetView returns a task (calls GetTask) with the given view.
func (f *Funnel) GetView(id string, view tes.View) *tes.Task {
	t, err := f.RPC.GetTask(context.Background(), &tes.GetTaskRequest{
		Id:   id,
		View: view.String(),
	})
	if err != nil {
		panic(err)
	}
	return t
}

// Get gets a task by ID
func (f *Funnel) Get(id string) *tes.Task {
	return f.GetView(id, tes.View_FULL)
}

// Run uses `funnel run` syntax to create a task.
// Run panics on error.
func (f *Funnel) Run(s string) string {
	id, err := f.RunE(s)
	if err != nil {
		panic(err)
	}
	return id
}

// RunE is like Run(), but returns an error instead of panicing.
func (f *Funnel) RunE(s string) (string, error) {
	// Process the string as a template to allow a few helpers
	tpl := template.Must(template.New("run").Parse(s))
	var by bytes.Buffer
	data := map[string]string{
		"storage": f.StorageDir,
	}
	if eerr := tpl.Execute(&by, data); eerr != nil {
		return "", eerr
	}
	s = by.String()

	tasks, err := runlib.ParseString(s)
	if err != nil {
		return "", err
	}
	if len(tasks) > 1 {
		return "", fmt.Errorf("Funnel run only handles a single task (no scatter)")
	}
	return f.RunTask(tasks[0])
}

// RunTask calls CreateTask with the given task message and returns the ID.“
func (f *Funnel) RunTask(t *tes.Task) (string, error) {
	resp, cerr := f.RPC.CreateTask(context.Background(), t, grpc.WaitForReady(true))
	if cerr != nil {
		return "", cerr
	}
	return resp.Id, nil
}

// Wait waits for a task to complete.
func (f *Funnel) Wait(id string) *tes.Task {
	for range time.NewTicker(f.rate).C {
		t := f.Get(id)
		log.Info("waiting for task", "taskID", id)
		log.Info("taskState", "State?", t.State)
		if t.State != tes.State_QUEUED && t.State != tes.State_INITIALIZING &&
			t.State != tes.State_RUNNING {
			return t
		}
	}
	return nil
}

// WaitForInitializing waits for a task to be in the Initializing state
func (f *Funnel) WaitForInitializing(ids ...string) {
	for _, id := range ids {
		for range time.NewTicker(f.rate).C {
			t := f.Get(id)
			if t.State == tes.State_INITIALIZING {
				break
			}
		}
	}
}

// WaitForRunning waits for a task to be in the RUNNING state
func (f *Funnel) WaitForRunning(ids ...string) {
	for _, id := range ids {
		for range time.NewTicker(f.rate).C {
			t := f.Get(id)
			if t.State == tes.State_RUNNING {
				break
			}
		}
	}
}

// WaitForExec waits for a task to reach the given executor index.
// 1 is the first executor.
func (f *Funnel) WaitForExec(id string, i int) {
	for range time.NewTicker(f.rate).C {
		t := f.Get(id)
		if len(t.Logs[0].Logs) >= i {
			return
		}
	}
}

// HelloWorld is a simple, valid task that is easy to reuse in tests.
func HelloWorld() *tes.Task {
	return &tes.Task{
		Id: taskpolicy.GenerateID(),
		Executors: []*tes.Executor{
			{
				Image:   "alpine",
				Command: []string{"echo", "hello world"},
			},
		},
	}
}
