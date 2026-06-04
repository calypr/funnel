package e2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/ohsu-comp-bio/funnel/compute/kubernetes/resources"
	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/logger"
	"github.com/ohsu-comp-bio/funnel/tes"
	"github.com/ohsu-comp-bio/funnel/tests"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

var fun *tests.Funnel
var clusterName string

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

// checkDependencies checks if the required dependencies (k3d and helm) are installed.
// K3d: https://k3d.io/
// Helm: https://helm.sh/
func checkDependencies() error {
	cmd := exec.Command("k3d", "version")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("k3d command not found: %v", err)
	}

	cmd = exec.Command("helm", "version")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("helm command not found: %v", err)
	}

	return nil
}

// TestMain sets up the test environment for Kubernetes integration tests.
// When k3d/helm are unavailable the e2e setup is skipped, but unit tests
// (those that don't rely on the `fun` variable) are still executed.
func TestMain(m *testing.M) {
	if err := checkDependencies(); err != nil {
		logger.Debug("k3d/helm not found; skipping e2e setup, running unit tests only:", err)
		os.Exit(m.Run())
	}

	tests.ParseConfig()
	conf := tests.DefaultConfig()

	// Set the compute type to Kubernetes
	conf.Compute = "kubernetes"

	// Set the cluster name
	clusterName = "funnel-test-cluster-" + tests.RandomString(6)

	// TODO: This pattern is used when running tests from the Makefile (e.g. `make test-slurm`)
	// We don't have any `test-K8s` target, but can add one if needed.
	if conf.Compute != "kubernetes" {
		logger.Debug("Skipping kubernetes e2e tests...")
		os.Exit(0)
	}

	// Create the K8s cluster
	err := StartK8sCluster(clusterName)
	if err != nil {
		logger.Debug("failed to start K8s cluster:", err)
		os.Exit(1)
	}

	kubeconfig := filepath.Join(os.TempDir(),
		"funnel",
		fmt.Sprintf("%s-kubeconfig.yaml", clusterName),
	)

	// Write the kubeconfig to a temporary file
	err = WriteKubeconfig(clusterName, kubeconfig)
	if err != nil {
		logger.Debug("failed to get kubeconfig:", err)
		os.Exit(1)
	}

	// Start the Funnel server in the K8s cluster using the Helm charts in the K8s deployments directory
	err = StartServerInK8s(clusterName, "../../deployments/kubernetes/helm/")
	if err != nil {
		logger.Debug("failed to start funnel server in K8s:", err)
		os.Exit(1)
	}

	// Create a Funnel instance with a K8s client
	conf.Server.RPCPort = "9090"
	conf.Server.HTTPPort = "8080"
	fun = tests.NewFunnel(conf)
	fun.StartServer()

	exit := 0
	defer func() {
		// Cleanup the test K8s cluster
		err = DeleteK8sCluster(clusterName, kubeconfig)
		if err != nil {
			logger.Debug("failed to delete K8s cluster:", err)
		}
		os.Exit(exit)
	}()

	exit = m.Run()
	return
}

// StartK8sCluster creates a K8s cluster for integration tests.
func StartK8sCluster(clusterName string) error {
	cmd := exec.Command("k3d", "cluster", "create", clusterName)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to create K8s cluster: %v", err)
	}

	logger.Debug("K8s cluster created successfully", "Cluster Name", clusterName)

	return nil
}

// WriteKubeconfig retrieves the kubeconfig for the K8s cluster.
// Example: `/tmp/funnel/funnel-test-cluster-abc123-kubeconfig.yaml`
func WriteKubeconfig(clusterName string, kubeconfig string) error {
	cmd := exec.Command("k3d", "kubeconfig", "get", clusterName)
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to get kubeconfig for K8s cluster: %v", err)
	}

	err = os.MkdirAll(filepath.Dir(kubeconfig), os.ModePerm)
	if err != nil {
		return fmt.Errorf("failed to create directory for kubeconfig: %v", err)
	}

	err = os.WriteFile(kubeconfig, output, 0644)
	if err != nil {
		return fmt.Errorf("failed to write kubeconfig to file: %v", err)
	}

	return nil
}

// DeleteK8sCluster tears down the test K8s cluster.
func DeleteK8sCluster(clusterName string, kubeconfig string) error {
	cmd := exec.Command("k3d", "delete", "cluster", clusterName, "--config", kubeconfig)

	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("failed to delete K8s cluster: %v", err)
	}

	return nil
}

// StartServerInK8s deploys the Funnel server in the K8s cluster using Helm.
func StartServerInK8s(clusterName string, chartPath string) error {
	cmd := exec.Command("helm", "upgrade", "--install", "funnel", chartPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to deploy Funnel with Helm: %v", err)
	}

	// Wait until the Funnel server is ready
	cmd = exec.CommandContext(context.Background(), "kubectl", "rollout", "status", "deployment/funnel-server", "--timeout", "180s")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to wait for Funnel server to be ready: %v", err)
	}

	logger.Debug("Funnel server deployed successfully in K8s cluster", "Cluster Name", clusterName)

	return nil
}

func PortForwardFunnel(t *testing.T, namespace, svcName string, ports ...string) *exec.Cmd {
	args := []string{"port-forward", "svc/funnel"}
	args = append(args, ports...)

	cmd := exec.Command("kubectl", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start port-forward: %v", err)
	}

	return cmd
}

// TestHelloWorld runs a simple hello world task in the Kubernetes cluster.
func TestHelloWorld(t *testing.T) {
	if fun == nil {
		t.Skip("k3d cluster not available")
	}
	id, err := fun.RunE(`
    --sh 'echo hello world'
    `)

	if err != nil {
		t.Fatal("failed to run task:", err)
	}

	task := fun.Wait(id)

	if task.State != tes.State_COMPLETE {
		t.Fatal("expected task to be in complete state; got:", task.State.String())
	}

	if task.Logs[0].Logs[0].Stdout != "hello world\n" {
		t.Fatal("Missing stdout")
	}
}

// jobTemplateNoTTL is a minimal WorkerTemplate without ttlSecondsAfterFinished;
// used to verify that a default TTL of 300 seconds is injected automatically.
const jobTemplateNoTTL = `apiVersion: batch/v1
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

// jobTemplateWithTTL has an explicit ttlSecondsAfterFinished of 600;
// used to verify that a template-specified TTL is not overwritten by the default.
const jobTemplateWithTTL = `apiVersion: batch/v1
kind: Job
metadata:
  name: funnel-{{.TaskId}}
  namespace: {{.JobsNamespace}}
spec:
  ttlSecondsAfterFinished: 600
  backoffLimit: {{.BackoffLimit}}
  template:
    spec:
      restartPolicy: Never
      containers:
      - name: worker
        image: alpine
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

// Tests for CreateJob: ttlSecondsAfterFinished defaults to 300 when absent from the template.
func TestCreateJob_DefaultTTLIsSet(t *testing.T) {
	client := fakeClientWithFunnelPod(unitTestNS)
	task := &tes.Task{
		Id:        "ttl-default",
		Name:      "TTL Default Test",
		Resources: &tes.Resources{CpuCores: 1, RamGb: 1.0},
	}

	if _, err := resources.CreateJob(context.Background(), task, baseJobConfig(jobTemplateNoTTL), client, unitTestLog); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	job, err := client.BatchV1().Jobs(unitTestJobsNS).Get(context.Background(), "funnel-"+task.Id, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get job: %v", err)
	}

	if job.Spec.TTLSecondsAfterFinished == nil {
		t.Fatal("TTLSecondsAfterFinished is nil; want 300")
	}
	if got := *job.Spec.TTLSecondsAfterFinished; got != 300 {
		t.Errorf("TTLSecondsAfterFinished = %d; want 300", got)
	}
}

// Tests for CreateJob: a ttlSecondsAfterFinished already in the template is not overwritten.
func TestCreateJob_ExistingTTLIsPreserved(t *testing.T) {
	client := fakeClientWithFunnelPod(unitTestNS)
	task := &tes.Task{
		Id:        "ttl-preserve",
		Resources: &tes.Resources{CpuCores: 1, RamGb: 1.0},
	}

	if _, err := resources.CreateJob(context.Background(), task, baseJobConfig(jobTemplateWithTTL), client, unitTestLog); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	job, err := client.BatchV1().Jobs(unitTestJobsNS).Get(context.Background(), "funnel-"+task.Id, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get job: %v", err)
	}

	if job.Spec.TTLSecondsAfterFinished == nil {
		t.Fatal("TTLSecondsAfterFinished is nil; want 600")
	}
	if got := *job.Spec.TTLSecondsAfterFinished; got != 600 {
		t.Errorf("TTLSecondsAfterFinished = %d; want 600 (template value must not be overwritten)", got)
	}
}

// Tests for CreateJob: BackoffLimit falls back to 10 when not set via backend_parameters.
func TestCreateJob_DefaultBackoffLimit(t *testing.T) {
	client := fakeClientWithFunnelPod(unitTestNS)
	task := &tes.Task{
		Id:        "bl-default",
		Resources: &tes.Resources{CpuCores: 1},
	}

	if _, err := resources.CreateJob(context.Background(), task, baseJobConfig(jobTemplateNoTTL), client, unitTestLog); err != nil {
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

	if _, err := resources.CreateJob(context.Background(), task, baseJobConfig(jobTemplateNoTTL), client, unitTestLog); err != nil {
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

	if _, err := resources.CreateJob(context.Background(), task, baseJobConfig(jobTemplateNoTTL), client, unitTestLog); err != nil {
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

	if _, err := resources.CreateJob(context.Background(), task, baseJobConfig(jobTemplateNoTTL), client, unitTestLog); err != nil {
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

	if _, err := resources.CreateJob(context.Background(), task, baseJobConfig(jobTemplateNoTTL), client, unitTestLog); err != nil {
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
	if err := resources.CreatePV(context.Background(), unitTestTaskID, conf, client, unitTestLog); err != nil {
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
	if err := resources.CreatePV(context.Background(), unitTestTaskID, conf, client, unitTestLog); err != nil {
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
	if err := resources.CreatePVC(context.Background(), unitTestTaskID, conf, client, unitTestLog, nil); err != nil {
		t.Fatalf("CreatePVC without GenericS3: %v", err)
	}

	pvcName := "funnel-worker-pvc-" + unitTestTaskID
	if _, err := client.CoreV1().PersistentVolumeClaims(unitTestJobsNS).Get(context.Background(), pvcName, metav1.GetOptions{}); err != nil {
		t.Fatalf("PVC %q not found: %v", pvcName, err)
	}
}
