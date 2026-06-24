package worker

import (
	"context"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

const (
	testNamespace = "funnel"
	testSelector  = "job-name=task-0"
)

func runningPod(resourceVersion string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "task-0-abc",
			Namespace:       testNamespace,
			ResourceVersion: resourceVersion,
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
}

func terminatedPod(resourceVersion string, exitCode int32) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "task-0-abc",
			Namespace:       testNamespace,
			ResourceVersion: resourceVersion,
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{ExitCode: exitCode},
					},
				},
			},
		},
	}
}

// TestWaitForPodFinishReestablishesWatchOnTimeout simulates the Kubernetes API
// server closing a long-lived watch connection (the ~20-30 min watch timeout
// that previously caused long-running tasks to fail with "received nil pod
// object from watcher"). The first watch emits a Running pod then closes its
// channel; the second watch emits the terminated pod. waitForPodFinish must
// transparently re-establish the watch and return the terminated pod rather
// than erroring out.
func TestWaitForPodFinishReestablishesWatchOnTimeout(t *testing.T) {
	clientset := fake.NewSimpleClientset()

	var mu sync.Mutex
	var watchCount int
	var resumedFrom []string

	clientset.PrependWatchReactor("pods", func(action k8stesting.Action) (bool, watch.Interface, error) {
		mu.Lock()
		defer mu.Unlock()
		watchCount++
		current := watchCount

		// Record the resourceVersion the watch was resumed from.
		if wa, ok := action.(k8stesting.WatchActionImpl); ok {
			resumedFrom = append(resumedFrom, wa.WatchRestrictions.ResourceVersion)
		}

		fw := watch.NewFake()
		switch current {
		case 1:
			// First watch: emit a Running pod, then simulate the server
			// closing the connection (watch timeout) by stopping the watcher,
			// which closes its result channel.
			go func() {
				fw.Modify(runningPod("100"))
				time.Sleep(20 * time.Millisecond)
				fw.Stop()
			}()
		default:
			// Re-established watch: emit the terminated pod.
			go func() {
				fw.Modify(terminatedPod("101", 0))
			}()
		}
		return true, fw, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pod, err := waitForPodFinish(ctx, clientset, testNamespace, testSelector)
	if err != nil {
		t.Fatalf("expected pod to finish, got error: %v", err)
	}
	if pod == nil {
		t.Fatal("expected non-nil pod")
	}
	if len(pod.Status.ContainerStatuses) == 0 || pod.Status.ContainerStatuses[0].State.Terminated == nil {
		t.Fatal("expected terminated container status")
	}

	mu.Lock()
	defer mu.Unlock()
	if watchCount < 2 {
		t.Fatalf("expected watch to be re-established at least once, watchCount=%d", watchCount)
	}
	// The re-established watch should resume from the last observed
	// resourceVersion rather than starting over.
	if len(resumedFrom) < 2 || resumedFrom[1] != "100" {
		t.Fatalf("expected re-established watch to resume from resourceVersion 100, got %v", resumedFrom)
	}
}

// TestWaitForPodFinishReturnsTerminatedPod verifies the happy path: a single
// watch that emits a terminated pod returns it without error.
func TestWaitForPodFinishReturnsTerminatedPod(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	clientset.PrependWatchReactor("pods", func(_ k8stesting.Action) (bool, watch.Interface, error) {
		fw := watch.NewFake()
		go func() {
			fw.Modify(terminatedPod("1", 0))
		}()
		return true, fw, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pod, err := waitForPodFinish(ctx, clientset, testNamespace, testSelector)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pod == nil || len(pod.Status.ContainerStatuses) == 0 {
		t.Fatal("expected terminated pod")
	}
}

// TestWaitForPodFinishRestartsOnExpiredResourceVersion verifies that an
// "Expired" / "Gone" watch error (HTTP 410, too-old resourceVersion) restarts
// the watch from scratch instead of failing the task.
func TestWaitForPodFinishRestartsOnExpiredResourceVersion(t *testing.T) {
	clientset := fake.NewSimpleClientset()

	var mu sync.Mutex
	var watchCount int

	clientset.PrependWatchReactor("pods", func(_ k8stesting.Action) (bool, watch.Interface, error) {
		mu.Lock()
		defer mu.Unlock()
		watchCount++
		current := watchCount

		fw := watch.NewFake()
		switch current {
		case 1:
			go func() {
				fw.Modify(runningPod("100"))
				time.Sleep(20 * time.Millisecond)
				fw.Error(&metav1.Status{Reason: metav1.StatusReasonExpired, Message: "too old resource version"})
			}()
		default:
			go func() {
				fw.Modify(terminatedPod("200", 0))
			}()
		}
		return true, fw, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pod, err := waitForPodFinish(ctx, clientset, testNamespace, testSelector)
	if err != nil {
		t.Fatalf("expected watch to restart on expired resourceVersion, got error: %v", err)
	}
	if pod == nil || len(pod.Status.ContainerStatuses) == 0 {
		t.Fatal("expected terminated pod after restart")
	}

	mu.Lock()
	defer mu.Unlock()
	if watchCount < 2 {
		t.Fatalf("expected watch to restart after expiry, watchCount=%d", watchCount)
	}
}
