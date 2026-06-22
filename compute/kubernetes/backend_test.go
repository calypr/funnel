// Package kubernetes contains code for accessing compute resources via the Kubernetes v1 Batch API.
package kubernetes

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/events"
	"github.com/ohsu-comp-bio/funnel/logger"
	"github.com/ohsu-comp-bio/funnel/tes"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// mockDatabase implements tes.ReadOnlyServer for testing CleanOrphanedResources.
type mockDatabase struct {
	tasks map[string]*tes.Task
}

func (m *mockDatabase) GetTask(_ context.Context, req *tes.GetTaskRequest) (*tes.Task, error) {
	t, ok := m.tasks[req.Id]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return t, nil
}

func (m *mockDatabase) ListTasks(_ context.Context, _ *tes.ListTasksRequest) (*tes.ListTasksResponse, error) {
	return &tes.ListTasksResponse{}, nil
}

func (m *mockDatabase) Close() {}

// noopEventWriter implements events.Writer for testing.
type noopEventWriter struct{}

func (n *noopEventWriter) WriteEvent(ctx context.Context, ev *events.Event) error {
	return nil
}

func (n *noopEventWriter) Close() {}

const (
	configMapTemplate = `apiVersion: v1
kind: ConfigMap
metadata:
  name: funnel-worker-config-{{.TaskId}}
  namespace: {{.Namespace}}
data:
  config.yaml: "placeholder"
`
	serviceAccountTemplate = `apiVersion: v1
kind: ServiceAccount
metadata:
  name: funnel-worker-sa-{{.Namespace}}-{{.TaskId}}
  namespace: {{.Namespace}}
  labels:
    app: funnel
    taskId: {{.TaskId}}
`
	roleTemplate = `apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: funnel-worker-sa-{{.Namespace}}-{{.TaskId}}-role
  namespace: {{.Namespace}}
  labels:
    app: funnel
    taskId: {{.TaskId}}
rules: []
`
	roleBindingTemplate = `apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: funnel-worker-sa-{{.Namespace}}-{{.TaskId}}-binding
  namespace: {{.Namespace}}
  labels:
    app: funnel
    taskId: {{.TaskId}}
subjects:
- kind: ServiceAccount
  name: funnel-worker-sa-{{.Namespace}}-{{.TaskId}}
  namespace: {{.Namespace}}
roleRef:
  kind: Role
  name: funnel-worker-sa-{{.Namespace}}-{{.TaskId}}-role
  apiGroup: rbac.authorization.k8s.io
`
)

func applyKubernetesTemplates(conf *config.Config) {
	conf.Kubernetes.ConfigMapTemplate = configMapTemplate
	conf.Kubernetes.ServiceAccountTemplate = serviceAccountTemplate
	conf.Kubernetes.RoleTemplate = roleTemplate
	conf.Kubernetes.RoleBindingTemplate = roleBindingTemplate
}

func TestTaskSubmission(t *testing.T) {
	// Create a fake Kubernetes client
	fakeClient := fake.NewSimpleClientset()

	// Create a mock configuration
	conf := config.DefaultConfig()
	conf.Kubernetes.Namespace = "test-namespace"
	conf.Kubernetes.JobsNamespace = "test-namespace"
	conf.Kubernetes.WorkerTemplate = `
apiVersion: batch/v1
kind: Job
metadata:
  name: {{.TaskId}}
  namespace: {{.JobsNamespace}}
spec:
  template:
    spec:
      restartPolicy: Never
      containers:
      - name: task
        image: alpine
        command: ["echo", "hello world"]
        resources:
          requests:
            cpu: "{{.Cpus}}"
            memory: "{{.RamGb}}Gi"
            ephemeral-storage: "{{.DiskGb}}Gi"
`
	applyKubernetesTemplates(conf)

	// Create a logger
	log := logger.NewLogger("test", logger.DefaultConfig())

	backend := &Backend{
		client:   fakeClient,
		event:    &noopEventWriter{},
		database: nil,
		log:      log,
		conf:     conf, // Funnel configuration
	}

	// Define a test task
	task := &tes.Task{
		Id: "test-task",
		Resources: &tes.Resources{
			CpuCores: 1,
			RamGb:    1.0,
			DiskGb:   10.0,
		},
		Executors: []*tes.Executor{
			{
				Image:   "alpine",
				Command: []string{"echo", "hello world"},
			},
		},
	}

	// Submit the task to the backend
	err := backend.Submit(context.Background(), task, conf)
	if err != nil {
		t.Fatalf("failed to submit task: %v", err)
	}

	// Verify that the Job was created
	job, err := fakeClient.BatchV1().Jobs(conf.Kubernetes.JobsNamespace).Get(context.Background(), task.Id, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get Job: %v", err)
	}

	if job.Name != task.Id {
		t.Errorf("expected Job name '%s', got '%s'", task.Id, job.Name)
	}

	// Seed a PV so we can verify cleanResources deletes it.
	pvName := "funnel-worker-pv-" + task.Id
	_, err = fakeClient.CoreV1().PersistentVolumes().Create(context.Background(), &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: pvName,
			Labels: map[string]string{
				"app":       "funnel",
				"taskId":    task.Id,
				"namespace": conf.Kubernetes.JobsNamespace,
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("failed to create test PV: %v", err)
	}

	// Clean up resources
	err = backend.cleanResources(context.Background(), task.Id)
	if err != nil {
		t.Fatalf("failed to clean resources: %v", err)
	}

	// Verify that the Job was deleted
	_, err = fakeClient.BatchV1().Jobs(conf.Kubernetes.JobsNamespace).Get(context.Background(), task.Id, metav1.GetOptions{})
	if err == nil {
		t.Error("expected Job to be deleted, but it still exists")
	}

	// Verify that the PV was deleted
	_, err = fakeClient.CoreV1().PersistentVolumes().Get(context.Background(), pvName, metav1.GetOptions{})
	if err == nil {
		t.Error("expected PV to be deleted, but it still exists")
	}
}

func TestSubmit_AppliesNodeSelectorAndTolerationsToWorkerJob(t *testing.T) {
	fakeClient := fake.NewSimpleClientset()

	// Create a fake funnel server pod so CreateJob can resolve worker image.
	funnelPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "funnel-server",
			Namespace: "test-namespace",
			Labels: map[string]string{
				"app": "funnel",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "funnel", Image: "alpine"},
			},
		},
	}
	if _, err := fakeClient.CoreV1().Pods("test-namespace").Create(context.Background(), funnelPod, metav1.CreateOptions{}); err != nil {
		t.Fatalf("failed to create fake funnel pod: %v", err)
	}

	conf := config.DefaultConfig()
	conf.Compute = "kubernetes"
	conf.Kubernetes.Namespace = "test-namespace"
	conf.Kubernetes.JobsNamespace = "test-namespace"
	conf.Kubernetes.NodeSelector = map[string]string{
		"pool": "cpu",
		"zone": "us-west-2a",
	}
	conf.Kubernetes.Tolerations = []*config.Toleration{
		{
			Key:      "dedicated",
			Operator: "Equal",
			Value:    "worker",
			Effect:   "NoSchedule",
		},
	}

	applyKubernetesTemplates(conf)

	// Include scheduling blocks in the worker template under test.
	conf.Kubernetes.WorkerTemplate = `
apiVersion: batch/v1
kind: Job
metadata:
  name: funnel-{{.TaskId}}
  namespace: {{.JobsNamespace}}
spec:
  template:
    spec:
      {{- if .NodeSelector }}
      nodeSelector:
        {{- range $key, $value := .NodeSelector }}
        {{ $key }}: {{ $value }}
        {{- end }}
      {{- end }}
      {{- if .Tolerations }}
      tolerations:
        {{- range .Tolerations }}
        - key: {{ .Key }}
          operator: {{ .Operator }}
          effect: {{ .Effect }}
          {{if .Value}}value: {{ .Value }}{{end}}
        {{- end }}
      {{- end }}
      restartPolicy: Never
      containers:
      - name: worker
        image: alpine
        command: ["echo", "ok"]
`

	log := logger.NewLogger("test", logger.DefaultConfig())
	backend := &Backend{
		client: fakeClient,
		log:    log,
		conf:   conf,
		event:  &noopEventWriter{},
	}

	task := &tes.Task{
		Id: "sched-worker",
		Resources: &tes.Resources{
			CpuCores: 1,
			RamGb:    1,
			DiskGb:   1,
		},
		Executors: []*tes.Executor{
			{
				Image:   "alpine",
				Command: []string{"echo", "ok"},
			},
		},
	}

	if err := backend.Submit(context.Background(), task, conf); err != nil {
		t.Fatalf("submit failed: %v", err)
	}

	job, err := fakeClient.BatchV1().Jobs(conf.Kubernetes.JobsNamespace).Get(context.Background(), "funnel-"+task.Id, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get worker job: %v", err)
	}

	if !reflect.DeepEqual(job.Spec.Template.Spec.NodeSelector, conf.Kubernetes.NodeSelector) {
		t.Fatalf("nodeSelector mismatch: got=%v want=%v", job.Spec.Template.Spec.NodeSelector, conf.Kubernetes.NodeSelector)
	}

	if len(job.Spec.Template.Spec.Tolerations) != 1 {
		t.Fatalf("unexpected tolerations length: got=%d want=1", len(job.Spec.Template.Spec.Tolerations))
	}

	gotTol := job.Spec.Template.Spec.Tolerations[0]
	if gotTol.Key != "dedicated" {
		t.Fatalf("unexpected toleration key: got=%q want=%q", gotTol.Key, "dedicated")
	}
	if gotTol.Operator != corev1.TolerationOperator("Equal") {
		t.Fatalf("unexpected toleration operator: got=%q want=%q", gotTol.Operator, corev1.TolerationOperator("Equal"))
	}
	if gotTol.Effect != corev1.TaintEffect("NoSchedule") {
		t.Fatalf("unexpected toleration effect: got=%q want=%q", gotTol.Effect, corev1.TaintEffect("NoSchedule"))
	}
	if gotTol.Value != "worker" {
		t.Fatalf("unexpected toleration value: got=%q want=%q", gotTol.Value, "worker")
	}
}

// TestCancel_SAInUse verifies that Cancel succeeds even when the task's
// ServiceAccount is still attached to a running pod (the foreground-deleted
// Job's pod hasn't terminated yet). The SA should be left in place and not
// cause Cancel to return an error.
func TestCancel_SAInUse(t *testing.T) {
	const taskID = "cancel-test-task"
	const ns = "test-namespace"

	fakeClient := fake.NewSimpleClientset()
	ctx := context.Background()

	// Pre-create a worker Job so DeleteJob has something to delete.
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: taskID, Namespace: ns},
	}
	if _, err := fakeClient.BatchV1().Jobs(ns).Create(ctx, job, metav1.CreateOptions{}); err != nil {
		t.Fatalf("creating job: %v", err)
	}

	saName := fmt.Sprintf("funnel-worker-sa-%s-%s", ns, taskID)

	// Pre-create the SA with the label DeleteServiceAccount queries by.
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      saName,
			Namespace: ns,
			Labels:    map[string]string{"app": "funnel", "taskId": taskID},
		},
	}
	if _, err := fakeClient.CoreV1().ServiceAccounts(ns).Create(ctx, sa, metav1.CreateOptions{}); err != nil {
		t.Fatalf("creating SA: %v", err)
	}

	// Simulate a still-running worker pod attached to the SA (the situation
	// that arises immediately after foreground Job deletion).
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: taskID + "-pod", Namespace: ns},
		Spec:       corev1.PodSpec{ServiceAccountName: saName},
	}
	if _, err := fakeClient.CoreV1().Pods(ns).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		t.Fatalf("creating pod: %v", err)
	}

	// fake.NewSimpleClientset doesn't enforce field selectors, so we inject a
	// reactor that returns the pod when isServiceAccountAttachedToPods queries
	// by spec.serviceAccountName, ensuring the in-use path is exercised.
	fakeClient.PrependReactor("list", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, &corev1.PodList{Items: []corev1.Pod{*pod}}, nil
	})

	conf := config.DefaultConfig()
	conf.Kubernetes.Namespace = ns
	conf.Kubernetes.JobsNamespace = ns
	conf.Kubernetes.WorkerTemplate = "placeholder"

	backend := &Backend{
		client: fakeClient,
		log:    logger.NewLogger("test", logger.DefaultConfig()),
		conf:   conf,
		event:  &noopEventWriter{},
	}

	// Cancel must succeed even though the SA is still in use.
	if err := backend.Cancel(ctx, taskID); err != nil {
		t.Fatalf("Cancel returned unexpected error: %v", err)
	}

	// Job should be gone.
	_, err := fakeClient.BatchV1().Jobs(ns).Get(ctx, taskID, metav1.GetOptions{})
	if err == nil {
		t.Error("expected Job to be deleted after Cancel")
	}

	// SA must still exist — it was skipped, not deleted.
	_, err = fakeClient.CoreV1().ServiceAccounts(ns).Get(ctx, saName, metav1.GetOptions{})
	if err != nil {
		t.Errorf("expected SA to remain while pod is still running, got: %v", err)
	}
}

// TestHasTerminalContainerWaitingError verifies that hasTerminalContainerWaitingError
// correctly detects pods stuck in terminal waiting states (e.g. CreateContainerConfigError).
func TestHasTerminalContainerWaitingError(t *testing.T) {
	const ns = "test-namespace"

	tests := []struct {
		name           string
		containerState corev1.ContainerState
		initState      corev1.ContainerState
		wantTerminal   bool
		wantReasonPart string
	}{
		{
			name: "CreateContainerConfigError is terminal",
			containerState: corev1.ContainerState{
				Waiting: &corev1.ContainerStateWaiting{
					Reason:  "CreateContainerConfigError",
					Message: "secret not found",
				},
			},
			wantTerminal:   true,
			wantReasonPart: "CreateContainerConfigError",
		},
		{
			name: "InvalidImageName is terminal",
			containerState: corev1.ContainerState{
				Waiting: &corev1.ContainerStateWaiting{
					Reason:  "InvalidImageName",
					Message: "bad image",
				},
			},
			wantTerminal:   true,
			wantReasonPart: "InvalidImageName",
		},
		{
			name: "CreateContainerError is terminal",
			containerState: corev1.ContainerState{
				Waiting: &corev1.ContainerStateWaiting{
					Reason:  "CreateContainerError",
					Message: "failed to create container",
				},
			},
			wantTerminal:   true,
			wantReasonPart: "CreateContainerError",
		},
		{
			name: "ContainerCreating is not terminal",
			containerState: corev1.ContainerState{
				Waiting: &corev1.ContainerStateWaiting{
					Reason: "ContainerCreating",
				},
			},
			wantTerminal: false,
		},
		{
			name: "ImagePullBackOff is in the terminal list",
			containerState: corev1.ContainerState{
				Waiting: &corev1.ContainerStateWaiting{
					Reason: "ImagePullBackOff",
				},
			},
			wantTerminal: true,
		},
		{
			name: "running container is not terminal",
			containerState: corev1.ContainerState{
				Running: &corev1.ContainerStateRunning{},
			},
			wantTerminal: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: ns,
					Labels:    map[string]string{"job-name": "test-job"},
				},
			}

			if tc.containerState != (corev1.ContainerState{}) {
				pod.Status.ContainerStatuses = []corev1.ContainerStatus{
					{Name: "main", State: tc.containerState},
				}
			}

			pods := &corev1.PodList{Items: []corev1.Pod{*pod}}

			got, reason := hasTerminalContainerWaitingError(pods)
			if got != tc.wantTerminal {
				t.Errorf("hasTerminalContainerWaitingError() = %v, want %v (reason=%q)", got, tc.wantTerminal, reason)
			}
			if tc.wantTerminal && tc.wantReasonPart != "" {
				if !strings.Contains(reason, tc.wantReasonPart) {
					t.Errorf("reason %q does not contain %q", reason, tc.wantReasonPart)
				}
			}
		})
	}
}

func TestHasJobFailedCreateEvent(t *testing.T) {
	const ns = "test-ns"
	const jobName = "test-job"

	psaMessage := `pods "test-job-abc" is forbidden: violates PodSecurity "restricted:latest": ` +
		`allowPrivilegeEscalation != false, runAsNonRoot != true`

	// persistentEvent builds a FailedCreate event that satisfies both the count
	// and time-span thresholds required by hasJobFailedCreateEvent.
	persistentEvent := func(name, msg string) corev1.Event {
		now := metav1.Now()
		first := metav1.NewTime(now.Add(-minFailureSpan - time.Second))
		return corev1.Event{
			ObjectMeta:     metav1.ObjectMeta{Name: name, Namespace: ns},
			InvolvedObject: corev1.ObjectReference{Name: jobName},
			Reason:         "FailedCreate",
			Message:        msg,
			Count:          failedCreateThreshold,
			FirstTimestamp: first,
			LastTimestamp:  now,
		}
	}

	cases := []struct {
		name           string
		events         []corev1.Event
		wantCount      int
		wantReasonPart string
	}{
		{
			name:      "no events → count 0",
			events:    nil,
			wantCount: 0,
		},
		{
			name: "unrelated event reason → count 0",
			events: []corev1.Event{
				{
					ObjectMeta:     metav1.ObjectMeta{Name: "ev1", Namespace: ns},
					InvolvedObject: corev1.ObjectReference{Name: jobName},
					Reason:         "Scheduled",
					Message:        "Successfully assigned pod",
				},
			},
			wantCount: 0,
		},
		{
			name: "one FailedCreate event from PSA enforcement → persistent, count returned with message",
			events: []corev1.Event{
				persistentEvent("ev-fc", psaMessage),
			},
			wantCount:      failedCreateThreshold,
			wantReasonPart: "violates PodSecurity",
		},
		{
			name: "one FailedCreate for missing service account → persistent, count returned with message",
			events: []corev1.Event{
				persistentEvent("ev-sa", `pods "test-job-" is forbidden: error looking up service account jobs/funnel-worker-sa: serviceaccount "funnel-worker-sa" not found`),
			},
			wantCount:      failedCreateThreshold,
			wantReasonPart: "serviceaccount",
		},
		{
			name: "multiple FailedCreate events → count reflects all, last message returned",
			events: []corev1.Event{
				{
					ObjectMeta:     metav1.ObjectMeta{Name: "ev1", Namespace: ns},
					InvolvedObject: corev1.ObjectReference{Name: jobName},
					Reason:         "Scheduled",
					Message:        "first message",
				},
				persistentEvent("ev2", "earlier failure"),
				persistentEvent("ev3", psaMessage),
			},
			wantCount:      failedCreateThreshold * 2,
			wantReasonPart: "violates PodSecurity",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()

			// Build fake client pre-populated with events.
			var objs []runtime.Object
			for i := range tc.events {
				objs = append(objs, &tc.events[i])
			}
			fakeClient := fake.NewSimpleClientset(objs...)

			// The fake client's field selector support is limited; we intercept
			// the List call and filter manually to simulate the FieldSelector
			// used by hasJobFailedCreateEvent.
			fakeClient.PrependReactor("list", "events", func(action k8stesting.Action) (bool, runtime.Object, error) {
				la := action.(k8stesting.ListAction)
				fs := la.GetListRestrictions().Fields.String()

				all, err := fakeClient.Tracker().List(
					corev1.SchemeGroupVersion.WithResource("events"),
					corev1.SchemeGroupVersion.WithKind("Event"),
					ns,
				)
				if err != nil {
					return true, nil, err
				}
				evList := all.(*corev1.EventList)
				var filtered []corev1.Event
				for _, ev := range evList.Items {
					nameMatch := strings.Contains(fs, fmt.Sprintf("involvedObject.name=%s", ev.InvolvedObject.Name))
					if !nameMatch {
						continue
					}
					// Reason filter is optional: only apply it when present.
					reasonFilter := fmt.Sprintf("reason=%s", ev.Reason)
					if strings.Contains(fs, "reason=") && !strings.Contains(fs, reasonFilter) {
						continue
					}
					filtered = append(filtered, ev)
				}
				return true, &corev1.EventList{Items: filtered}, nil
			})

			conf := config.DefaultConfig()
			conf.Kubernetes.JobsNamespace = ns
			b := &Backend{
				client: fakeClient,
				log:    logger.NewLogger("test", logger.DefaultConfig()),
				conf:   conf,
			}

			gotCount, reason := b.hasJobFailedCreateEvent(ctx, jobName)
			if gotCount != tc.wantCount {
				t.Errorf("hasJobFailedCreateEvent() count = %d, want %d (reason=%q)", gotCount, tc.wantCount, reason)
			}
			if tc.wantCount > 0 && tc.wantReasonPart != "" {
				if !strings.Contains(reason, tc.wantReasonPart) {
					t.Errorf("reason %q does not contain %q", reason, tc.wantReasonPart)
				}
			}
		})
	}
}

// mockReadOnlyServer implements tes.ReadOnlyServer for reconciler tests.
type mockReadOnlyServer struct {
	mu    sync.Mutex
	tasks []*tes.Task
	// onQueuedList, if set, is invoked each time ListTasks is called for the
	// QUEUED state. Tests use it to count reconcile passes and cancel the
	// context deterministically after a known number of passes.
	onQueuedList func()
}

func (m *mockReadOnlyServer) ListTasks(_ context.Context, req *tes.ListTasksRequest) (*tes.ListTasksResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if req.State == tes.State_QUEUED && m.onQueuedList != nil {
		m.onQueuedList()
	}
	var out []*tes.Task
	for _, t := range m.tasks {
		if t.State == req.State {
			out = append(out, t)
		}
	}
	return &tes.ListTasksResponse{Tasks: out}, nil
}

func (m *mockReadOnlyServer) GetTask(_ context.Context, req *tes.GetTaskRequest) (*tes.Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.tasks {
		if t.Id == req.Id {
			return t, nil
		}
	}
	return nil, fmt.Errorf("task %s not found", req.Id)
}

func (m *mockReadOnlyServer) Close() {}

// capturingEventWriter records all events written to it.
type capturingEventWriter struct {
	mu     sync.Mutex
	events []*events.Event
}

func (c *capturingEventWriter) WriteEvent(_ context.Context, ev *events.Event) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, ev)
	return nil
}

func (c *capturingEventWriter) Close() {}

func (c *capturingEventWriter) hasSystemError(taskID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, ev := range c.events {
		if ev.Id == taskID && ev.Type == events.Type_TASK_STATE {
			if ev.GetState() == tes.State_SYSTEM_ERROR {
				return true
			}
		}
	}
	return false
}

// systemErrorCount returns the number of SYSTEM_ERROR state events written for taskID.
func (c *capturingEventWriter) systemErrorCount(taskID string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, ev := range c.events {
		if ev.Id == taskID && ev.Type == events.Type_TASK_STATE && ev.GetState() == tes.State_SYSTEM_ERROR {
			n++
		}
	}
	return n
}

// hasSystemLog reports whether a SYSTEM_LOG event whose message contains substr
// was written for taskID.
func (c *capturingEventWriter) hasSystemLog(taskID, substr string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, ev := range c.events {
		if ev.Id == taskID && ev.Type == events.Type_SYSTEM_LOG {
			if sl := ev.GetSystemLog(); sl != nil && strings.Contains(sl.Msg, substr) {
				return true
			}
		}
	}
	return false
}

// TestReconcile_ZeroStatusFailedCreate verifies that a Job with all-zero
// status counters (Active=0, Succeeded=0, Failed=0) but with a FailedCreate
// event — as produced by Pod Security Admission enforcement — is detected by
// the reconciler and transitions the task to SYSTEM_ERROR.
func TestReconcile_ZeroStatusFailedCreate(t *testing.T) {
	const ns = "test-ns"
	const taskID = "test-task-psa"

	psaMsg := `pods "test-task-psa-abc" is forbidden: violates PodSecurity "restricted:latest": runAsNonRoot != true`

	// Build a Job with all-zero status counters (what we see with PSA blocking).
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      taskID,
			Namespace: ns,
			Labels:    map[string]string{"app": "funnel-worker"},
		},
		Status: batchv1.JobStatus{
			Active:    0,
			Succeeded: 0,
			Failed:    0,
		},
	}

	// FailedCreate event on the Job (emitted by the Job controller).
	// Count is set to failedCreateThreshold and FirstTimestamp/LastTimestamp
	// span minFailureSpan so that both persistence thresholds are met by a
	// single deduplicated event object, as Kubernetes produces after the Job
	// controller retries pod creation repeatedly.
	now := metav1.Now()
	firstTime := metav1.NewTime(now.Add(-minFailureSpan - time.Second))
	failedCreateEvent := &corev1.Event{
		ObjectMeta:     metav1.ObjectMeta{Name: "ev-fc", Namespace: ns},
		InvolvedObject: corev1.ObjectReference{Name: taskID},
		Reason:         "FailedCreate",
		Message:        psaMsg,
		Count:          failedCreateThreshold,
		FirstTimestamp: firstTime,
		LastTimestamp:  now,
	}

	fakeClient := fake.NewSimpleClientset(job, failedCreateEvent)

	// Intercept event List calls to apply field-selector filtering manually,
	// since the fake client does not support server-side field selectors.
	fakeClient.PrependReactor("list", "events", func(action k8stesting.Action) (bool, runtime.Object, error) {
		la := action.(k8stesting.ListAction)
		fs := la.GetListRestrictions().Fields.String()

		all, err := fakeClient.Tracker().List(
			corev1.SchemeGroupVersion.WithResource("events"),
			corev1.SchemeGroupVersion.WithKind("Event"),
			ns,
		)
		if err != nil {
			return true, nil, err
		}
		evList := all.(*corev1.EventList)
		var filtered []corev1.Event
		for _, ev := range evList.Items {
			nameMatch := strings.Contains(fs, fmt.Sprintf("involvedObject.name=%s", ev.InvolvedObject.Name))
			if !nameMatch {
				continue
			}
			// Reason filter is optional: only apply it when present in the selector.
			reasonFilter := fmt.Sprintf("reason=%s", ev.Reason)
			if strings.Contains(fs, "reason=") && !strings.Contains(fs, reasonFilter) {
				continue
			}
			filtered = append(filtered, ev)
		}
		return true, &corev1.EventList{Items: filtered}, nil
	})

	db := &mockReadOnlyServer{
		tasks: []*tes.Task{
			{Id: taskID, State: tes.State_QUEUED},
		},
	}
	evWriter := &capturingEventWriter{}

	conf := config.DefaultConfig()
	conf.Kubernetes.JobsNamespace = ns

	b := &Backend{
		client:   fakeClient,
		event:    evWriter,
		database: db,
		log:      logger.NewLogger("test", logger.DefaultConfig()),
		conf:     conf,
	}

	// Run a single reconcile tick (context cancels after the first tick fires).
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	b.reconcile(ctx, 100*time.Millisecond, true /* disableCleanup */)

	if !evWriter.hasSystemError(taskID) {
		t.Errorf("expected SYSTEM_ERROR event for task %s, got events: %+v", taskID, evWriter.events)
	}
}

// TestReconcile_MissingJobFailsTask verifies that a non-terminal task whose
// worker Job has been deleted out-of-band (e.g. manually, or while the pod was
// still ContainerCreating) is eventually transitioned to SYSTEM_ERROR with a
// system log, rather than being left stuck in QUEUED forever. See issue #88.
func TestReconcile_MissingJobFailsTask(t *testing.T) {
	const ns = "test-ns"
	const taskID = "test-task-missing-job"

	// No worker Job exists in Kubernetes, but the task is QUEUED in the DB.
	fakeClient := fake.NewSimpleClientset()

	db := &mockReadOnlyServer{
		tasks: []*tes.Task{
			{Id: taskID, State: tes.State_QUEUED},
		},
	}
	evWriter := &capturingEventWriter{}

	// Cancel a few passes after the threshold is crossed so we can also assert
	// the SYSTEM_ERROR is emitted exactly once (not re-fired every pass).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var passes int
	db.onQueuedList = func() {
		passes++
		if passes >= missingJobThreshold+2 {
			cancel()
		}
	}

	conf := config.DefaultConfig()
	conf.Kubernetes.JobsNamespace = ns

	b := &Backend{
		client:   fakeClient,
		event:    evWriter,
		database: db,
		log:      logger.NewLogger("test", logger.DefaultConfig()),
		conf:     conf,
	}

	b.reconcile(ctx, 20*time.Millisecond, true /* disableCleanup */)

	if !evWriter.hasSystemError(taskID) {
		t.Errorf("expected SYSTEM_ERROR for task with missing job, got events: %+v", evWriter.events)
	}
	if !evWriter.hasSystemLog(taskID, "no longer exists") {
		t.Errorf("expected SYSTEM_LOG describing missing job, got events: %+v", evWriter.events)
	}
	if n := evWriter.systemErrorCount(taskID); n != 1 {
		t.Errorf("expected exactly one SYSTEM_ERROR event, got %d", n)
	}
}

// TestReconcile_MissingJobGracePeriod verifies that a non-terminal task with no
// worker Job is NOT failed within the grace window — this is the expected,
// transient state immediately after submit (before the Job object is created).
// The task must not be marked SYSTEM_ERROR until missingJobThreshold consecutive
// passes have observed the missing job.
func TestReconcile_MissingJobGracePeriod(t *testing.T) {
	const ns = "test-ns"
	const taskID = "test-task-grace"

	fakeClient := fake.NewSimpleClientset()

	evWriter := &capturingEventWriter{}
	db := &mockReadOnlyServer{
		tasks: []*tes.Task{
			{Id: taskID, State: tes.State_QUEUED},
		},
	}

	// Cancel the context after the second reconcile pass (2 < missingJobThreshold),
	// so the task should still be within its grace window and not yet failed.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var passes int
	db.onQueuedList = func() {
		passes++
		if passes >= 2 {
			cancel()
		}
	}

	conf := config.DefaultConfig()
	conf.Kubernetes.JobsNamespace = ns

	b := &Backend{
		client:   fakeClient,
		event:    evWriter,
		database: db,
		log:      logger.NewLogger("test", logger.DefaultConfig()),
		conf:     conf,
	}

	b.reconcile(ctx, 20*time.Millisecond, true /* disableCleanup */)

	if passes >= missingJobThreshold {
		t.Skipf("reconcile ran %d passes before cancel; cannot assert grace behavior for threshold %d", passes, missingJobThreshold)
	}
	if evWriter.hasSystemError(taskID) {
		t.Errorf("task was failed within grace window (%d passes, threshold %d): %+v", passes, missingJobThreshold, evWriter.events)
	}
}

func TestFetchPodWarningEvents(t *testing.T) {
	const ns = "test-ns"
	const jobName = "test-job"

	cases := []struct {
		name        string
		podName     string
		events      []corev1.Event
		wantContain []string // substrings that must appear in result
		wantEmpty   bool
	}{
		{
			name:      "no events → empty string",
			podName:   "pod-1",
			events:    nil,
			wantEmpty: true,
		},
		{
			name:    "non-warning event type → ignored",
			podName: "pod-1",
			events: []corev1.Event{
				{
					ObjectMeta:     metav1.ObjectMeta{Name: "ev1", Namespace: ns},
					InvolvedObject: corev1.ObjectReference{Name: "pod-1"},
					Type:           "Normal",
					Reason:         "Pulled",
					Message:        "Successfully pulled image",
				},
			},
			wantEmpty: true,
		},
		{
			name:    "unallowed warning reason → ignored",
			podName: "pod-1",
			events: []corev1.Event{
				{
					ObjectMeta:     metav1.ObjectMeta{Name: "ev2", Namespace: ns},
					InvolvedObject: corev1.ObjectReference{Name: "pod-1"},
					Type:           "Warning",
					Reason:         "Evicted",
					Message:        "node ran out of memory",
				},
			},
			wantEmpty: true,
		},
		{
			name:    "CreateContainerConfigError → secret not found in pod event",
			podName: "pod-1",
			events: []corev1.Event{
				{
					ObjectMeta:     metav1.ObjectMeta{Name: "ev3", Namespace: ns},
					InvolvedObject: corev1.ObjectReference{Name: "pod-1"},
					Type:           "Warning",
					Reason:         "Failed",
					Message:        `Error: secret "example-secret" not found`,
				},
			},
			wantContain: []string{`Failed: Error: secret "example-secret" not found`},
		},
		{
			name:    "ErrImagePull → image not found",
			podName: "pod-1",
			events: []corev1.Event{
				{
					ObjectMeta:     metav1.ObjectMeta{Name: "ev4", Namespace: ns},
					InvolvedObject: corev1.ObjectReference{Name: "pod-1"},
					Type:           "Warning",
					Reason:         "ErrImagePull",
					Message:        `Failed to pull image "xyz": not found`,
				},
			},
			wantContain: []string{`ErrImagePull: Failed to pull image "xyz": not found`},
		},
		{
			name:    "StartError → bad entrypoint",
			podName: "pod-1",
			events: []corev1.Event{
				{
					ObjectMeta:     metav1.ObjectMeta{Name: "ev5", Namespace: ns},
					InvolvedObject: corev1.ObjectReference{Name: "pod-1"},
					Type:           "Warning",
					Reason:         "StartError",
					Message:        `exec: "badcmd": executable file not found in $PATH`,
				},
			},
			wantContain: []string{`StartError: exec: "badcmd": executable file not found in $PATH`},
		},
		{
			name:    "duplicate events → deduplicated",
			podName: "pod-1",
			events: []corev1.Event{
				{
					ObjectMeta:     metav1.ObjectMeta{Name: "ev6a", Namespace: ns},
					InvolvedObject: corev1.ObjectReference{Name: "pod-1"},
					Type:           "Warning",
					Reason:         "Failed",
					Message:        `Error: secret "s" not found`,
				},
				{
					ObjectMeta:     metav1.ObjectMeta{Name: "ev6b", Namespace: ns},
					InvolvedObject: corev1.ObjectReference{Name: "pod-1"},
					Type:           "Warning",
					Reason:         "Failed",
					Message:        `Error: secret "s" not found`,
				},
			},
			wantContain: []string{`Failed: Error: secret "s" not found`},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()

			// Pre-populate the fake client with the pod and events.
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tc.podName,
					Namespace: ns,
					Labels:    map[string]string{"job-name": jobName},
				},
			}
			objs := []runtime.Object{pod}
			for i := range tc.events {
				objs = append(objs, &tc.events[i])
			}
			fakeClient := fake.NewSimpleClientset(objs...)

			// Intercept event List calls to filter by involvedObject.name and type,
			// since the fake client does not support server-side field selectors.
			fakeClient.PrependReactor("list", "events", func(action k8stesting.Action) (bool, runtime.Object, error) {
				la := action.(k8stesting.ListAction)
				fs := la.GetListRestrictions().Fields.String()

				all, err := fakeClient.Tracker().List(
					corev1.SchemeGroupVersion.WithResource("events"),
					corev1.SchemeGroupVersion.WithKind("Event"),
					ns,
				)
				if err != nil {
					return true, nil, err
				}
				evList := all.(*corev1.EventList)
				var filtered []corev1.Event
				for _, ev := range evList.Items {
					nameMatch := strings.Contains(fs, fmt.Sprintf("involvedObject.name=%s", ev.InvolvedObject.Name))
					typeMatch := !strings.Contains(fs, "type=") || strings.Contains(fs, fmt.Sprintf("type=%s", ev.Type))
					if nameMatch && typeMatch {
						filtered = append(filtered, ev)
					}
				}
				return true, &corev1.EventList{Items: filtered}, nil
			})

			conf := config.DefaultConfig()
			conf.Kubernetes.JobsNamespace = ns
			b := &Backend{
				client: fakeClient,
				log:    logger.NewLogger("test", logger.DefaultConfig()),
				conf:   conf,
			}

			pods := &corev1.PodList{Items: []corev1.Pod{*pod}}
			got := FetchPodWarningEvents(ctx, b.client, b.conf.Kubernetes.JobsNamespace, pods)

			if tc.wantEmpty {
				if got != "" {
					t.Errorf("expected empty string, got %q", got)
				}
				return
			}
			for _, want := range tc.wantContain {
				if !strings.Contains(got, want) {
					t.Errorf("result %q does not contain %q", got, want)
				}
			}
			// Deduplication check: count occurrences of the first wantContain
			if len(tc.wantContain) > 0 {
				count := strings.Count(got, tc.wantContain[0])
				if count > 1 {
					t.Errorf("expected %q to appear once, got %d times in %q", tc.wantContain[0], count, got)
				}
			}
		})
	}
}

func TestExtractTaskIDFromExecutorJobName(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"abc123-0", "abc123"},
		{"abc123-42", "abc123"},
		{"task-id-with-dashes-0", "task-id-with-dashes"},
		{"abc123", ""},     // no index suffix
		{"abc123-", ""},    // empty suffix
		{"abc123-foo", ""}, // non-numeric suffix
		{"-0", ""},         // empty task ID portion
		{"", ""},
	}
	for _, tt := range tests {
		got := extractTaskIDFromExecutorJobName(tt.name)
		if got != tt.want {
			t.Errorf("extractTaskIDFromExecutorJobName(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

// TestCleanOrphanedResources_ExecutorJobs verifies that CleanOrphanedResources
// discovers and deletes executor jobs whose parent tasks are in a terminal state.
func TestCleanOrphanedResources_ExecutorJobs(t *testing.T) {
	const ns = "test-namespace"
	const taskID = "orphaned-task"
	ctx := context.Background()

	// Executor job left behind after the worker job was already removed.
	executorJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      taskID + "-0",
			Namespace: ns,
			Labels:    map[string]string{"app": "funnel-executor"},
		},
	}

	fakeClient := fake.NewSimpleClientset(executorJob)

	db := &mockDatabase{
		tasks: map[string]*tes.Task{
			taskID: {Id: taskID, State: tes.State_COMPLETE},
		},
	}

	conf := config.DefaultConfig()
	conf.Kubernetes.Namespace = ns
	conf.Kubernetes.JobsNamespace = ns

	backend := &Backend{
		client:   fakeClient,
		log:      logger.NewLogger("test", logger.DefaultConfig()),
		conf:     conf,
		event:    &noopEventWriter{},
		database: db,
	}

	backend.CleanOrphanedResources(ctx)

	// Executor job must be gone.
	_, err := fakeClient.BatchV1().Jobs(ns).Get(ctx, taskID+"-0", metav1.GetOptions{})
	if err == nil {
		t.Error("expected executor job to be deleted, but it still exists")
	}
}

// TestCleanOrphanedResources_ExecutorJobs_ActiveTask verifies that executor jobs
// for tasks that are still active are NOT deleted.
func TestCleanOrphanedResources_ExecutorJobs_ActiveTask(t *testing.T) {
	const ns = "test-namespace"
	const taskID = "active-task"
	ctx := context.Background()

	executorJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      taskID + "-0",
			Namespace: ns,
			Labels:    map[string]string{"app": "funnel-executor"},
		},
	}

	fakeClient := fake.NewSimpleClientset(executorJob)

	db := &mockDatabase{
		tasks: map[string]*tes.Task{
			taskID: {Id: taskID, State: tes.State_RUNNING},
		},
	}

	conf := config.DefaultConfig()
	conf.Kubernetes.Namespace = ns
	conf.Kubernetes.JobsNamespace = ns

	backend := &Backend{
		client:   fakeClient,
		log:      logger.NewLogger("test", logger.DefaultConfig()),
		conf:     conf,
		event:    &noopEventWriter{},
		database: db,
	}

	backend.CleanOrphanedResources(ctx)

	// Executor job must still be present — task is running.
	_, err := fakeClient.BatchV1().Jobs(ns).Get(ctx, taskID+"-0", metav1.GetOptions{})
	if err != nil {
		t.Errorf("expected executor job to remain for running task, got: %v", err)
	}
}
