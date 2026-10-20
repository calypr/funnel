package kubernetes

import (
	"context"
	"testing"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/logger"
	"github.com/calypr/funnel/tes"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

const reconcileTestNamespace = "worker-jobs"

type reconcileTaskDatabase struct {
	task              *tes.Task
	transitionBetween bool
}

func (d *reconcileTaskDatabase) ListTasks(_ context.Context, req *tes.ListTasksRequest) (*tes.ListTasksResponse, error) {
	if d.transitionBetween {
		switch req.State {
		case tes.State_INITIALIZING:
			d.task.State = tes.State_INITIALIZING
			return &tes.ListTasksResponse{Tasks: []*tes.Task{d.task}}, nil
		case tes.State_RUNNING:
			d.task.State = tes.State_RUNNING
			return &tes.ListTasksResponse{Tasks: []*tes.Task{d.task}}, nil
		default:
			return &tes.ListTasksResponse{}, nil
		}
	}
	if d.task.State == req.State {
		return &tes.ListTasksResponse{Tasks: []*tes.Task{d.task}}, nil
	}
	return &tes.ListTasksResponse{}, nil
}

func (d *reconcileTaskDatabase) GetTask(_ context.Context, _ *tes.GetTaskRequest) (*tes.Task, error) {
	return d.task, nil
}

func (*reconcileTaskDatabase) Close() {}

type reconcileStateEvents struct {
	states []tes.State
}

func (w *reconcileStateEvents) WriteEvent(_ context.Context, ev *events.Event) error {
	if ev.Type == events.Type_TASK_STATE {
		w.states = append(w.states, ev.GetState())
	}
	return nil
}

func (*reconcileStateEvents) Close() {}

func (w *reconcileStateEvents) hasSystemError() bool {
	for _, state := range w.states {
		if state == tes.State_SYSTEM_ERROR {
			return true
		}
	}
	return false
}

func newReconcileTestBackend(client kubernetes.Interface, database tes.ReadOnlyServer, writer events.Writer) *Backend {
	conf := config.DefaultConfig()
	conf.Kubernetes.Namespace = reconcileTestNamespace
	conf.Kubernetes.JobsNamespace = reconcileTestNamespace
	return &Backend{
		client:   client,
		event:    writer,
		database: database,
		log:      logger.NewLogger("reconcile-race-test", logger.DefaultConfig()),
		conf:     conf,
	}
}

func reconcileTestJob(taskID string, status batchv1.JobStatus) *batchv1.Job {
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      taskID,
			Namespace: reconcileTestNamespace,
			Labels:    map[string]string{"app": "funnel-worker"},
		},
		Status: status,
	}
}

func TestReconcileDoesNotFailTaskListedInTwoNonterminalStates(t *testing.T) {
	const taskID = "task-state-transition"
	ctx := context.Background()
	job := reconcileTestJob(taskID, batchv1.JobStatus{Succeeded: 1})
	client := fake.NewSimpleClientset(job)
	database := &reconcileTaskDatabase{
		task:              &tes.Task{Id: taskID},
		transitionBetween: true,
	}
	writer := &reconcileStateEvents{}
	backend := newReconcileTestBackend(client, database, writer)

	backend.reconcileOnce(ctx, false)

	if writer.hasSystemError() {
		t.Fatal("task listed as initializing then running was marked SYSTEM_ERROR after its completed Job was cleaned")
	}
}

func TestReconcileFindsJobCreatedAfterInitialList(t *testing.T) {
	const taskID = "task-created-after-list"
	ctx := context.Background()
	job := reconcileTestJob(taskID, batchv1.JobStatus{Active: 1})
	client := fake.NewSimpleClientset()
	client.PrependReactor("list", "jobs", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, &batchv1.JobList{}, nil
	})
	client.PrependReactor("get", "jobs", func(action k8stesting.Action) (bool, runtime.Object, error) {
		get := action.(k8stesting.GetAction)
		if get.GetName() == taskID {
			return true, job, nil
		}
		return false, nil, nil
	})
	database := &reconcileTaskDatabase{task: &tes.Task{Id: taskID, State: tes.State_RUNNING}}
	writer := &reconcileStateEvents{}
	backend := newReconcileTestBackend(client, database, writer)

	backend.reconcileOnce(ctx, true)

	if writer.hasSystemError() {
		t.Fatal("active Job found after the initial list was treated as missing")
	}
}

func TestReconcileDoesNotFailQueuedTaskWithoutJob(t *testing.T) {
	const taskID = "queued-task-without-job"
	ctx := context.Background()
	client := fake.NewSimpleClientset()
	database := &reconcileTaskDatabase{task: &tes.Task{Id: taskID, State: tes.State_QUEUED}}
	writer := &reconcileStateEvents{}
	backend := newReconcileTestBackend(client, database, writer)

	backend.reconcileOnce(ctx, true)

	if writer.hasSystemError() {
		t.Fatal("queued task without a Job was marked SYSTEM_ERROR before submission")
	}
}

func TestReconcileStillFailsRunningTaskWithoutJob(t *testing.T) {
	const taskID = "running-task-without-job"
	ctx := context.Background()
	client := fake.NewSimpleClientset()
	database := &reconcileTaskDatabase{task: &tes.Task{Id: taskID, State: tes.State_RUNNING}}
	writer := &reconcileStateEvents{}
	backend := newReconcileTestBackend(client, database, writer)

	backend.reconcileOnce(ctx, true)

	if !writer.hasSystemError() {
		t.Fatal("running task with no Job was not marked SYSTEM_ERROR")
	}
}
