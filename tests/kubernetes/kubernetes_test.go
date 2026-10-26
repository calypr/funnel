package e2e

import (
	"context"
	"os"
	"os/exec"
	"testing"

	"github.com/calypr/funnel/compute/kubernetes/resources"
	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/logger"
	"github.com/calypr/funnel/tes"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// Constants shared across unit tests that use fake Kubernetes clients.
const (
	unitTestNS     = "test-namespace"
	unitTestJobsNS = "test-jobs-namespace"
	unitTestTaskID = "test-task-id"
)

var unitTestLog = logger.NewLogger("unit-test", logger.DefaultConfig())

// fakeClientWithFunnelPod returns a fake Kubernetes client pre-populated with a
// funnel server pod so that CreateJob's pod-image-discovery does not panic.
func fakeClientWithFunnelPod(ns string) *fake.Clientset {
	client := fake.NewSimpleClientset()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "funnel-server",
			Namespace: ns,
			Labels:    map[string]string{"app": "funnel"}},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "funnel", Image: "ohsucompbio/funnel:latest"},
			},
		},
	}
	_, _ = client.CoreV1().Pods(ns).Create(context.Background(), pod, metav1.CreateOptions{})
	return client
}

// baseJobConfig returns a DefaultConfig with Namespace and JobsNamespace set,
// and WorkerTemplate replaced by the supplied template string.
func baseJobConfig(workerTemplate string) *config.Config {
	conf := config.DefaultConfig()
	conf.Kubernetes.Namespace = unitTestNS
	conf.Kubernetes.JobsNamespace = unitTestJobsNS
	conf.Kubernetes.WorkerTemplate = workerTemplate
	return conf
}

func TestHelloWorld(t *testing.T) {
	for _, dependency := range []string{"bash", "docker", "k3d", "kubectl", "python3"} {
		if _, err := exec.LookPath(dependency); err != nil {
			t.Skipf("%s is not installed; skipping Kubernetes smoke test: %v", dependency, err)
		}
	}

	cmd := exec.Command("../kubernetes-storage/run.sh", "StorageE2E.test_hello_world")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("Kubernetes storage smoke test failed: %v", err)
	}
}

// jobTemplate is a minimal WorkerTemplate
const jobTemplate = `apiVersion: batch/v1
kind: Job
metadata:
  name: funnel-{{.TaskId}}
  namespace: {{.JobsNamespace}}
spec:
  backoffLimit: {{.BackoffLimit}}
  template:
    metadata:
      labels:
        task-name: "{{.TaskNameLabel}}"
    spec:
      restartPolicy: Never
      containers:
      - name: worker
        image: alpine
        command: ["echo", "{{.TaskName}}"]
`

// Tests for SanitizeLabelValue: converts task names into valid Kubernetes label values.
func TestSanitizeLabelValue_ValidInputPassthrough(t *testing.T) {
	cases := []struct{ in, want string }{
		{"hello", "hello"},
		{"Hello-World.123", "Hello-World.123"},
		{"abc_def", "abc_def"},
		{"a", "a"},
		{"", ""},
	}
	for _, c := range cases {
		if got := resources.SanitizeLabelValue(c.in); got != c.want {
			t.Errorf("SanitizeLabelValue(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}

func TestSanitizeLabelValue_ReplacesInvalidChars(t *testing.T) {
	cases := []struct{ in, want string }{
		{"hello world", "hello-world"},
		{"task/name:v1", "task-name-v1"},
		{"foo@bar", "foo-bar"},
		{"a b c", "a-b-c"},
	}
	for _, c := range cases {
		if got := resources.SanitizeLabelValue(c.in); got != c.want {
			t.Errorf("SanitizeLabelValue(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}

func TestSanitizeLabelValue_TruncatesAt63Chars(t *testing.T) {
	long := "abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz01"
	got := resources.SanitizeLabelValue(long)
	if len(got) > 63 {
		t.Errorf("SanitizeLabelValue(64-char string) returned %d chars; want \u226463", len(got))
	}
}

func TestSanitizeLabelValue_StripsLeadingTrailingNonAlphanumeric(t *testing.T) {
	cases := []struct{ in, want string }{
		{"--hello--", "hello"},
		{"...hello...", "hello"},
		{"-a-", "a"},
	}
	for _, c := range cases {
		if got := resources.SanitizeLabelValue(c.in); got != c.want {
			t.Errorf("SanitizeLabelValue(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}

// Tests for CreateJob: BackoffLimit falls back to 10 when not set via backend_parameters.
func TestCreateJob_DefaultBackoffLimit(t *testing.T) {
	client := fakeClientWithFunnelPod(unitTestNS)
	task := &tes.Task{
		Id:        "bl-default",
		Resources: &tes.Resources{CpuCores: 1},
	}

	if _, err := resources.CreateJob(context.Background(), task, baseJobConfig(jobTemplate), client, unitTestLog, nil); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	job, err := client.BatchV1().Jobs(unitTestJobsNS).Get(context.Background(), "funnel-"+task.Id, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get job: %v", err)
	}

	if job.Spec.BackoffLimit == nil {
		t.Fatal("BackoffLimit is nil; want 10")
	}
	if got := *job.Spec.BackoffLimit; got != 10 {
		t.Errorf("BackoffLimit = %d; want 10", got)
	}
}

// Tests for CreateJob: BackoffLimit is read from task backend_parameters["backoff_limit"].
func TestCreateJob_BackoffLimitFromBackendParameters(t *testing.T) {
	client := fakeClientWithFunnelPod(unitTestNS)
	task := &tes.Task{
		Id: "bl-custom",
		Resources: &tes.Resources{
			CpuCores:          1,
			BackendParameters: map[string]string{"backoff_limit": "3"},
		},
	}

	if _, err := resources.CreateJob(context.Background(), task, baseJobConfig(jobTemplate), client, unitTestLog, nil); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	job, err := client.BatchV1().Jobs(unitTestJobsNS).Get(context.Background(), "funnel-"+task.Id, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get job: %v", err)
	}

	if job.Spec.BackoffLimit == nil {
		t.Fatal("BackoffLimit is nil; want 3")
	}
	if got := *job.Spec.BackoffLimit; got != 3 {
		t.Errorf("BackoffLimit = %d; want 3", got)
	}
}

func TestCreateJob_BackoffLimitInvalidValueFallsBackToDefault(t *testing.T) {
	client := fakeClientWithFunnelPod(unitTestNS)
	task := &tes.Task{
		Id: "bl-invalid",
		Resources: &tes.Resources{
			CpuCores:          1,
			BackendParameters: map[string]string{"backoff_limit": "not-a-number"},
		},
	}

	if _, err := resources.CreateJob(context.Background(), task, baseJobConfig(jobTemplate), client, unitTestLog, nil); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	job, err := client.BatchV1().Jobs(unitTestJobsNS).Get(context.Background(), "funnel-"+task.Id, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get job: %v", err)
	}

	if job.Spec.BackoffLimit == nil {
		t.Fatal("BackoffLimit is nil; want 10 (fallback)")
	}
	if got := *job.Spec.BackoffLimit; got != 10 {
		t.Errorf("BackoffLimit = %d; want 10 (fallback for non-numeric value)", got)
	}
}

func TestCreateJob_NegativeBackoffLimitFallsBackToDefault(t *testing.T) {
	client := fakeClientWithFunnelPod(unitTestNS)
	task := &tes.Task{
		Id: "bl-negative",
		Resources: &tes.Resources{
			CpuCores:          1,
			BackendParameters: map[string]string{"backoff_limit": "-1"},
		},
	}

	if _, err := resources.CreateJob(context.Background(), task, baseJobConfig(jobTemplate), client, unitTestLog, nil); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	job, err := client.BatchV1().Jobs(unitTestJobsNS).Get(context.Background(), "funnel-"+task.Id, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get job: %v", err)
	}

	if job.Spec.BackoffLimit == nil {
		t.Fatal("BackoffLimit is nil; want 10 (fallback)")
	}
	if got := *job.Spec.BackoffLimit; got != 10 {
		t.Errorf("BackoffLimit = %d; want 10 (fallback for negative value)", got)
	}
}

// Tests for CreateJob: task name is sanitized before being used as a Kubernetes label value.
func TestCreateJob_TaskNameLabelIsSanitized(t *testing.T) {
	client := fakeClientWithFunnelPod(unitTestNS)
	task := &tes.Task{
		Id:        "label-test",
		Name:      "My Task: v1.0!",
		Resources: &tes.Resources{CpuCores: 1},
	}

	if _, err := resources.CreateJob(context.Background(), task, baseJobConfig(jobTemplate), client, unitTestLog, nil); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	job, err := client.BatchV1().Jobs(unitTestJobsNS).Get(context.Background(), "funnel-"+task.Id, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get job: %v", err)
	}

	label, ok := job.Spec.Template.Labels["task-name"]
	if !ok {
		t.Fatal("pod-template label 'task-name' not present")
	}
	if label == task.Name {
		t.Errorf("label value %q equals unsanitized task name; expected sanitized form", label)
	}
	if len(label) > 63 {
		t.Errorf("sanitized label length %d exceeds 63", len(label))
	}
}

// validConfigMapTemplate is a minimal ConfigMap template used by the CreateConfigMap tests.
const validConfigMapTemplate = `apiVersion: v1
kind: ConfigMap
metadata:
  name: funnel-worker-config-{{.TaskId}}
  namespace: {{.Namespace}}
data:
  funnel-worker.yaml: |
    placeholder: config
`

// Tests for CreateConfigMap: template-driven ConfigMap creation, error handling.
func TestCreateConfigMap_WithValidTemplate(t *testing.T) {
	conf := config.DefaultConfig()
	conf.Kubernetes.JobsNamespace = unitTestJobsNS
	conf.Kubernetes.ConfigMapTemplate = validConfigMapTemplate

	client := fake.NewSimpleClientset()
	if err := resources.CreateConfigMap(context.Background(), unitTestTaskID, conf, client, unitTestLog, nil); err != nil {
		t.Fatalf("CreateConfigMap: %v", err)
	}

	name := "funnel-worker-config-" + unitTestTaskID
	cm, err := client.CoreV1().ConfigMaps(unitTestJobsNS).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("ConfigMap %q not found after creation: %v", name, err)
	}
	if cm.Namespace != unitTestJobsNS {
		t.Errorf("ConfigMap.Namespace = %q; want %q", cm.Namespace, unitTestJobsNS)
	}
}

func TestCreateConfigMap_InvalidTemplateSyntax(t *testing.T) {
	conf := config.DefaultConfig()
	conf.Kubernetes.JobsNamespace = unitTestJobsNS
	conf.Kubernetes.ConfigMapTemplate = `{{.Unclosed`

	err := resources.CreateConfigMap(context.Background(), unitTestTaskID, conf, fake.NewSimpleClientset(), unitTestLog, nil)
	if err == nil {
		t.Error("expected error for malformed template syntax; got nil")
	}
}

func TestCreateConfigMap_TemplateProducesWrongKind(t *testing.T) {
	conf := config.DefaultConfig()
	conf.Kubernetes.JobsNamespace = unitTestJobsNS
	// Template yields a Pod — CreateConfigMap must reject it.
	conf.Kubernetes.ConfigMapTemplate = `apiVersion: v1
kind: Pod
metadata:
  name: wrong-{{.TaskId}}
  namespace: {{.Namespace}}
spec:
  containers:
  - name: c
    image: alpine
`
	err := resources.CreateConfigMap(context.Background(), unitTestTaskID, conf, fake.NewSimpleClientset(), unitTestLog, nil)
	if err == nil {
		t.Error("expected error when template produces a non-ConfigMap object; got nil")
	}
}

// pvTemplateHostPath is a hostPath-based PV template; used to test CreatePV without GenericS3.
const pvTemplateHostPath = `apiVersion: v1
kind: PersistentVolume
metadata:
  name: funnel-worker-pv-{{.TaskId}}
spec:
  capacity:
    storage: 1Gi
  accessModes:
    - ReadWriteOnce
  hostPath:
    path: /tmp/funnel/{{.TaskId}}
`

// pvTemplateS3 is a CSI-based PV template that references GenericS3 fields.
const pvTemplateS3 = `apiVersion: v1
kind: PersistentVolume
metadata:
  name: funnel-worker-pv-{{.TaskId}}
spec:
  capacity:
    storage: 1Gi
  accessModes:
    - ReadWriteOnce
  csi:
    driver: s3.csi.aws.com
    volumeHandle: {{.Bucket}}-{{.TaskId}}
    volumeAttributes:
      bucketName: {{.Bucket}}
      region: {{.Region}}
`

// Tests for CreatePV: GenericS3 is optional; S3 fields are passed when present.
func TestCreatePV_WithoutGenericS3DoesNotPanic(t *testing.T) {
	conf := config.DefaultConfig()
	conf.Kubernetes.JobsNamespace = unitTestJobsNS
	conf.Kubernetes.PVTemplate = pvTemplateHostPath

	client := fake.NewSimpleClientset()
	if err := resources.CreatePV(context.Background(), unitTestTaskID, 0, conf, client, unitTestLog); err != nil {
		t.Fatalf("CreatePV without GenericS3: %v", err)
	}

	pvName := "funnel-worker-pv-" + unitTestTaskID
	if _, err := client.CoreV1().PersistentVolumes().Get(context.Background(), pvName, metav1.GetOptions{}); err != nil {
		t.Fatalf("PV %q not found: %v", pvName, err)
	}
}

func TestCreatePV_WithGenericS3FieldsArePassed(t *testing.T) {
	conf := config.DefaultConfig()
	conf.Kubernetes.JobsNamespace = unitTestJobsNS
	conf.Kubernetes.PVTemplate = pvTemplateS3
	conf.GenericS3 = []*config.GenericS3Storage{
		{Bucket: "my-bucket", Region: "us-east-1", KmsKeyID: "key-123"},
	}

	client := fake.NewSimpleClientset()
	if err := resources.CreatePV(context.Background(), unitTestTaskID, 0, conf, client, unitTestLog); err != nil {
		t.Fatalf("CreatePV with GenericS3: %v", err)
	}

	pvName := "funnel-worker-pv-" + unitTestTaskID
	if _, err := client.CoreV1().PersistentVolumes().Get(context.Background(), pvName, metav1.GetOptions{}); err != nil {
		t.Fatalf("PV %q not found: %v", pvName, err)
	}
}

// pvcTemplateHostPath is a minimal PVC template; used to test CreatePVC without GenericS3.
const pvcTemplateHostPath = `apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: funnel-worker-pvc-{{.TaskId}}
  namespace: {{.Namespace}}
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 1Gi
`

// Tests for CreatePVC: GenericS3 is optional.
func TestCreatePVC_WithoutGenericS3DoesNotPanic(t *testing.T) {
	conf := config.DefaultConfig()
	conf.Kubernetes.JobsNamespace = unitTestJobsNS
	conf.Kubernetes.PVCTemplate = pvcTemplateHostPath

	client := fake.NewSimpleClientset()
	if err := resources.CreatePVC(context.Background(), unitTestTaskID, 0, conf, client, unitTestLog, nil); err != nil {
		t.Fatalf("CreatePVC without GenericS3: %v", err)
	}

	pvcName := "funnel-worker-pvc-" + unitTestTaskID
	if _, err := client.CoreV1().PersistentVolumeClaims(unitTestJobsNS).Get(context.Background(), pvcName, metav1.GetOptions{}); err != nil {
		t.Fatalf("PVC %q not found: %v", pvcName, err)
	}
}
