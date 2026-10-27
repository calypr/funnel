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
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/tes"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
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
