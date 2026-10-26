package kubernetes

import (
	"context"
	"testing"

	"github.com/calypr/funnel/tes"
	batchv1 "k8s.io/api/batch/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestBacklogCleanupHonorsDisableJobCleanup(t *testing.T) {
	for _, disabled := range []bool{true, false} {
		name := "enabled"
		if disabled {
			name = "disabled"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			completed := reconcileTestJob("completed", batchv1.JobStatus{Succeeded: 1})
			running := reconcileTestJob("running", batchv1.JobStatus{Active: 1})
			client := fake.NewSimpleClientset(completed, running)
			database := &reconcileTaskDatabase{task: &tes.Task{Id: running.Name, State: tes.State_RUNNING}}
			backend := newReconcileTestBackend(client, database, &noopEventWriter{})
			backend.conf.Kubernetes.DisableJobCleanup = disabled

			backend.cleanBacklog(ctx, backend.conf.Kubernetes.DisableJobCleanup)

			if disabled && len(client.Actions()) != 0 {
				t.Errorf("disabled cleanup made Kubernetes calls: %v", client.Actions())
			}
			_, err := client.BatchV1().Jobs(reconcileTestNamespace).Get(ctx, completed.Name, metav1.GetOptions{})
			if disabled && err != nil {
				t.Fatalf("completed job must remain when cleanup is disabled: %v", err)
			}
			if !disabled && !apierrors.IsNotFound(err) {
				t.Fatalf("completed job must be deleted when cleanup is enabled, got: %v", err)
			}
			if _, err := client.BatchV1().Jobs(reconcileTestNamespace).Get(ctx, running.Name, metav1.GetOptions{}); err != nil {
				t.Fatalf("running task's job must remain: %v", err)
			}
		})
	}
}
