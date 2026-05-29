// Package kubernetes contains code for accessing compute resources via the Kubernetes v1 Batch API.
package kubernetes

import (
	"context"
	"fmt"
	"reflect"
	"testing"

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

	// Verify that the ConfigMap was created
	configMapName := "funnel-worker-config-" + task.Id
	_, err = fakeClient.CoreV1().ConfigMaps(conf.Kubernetes.JobsNamespace).Get(context.Background(), configMapName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get ConfigMap: %v", err)
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

	// Verify that the ConfigMap was deleted
	_, err = fakeClient.CoreV1().ConfigMaps(conf.Kubernetes.JobsNamespace).Get(context.Background(), configMapName, metav1.GetOptions{})
	if err == nil {
		t.Error("expected ConfigMap to be deleted, but it still exists")
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
