package kubernetes

import (
	"context"
	"errors"
	"testing"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/logger"
	"github.com/calypr/funnel/tes"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

type failureRetryWriter struct {
	task    *tes.Task
	fail    bool
	failLog bool
}

func (w *failureRetryWriter) WriteEvent(_ context.Context, e *events.Event) error {
	if e.Type == events.Type_SYSTEM_LOG && w.failLog {
		return errors.New("log database unavailable")
	}
	if e.Type == events.Type_TASK_STATE {
		if w.fail {
			return errors.New("database unavailable")
		}
		w.task.State = e.GetState()
	}
	return nil
}

func TestWriteSystemErrorRetriesFailedDiagnostic(t *testing.T) {
	task := &tes.Task{Id: "task-1", State: tes.Queued}
	writer := &failureRetryWriter{task: task, failLog: true}
	b := &Backend{event: writer, database: &mockReadOnlyServer{tasks: []*tes.Task{task}}, log: logger.NewLogger("test", logger.DefaultConfig())}
	if b.writeSystemError(context.Background(), task.Id, nil, "failed to start") {
		t.Fatal("failure reporting succeeded without its diagnostic")
	}
	if task.State != tes.Queued {
		t.Fatalf("state = %s, want queued until diagnostic can be recorded", task.State)
	}
	writer.failLog = false
	if !b.writeSystemError(context.Background(), task.Id, nil, "failed to start") || task.State != tes.SystemError {
		t.Fatal("failure reporting did not succeed on retry")
	}
}
func (*failureRetryWriter) Close() {}

func TestReconcileRetainsJobUntilFailureRecorded(t *testing.T) {
	const id = "test-task"
	const ns = "test-ns"
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: id, Namespace: ns}, Status: batchv1.JobStatus{Active: 1}}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod", Namespace: ns, Labels: map[string]string{"job-name": id}}, Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{Name: "worker", State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CreateContainerConfigError", Message: "invalid config"}}}}}}
	client := fake.NewSimpleClientset(job, pod)
	conf := config.DefaultConfig()
	conf.Kubernetes.JobsNamespace = ns
	conf.Kubernetes.Namespace = ns
	task := &tes.Task{Id: id, State: tes.State_QUEUED}
	writer := &failureRetryWriter{task: task, fail: true}
	b := &Backend{client: client, event: writer, database: &mockReadOnlyServer{tasks: []*tes.Task{task}}, log: logger.NewLogger("test", logger.DefaultConfig()), conf: conf}
	b.reconcileJob(context.Background(), job, false)
	for _, a := range client.Actions() {
		if a.GetVerb() == "delete" && a.GetResource().Resource == "jobs" {
			t.Fatal("deleted job before recording failure")
		}
	}
	writer.fail = false
	b.reconcileJob(context.Background(), job, false)
	if task.State != tes.State_SYSTEM_ERROR {
		t.Fatalf("state = %s, want SYSTEM_ERROR", task.State)
	}
	for _, a := range client.Actions() {
		if a.GetVerb() == "delete" && a.GetResource().Resource == "jobs" {
			return
		}
	}
	t.Fatal("job not deleted after failure was recorded")
}

func TestBacklogRetainsFailedJobUntilFailureRecorded(t *testing.T) {
	const id = "failed-task"
	const ns = "test-ns"
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: id, Namespace: ns, Labels: map[string]string{"app": "funnel-worker"}},
		Status:     batchv1.JobStatus{Failed: 1, Conditions: []batchv1.JobCondition{{Type: batchv1.JobFailed, Status: corev1.ConditionTrue}}},
	}
	client := fake.NewSimpleClientset(job)
	conf := config.DefaultConfig()
	conf.Kubernetes.JobsNamespace = ns
	conf.Kubernetes.Namespace = ns
	task := &tes.Task{Id: id, State: tes.Queued}
	writer := &failureRetryWriter{task: task, fail: true}
	b := &Backend{client: client, event: writer, database: &mockReadOnlyServer{tasks: []*tes.Task{task}}, log: logger.NewLogger("test", logger.DefaultConfig()), conf: conf}
	b.cleanBacklog(context.Background(), false)
	for _, action := range client.Actions() {
		if action.GetVerb() == "delete" && action.GetResource().Resource == "jobs" {
			t.Fatal("backlog deleted job before recording failure")
		}
	}
	writer.fail = false
	b.cleanBacklog(context.Background(), false)
	if task.State != tes.SystemError {
		t.Fatalf("state = %s, want SYSTEM_ERROR", task.State)
	}
	for _, action := range client.Actions() {
		if action.GetVerb() == "delete" && action.GetResource().Resource == "jobs" {
			return
		}
	}
	t.Fatal("backlog did not delete job after failure was recorded")
}
