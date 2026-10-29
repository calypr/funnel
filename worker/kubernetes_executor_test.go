package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/calypr/funnel/compute/kubernetes/storageplan"
	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/tes"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"
)

func TestWaitForPodFinishWaitsForNamedExecutor(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	clientset.PrependWatchReactor("pods", func(_ k8stesting.Action) (bool, watch.Interface, error) {
		watcher := watch.NewRaceFreeFake()
		watcher.Add(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "task-0-pod", ResourceVersion: "1"},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
				ContainerStatuses: []corev1.ContainerStatus{
					{Name: "sidecar", State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 0}}},
					{Name: "task-runner", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
				},
			},
		})
		watcher.Add(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "task-0-pod", ResourceVersion: "2"},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
				ContainerStatuses: []corev1.ContainerStatus{
					{Name: "sidecar", State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 0}}},
					{Name: "task-runner", State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 23}}},
				},
			},
		})
		return true, watcher, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	pod, err := waitForPodFinish(ctx, clientset, testNamespace, testSelector, "task-runner")
	if err != nil {
		t.Fatalf("wait for pod: %v", err)
	}
	if pod.ResourceVersion != "2" {
		t.Fatalf("returned pod resourceVersion %q, want 2 after the named executor terminated", pod.ResourceVersion)
	}
}

func TestWaitForPodFinishWaitsPastSuccessfulInitContainer(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	clientset.PrependWatchReactor("pods", func(_ k8stesting.Action) (bool, watch.Interface, error) {
		watcher := watch.NewRaceFreeFake()
		watcher.Add(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "task-0-pod", ResourceVersion: "1"},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
				InitContainerStatuses: []corev1.ContainerStatus{{
					Name: "setup", State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 0}},
				}},
				ContainerStatuses: []corev1.ContainerStatus{{
					Name: "task-runner", State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "PodInitializing"}},
				}},
			},
		})
		watcher.Add(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "task-0-pod", ResourceVersion: "2"},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
				InitContainerStatuses: []corev1.ContainerStatus{{
					Name: "setup", State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 0}},
				}},
				ContainerStatuses: []corev1.ContainerStatus{{
					Name: "task-runner", State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 0}},
				}},
			},
		})
		return true, watcher, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	pod, err := waitForPodFinish(ctx, clientset, testNamespace, testSelector, "task-runner")
	if err != nil {
		t.Fatalf("wait for pod: %v", err)
	}
	if pod.ResourceVersion != "2" {
		t.Fatalf("returned pod resourceVersion %q, want 2 after the executor terminated", pod.ResourceVersion)
	}
}

func TestWaitForPodFinishReportsFailedInitContainer(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	clientset.PrependWatchReactor("pods", func(_ k8stesting.Action) (bool, watch.Interface, error) {
		watcher := watch.NewRaceFreeFake()
		watcher.Add(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "task-0-pod", ResourceVersion: "1"},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
				InitContainerStatuses: []corev1.ContainerStatus{{
					Name: "setup", State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 19}},
				}},
				ContainerStatuses: []corev1.ContainerStatus{{
					Name: "task-runner", State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "PodInitializing"}},
				}},
			},
		})
		return true, watcher, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_, err := waitForPodFinish(ctx, clientset, testNamespace, testSelector, "task-runner")
	var systemErr *K8sSystemErr
	if !errors.As(err, &systemErr) || systemErr.Reason != "InitContainerFailed" {
		t.Fatalf("wait error = %v, want K8sSystemErr InitContainerFailed", err)
	}
	if !strings.Contains(systemErr.Message, `init container "setup" exited with code 19`) {
		t.Fatalf("init failure message = %q", systemErr.Message)
	}
}

func TestWaitForPodFinishReturnsSelectedExecutorWaitingError(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	clientset.PrependWatchReactor("pods", func(_ k8stesting.Action) (bool, watch.Interface, error) {
		watcher := watch.NewRaceFreeFake()
		watcher.Add(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "task-0-pod", ResourceVersion: "1"},
			Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{
				Name: "task-runner",
				State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{
					Reason: "ImagePullBackOff", Message: "executor image is unavailable",
				}},
			}}},
		})
		return true, watcher, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_, err := waitForPodFinish(ctx, clientset, testNamespace, testSelector, "task-runner")
	var systemErr *K8sSystemErr
	if !errors.As(err, &systemErr) || systemErr.Reason != "ImagePullBackOff" {
		t.Fatalf("wait error = %v, want K8sSystemErr ImagePullBackOff", err)
	}
	if systemErr.Message != "executor pod has a terminal container waiting error: executor image is unavailable" {
		t.Fatalf("waiting error message = %q", systemErr.Message)
	}
}

func TestWaitForPodFinishIgnoresSidecarWaitingError(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	clientset.PrependWatchReactor("pods", func(_ k8stesting.Action) (bool, watch.Interface, error) {
		watcher := watch.NewRaceFreeFake()
		watcher.Add(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "task-0-pod", ResourceVersion: "1"},
			Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{
				{Name: "task-runner", State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 0}}},
				{Name: "sidecar", State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff"}}},
			}},
		})
		return true, watcher, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	pod, err := waitForPodFinish(ctx, clientset, testNamespace, testSelector, "task-runner")
	if err != nil {
		t.Fatalf("completed executor was overridden by sidecar waiting error: %v", err)
	}
	if pod.Name != "task-0-pod" {
		t.Fatalf("completed executor pod = %q, want task-0-pod", pod.Name)
	}
}

func TestRunRejectsExecutorJobWithoutRegularContainers(t *testing.T) {
	cmd := KubernetesCommand{
		TaskId: "task-123", JobId: 0,
		TaskTemplate: `apiVersion: batch/v1
kind: Job
metadata:
  name: {{.TaskId}}-{{.JobId}}
spec:
  template:
    spec:
      containers: []
`,
		Resources: &tes.Resources{}, ResourceLimits: &tes.Resources{},
	}

	err := cmd.Run(context.Background())
	var systemErr *K8sSystemErr
	if !errors.As(err, &systemErr) {
		t.Fatalf("Run error = %v, want K8sSystemErr", err)
	}
	if systemErr.Reason != "JobCreationFailed" {
		t.Fatalf("system error reason = %q, want JobCreationFailed", systemErr.Reason)
	}
	if !strings.Contains(systemErr.Message, "requires a regular container") {
		t.Fatalf("system error message = %q, want a clear missing-container message", systemErr.Message)
	}
}

const executorGPUCountTemplate = `apiVersion: batch/v1
kind: Job
metadata:
  name: {{.TaskId}}-{{.JobId}}
  namespace: {{.JobsNamespace}}
spec:
  template:
    spec:
      nodeSelector:
        template: pinned
      restartPolicy: Never
      containers:
      - name: executor
        image: {{.Image}}
        resources:
          requests:
            cpu: "1"
            memory: "2Gi"
          limits:
            cpu: "2"
            memory: "3Gi"
            example.com/accelerator: "7"
      - name: sidecar
        image: busybox
        resources:
          limits:
            memory: "64Mi"
`

func TestRunAppliesGPUCountOnlyToExecutorContainer(t *testing.T) {
	tests := []struct {
		name            string
		parameters      map[string]string
		wantGPU         string
		wantGPUResource corev1.ResourceName
	}{
		{name: "requested GPUs", parameters: map[string]string{"gpu_count": "2"}, wantGPU: "2", wantGPUResource: "nvidia.com/gpu"},
		{
			name: "custom resource and node selector",
			parameters: map[string]string{
				"gpu_count":         "2",
				"gpu_resource":      "vendor.example/accelerator",
				"gpu_node_selector": `model=fast,vendor=nvidia`,
			},
			wantGPU:         "2",
			wantGPUResource: "vendor.example/accelerator",
		},
		{name: "no GPU request"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conf := config.DefaultConfig()
			const jobsNamespace = "test-jobs"
			worker := DefaultWorker{
				Executor: Executor{
					Template:      executorGPUCountTemplate,
					Namespace:     jobsNamespace,
					JobsNamespace: jobsNamespace,
					NodeSelector:  map[string]string{"admin": "required"},
					Resources:     conf.Kubernetes.Resources,
				},
				EventWriter: events.Noop{},
			}
			task := &tes.Task{
				Id: "gpu-task",
				Resources: &tes.Resources{
					BackendParameters: tt.parameters,
				},
			}
			command, err := worker.newKubernetesCommand(context.Background(), events.NewTaskWriter(task.Id, 0, events.Noop{}), task, &tes.Executor{}, 0,
				Command{Image: "executor:test", ShellCommand: []string{"true"}}, task.Resources)
			if err != nil {
				t.Fatalf("newKubernetesCommand: %v", err)
			}

			client := fake.NewSimpleClientset()
			client.PrependWatchReactor("pods", func(k8stesting.Action) (bool, watch.Interface, error) {
				return true, nil, errors.New("stop after executor job submission")
			})
			command.Clientset = client
			if err := command.Run(context.Background()); err == nil {
				t.Fatal("Run unexpectedly completed without a pod watch")
			}

			job, err := client.BatchV1().Jobs(jobsNamespace).Get(context.Background(), "gpu-task-0", metav1.GetOptions{})
			if err != nil {
				t.Fatalf("get submitted executor job: %v", err)
			}
			containers := job.Spec.Template.Spec.Containers
			if len(containers) != 2 || containers[0].Name != "executor" || containers[1].Name != "sidecar" {
				t.Fatalf("submitted containers = %+v, want executor and sidecar", containers)
			}

			gpuResource := tt.wantGPUResource
			if gpuResource == "" {
				gpuResource = "nvidia.com/gpu"
			}
			gpuLimit, hasGPULimit := containers[0].Resources.Limits[gpuResource]
			if tt.wantGPU == "" {
				if hasGPULimit {
					t.Fatalf("executor GPU limit = %s, want no GPU limit", gpuLimit.String())
				}
			} else if !hasGPULimit || gpuLimit.Cmp(resource.MustParse(tt.wantGPU)) != 0 {
				t.Fatalf("executor GPU limit = %v, want %s", gpuLimit, tt.wantGPU)
			}
			if tt.wantGPU != "" {
				gpuRequest, exists := containers[0].Resources.Requests[gpuResource]
				if !exists || gpuRequest.Cmp(resource.MustParse(tt.wantGPU)) != 0 {
					t.Fatalf("executor GPU request = %v, want %s", gpuRequest, tt.wantGPU)
				}
			}
			if _, exists := containers[1].Resources.Limits[gpuResource]; exists {
				t.Fatal("sidecar received the executor GPU limit")
			}
			if got := job.Spec.Template.Spec.NodeSelector["template"]; got != "pinned" {
				t.Errorf("template node selector = %q, want pinned", got)
			}
			if want, exists := tt.parameters["gpu_node_selector"]; exists {
				if got := job.Spec.Template.Spec.NodeSelector["vendor"]; got != "nvidia" {
					t.Errorf("GPU node selector vendor = %q, want nvidia", got)
				}
				if got := job.Spec.Template.Spec.NodeSelector["model"]; got != "fast" {
					t.Errorf("GPU node selector model = %q, want fast (source %s)", got, want)
				}
			}

			for name, want := range map[corev1.ResourceName]string{
				corev1.ResourceCPU:                             "2",
				corev1.ResourceMemory:                          "3Gi",
				corev1.ResourceName("example.com/accelerator"): "7",
			} {
				got, exists := containers[0].Resources.Limits[name]
				if !exists || got.Cmp(resource.MustParse(want)) != 0 {
					t.Errorf("executor limit %q = %v, want %s", name, got, want)
				}
			}
			if got, exists := containers[0].Resources.Requests[corev1.ResourceCPU]; !exists || got.Cmp(resource.MustParse("1")) != 0 {
				t.Errorf("executor CPU request = %v, want 1", got)
			}
			if got, exists := containers[1].Resources.Limits[corev1.ResourceMemory]; !exists || got.Cmp(resource.MustParse("64Mi")) != 0 {
				t.Errorf("sidecar memory limit = %v, want 64Mi", got)
			}
		})
	}
}

func TestRunRejectsConflictingGPUOptionsBeforeSubmittingJob(t *testing.T) {
	tests := []struct {
		name         string
		parameters   map[string]string
		nodeSelector map[string]string
		template     string
	}{
		{
			name: "admin node selector conflict",
			parameters: map[string]string{
				"gpu_node_selector": `admin=task-value`,
			},
			nodeSelector: map[string]string{"admin": "required"},
			template:     executorGPUCountTemplate,
		},
		{
			name: "template node selector conflict",
			parameters: map[string]string{
				"gpu_node_selector": `template=task-value`,
			},
			template: executorGPUCountTemplate,
		},
		{
			name: "resource limit conflict",
			parameters: map[string]string{
				"gpu_count": "2",
			},
			template: strings.Replace(executorGPUCountTemplate, `example.com/accelerator: "7"`, `nvidia.com/gpu: "1"`, 1),
		},
		{
			name: "resource request conflict",
			parameters: map[string]string{
				"gpu_count": "2",
			},
			template: strings.Replace(executorGPUCountTemplate, `memory: "2Gi"`, "memory: \"2Gi\"\n            nvidia.com/gpu: \"1\"", 1),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conf := config.DefaultConfig()
			const jobsNamespace = "test-jobs"
			worker := DefaultWorker{
				Executor: Executor{
					Template:      tt.template,
					Namespace:     jobsNamespace,
					JobsNamespace: jobsNamespace,
					NodeSelector:  tt.nodeSelector,
					Resources:     conf.Kubernetes.Resources,
				},
				EventWriter: events.Noop{},
			}
			task := &tes.Task{Id: "gpu-conflict", Resources: &tes.Resources{BackendParameters: tt.parameters}}
			command, err := worker.newKubernetesCommand(context.Background(), events.NewTaskWriter(task.Id, 0, events.Noop{}), task, &tes.Executor{}, 0,
				Command{Image: "executor:test", ShellCommand: []string{"true"}}, task.Resources)
			if err != nil {
				t.Fatalf("newKubernetesCommand: %v", err)
			}
			client := fake.NewSimpleClientset()
			client.PrependWatchReactor("pods", func(k8stesting.Action) (bool, watch.Interface, error) {
				return true, nil, errors.New("stop after executor job submission")
			})
			command.Clientset = client
			if err := command.Run(context.Background()); err == nil {
				t.Fatal("Run accepted conflicting GPU options")
			}
			jobs, err := client.BatchV1().Jobs(jobsNamespace).List(context.Background(), metav1.ListOptions{})
			if err != nil {
				t.Fatalf("list executor jobs: %v", err)
			}
			if len(jobs.Items) != 0 {
				t.Fatalf("submitted %d executor jobs with conflicting GPU options", len(jobs.Items))
			}
		})
	}
}

func TestNewKubernetesCommandRejectsInvalidGPUCount(t *testing.T) {
	conf := config.DefaultConfig()
	worker := DefaultWorker{
		Executor:    Executor{Resources: conf.Kubernetes.Resources},
		EventWriter: events.Noop{},
	}
	for _, value := range []string{"", "0", "-1", "+1", "1.5", " 2", "9223372036854775808"} {
		t.Run(value, func(t *testing.T) {
			task := &tes.Task{Id: "invalid-gpu-task", Resources: &tes.Resources{
				BackendParameters: map[string]string{"gpu_count": value},
			}}
			_, err := worker.newKubernetesCommand(context.Background(), events.NewTaskWriter(task.Id, 0, events.Noop{}), task, &tes.Executor{}, 0,
				Command{}, task.Resources)
			if err == nil {
				t.Fatalf("newKubernetesCommand accepted gpu_count %q", value)
			}
		})
	}
}

func TestRunUsesNamedExecutorForLogsAndExitClassification(t *testing.T) {
	var mu sync.Mutex
	var logContainer string
	var failLogs atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("Kubernetes request %s %s", r.Method, r.URL.String())
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/apis/batch/v1/namespaces/jobs/jobs":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(&batchv1.Job{
				TypeMeta:   metav1.TypeMeta{APIVersion: "batch/v1", Kind: "Job"},
				ObjectMeta: metav1.ObjectMeta{Name: "task-123-0", Namespace: "jobs"},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/namespaces/jobs/pods" && r.URL.Query().Get("watch") == "true":
			pod, _ := json.Marshal(&corev1.Pod{
				TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"},
				ObjectMeta: metav1.ObjectMeta{Name: "task-123-0-pod", Namespace: "jobs", ResourceVersion: "1"},
				Status: corev1.PodStatus{
					Phase: corev1.PodFailed,
					ContainerStatuses: []corev1.ContainerStatus{
						{Name: "sidecar", State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 0}}},
						{Name: "task-runner", State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 23, Reason: "Error", Message: "executor failed"}}},
					},
				},
			})
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(struct {
				Type   watch.EventType `json:"type"`
				Object json.RawMessage `json:"object"`
			}{Type: watch.Modified, Object: pod})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/log"):
			if failLogs.Load() {
				http.Error(w, "log stream unavailable", http.StatusInternalServerError)
				return
			}
			mu.Lock()
			logContainer = r.URL.Query().Get("container")
			mu.Unlock()
			w.Header().Set("Content-Type", "text/plain")
			_, _ = io.WriteString(w, "executor output")
		default:
			http.Error(w, fmt.Sprintf("unexpected Kubernetes request %s %s", r.Method, r.URL.String()), http.StatusNotFound)
		}
	}))
	defer server.Close()

	clientset, err := kubernetes.NewForConfig(&rest.Config{Host: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	var stdout strings.Builder
	plan := &storageplan.Plan{
		Version: 4, TaskID: "task-123", Namespace: "jobs", WorkDir: "/work",
		WorkspaceVolume: "workspace", ExecutorContainer: "task-runner",
		Volumes: []corev1.Volume{{Name: "workspace", VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "task-123-workspace"},
		}}},
	}
	cmd := KubernetesCommand{
		StoragePlan: plan,
		TaskId:      "task-123", JobId: 0, JobsNamespace: "jobs",
		TaskTemplate: `apiVersion: batch/v1
kind: Job
metadata:
  name: {{.TaskId}}-{{.JobId}}
  namespace: {{.JobsNamespace}}
spec:
  template:
    spec:
      restartPolicy: Never
      containers:
      - name: sidecar
        image: busybox
      - name: task-runner
        image: busybox
`,
		Resources: &tes.Resources{}, ResourceLimits: &tes.Resources{},
		Clientset: clientset,
		Command:   Command{ShellCommand: []string{"true"}, Stdout: &stdout},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = cmd.Run(ctx)
	var executorErr *K8sExecutorErr
	if !errors.As(err, &executorErr) {
		t.Fatalf("Run error = %v, want K8sExecutorErr for task-runner exit 23", err)
	}
	if executorErr.ExitCode != 23 {
		t.Fatalf("executor exit code = %d, want 23", executorErr.ExitCode)
	}
	if executorErr.Message != "executor failed" {
		t.Fatalf("executor message = %q, want %q", executorErr.Message, "executor failed")
	}
	if stdout.String() != "executor output" {
		t.Fatalf("stdout = %q, want %q", stdout.String(), "executor output")
	}

	mu.Lock()
	if logContainer != "task-runner" {
		t.Fatalf("log container query = %q, want task-runner", logContainer)
	}
	mu.Unlock()

	failLogs.Store(true)
	builder := &events.TaskBuilder{Task: &tes.Task{}}
	cmd.Event = events.NewExecutorWriter("task-123", 0, 0, builder)
	err = cmd.Run(ctx)
	var systemErr *K8sSystemErr
	if !errors.As(err, &systemErr) || systemErr.Reason != "LogStreamingFailed" {
		t.Fatalf("Run error = %v, want LogStreamingFailed", err)
	}
	if got := builder.Task.Logs[0].Logs[0].GetExitCode(); got != 23 {
		t.Fatalf("exit code after log stream failure = %d, want 23", got)
	}
}
