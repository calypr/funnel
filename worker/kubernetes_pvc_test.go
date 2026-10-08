package worker

import (
	"context"
	"io"
	"os"
	"testing"
	"time"

	"github.com/ohsu-comp-bio/funnel/tes"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// TestKubernetesCommand_pvcName verifies pvcName switches between the shared
// PVC name and a per-task PVC name based on PVCMode, matching the
// server-side naming (config.Kubernetes.PVCNameForTask).
func TestKubernetesCommand_pvcName(t *testing.T) {
	cases := []struct {
		name          string
		pvcMode       string
		sharedPVCName string
		want          string
	}{
		{"empty: defaults to full", "", "", "funnel-worker-pvc-task1"},
		{"full: per-task PVC", "full", "", "funnel-worker-pvc-task1"},
		{"pvc: per-task PVC", "pvc", "", "funnel-worker-pvc-task1"},
		{"shared: default shared PVC", "shared", "", "funnel-pvc"},
		{"shared: custom shared PVC", "shared", "my-pvc", "my-pvc"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			kcmd := KubernetesCommand{TaskId: "task1", PVCMode: c.pvcMode, SharedPVCName: c.sharedPVCName}
			if got := kcmd.pvcName(); got != c.want {
				t.Errorf("pvcName() = %q, want %q", got, c.want)
			}
		})
	}
}

// TestExecutorJob_PVCName runs the executor against a fake clientset using the
// real executor-job.yaml shipped with Funnel, and verifies the created Job
// mounts the PVC matching PVCMode.
func TestExecutorJob_PVCName(t *testing.T) {
	content, err := os.ReadFile("../config/kubernetes/executor-job.yaml")
	if err != nil {
		t.Fatalf("reading executor template: %v", err)
	}

	cases := []struct {
		name    string
		pvcMode string
		want    string
	}{
		{"full: mounts per-task PVC", "full", "funnel-worker-pvc-task1"},
		{"pvc: mounts per-task PVC", "pvc", "funnel-worker-pvc-task1"},
		{"shared: mounts shared PVC", "shared", "funnel-pvc"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			clientset := fake.NewSimpleClientset()
			kcmd := KubernetesCommand{
				TaskId:         "task1",
				JobId:          0,
				Namespace:      testNamespace,
				JobsNamespace:  testNamespace,
				TaskTemplate:   string(content),
				PVCMode:        c.pvcMode,
				NeedsPVC:       true,
				Resources:      &tes.Resources{},
				ResourceLimits: &tes.Resources{},
				Clientset:      clientset,
				Command: Command{
					Image:        "alpine",
					ShellCommand: []string{"echo", "hello"},
					Volumes:      []Volume{{ContainerPath: "/data"}},
					Stdout:       io.Discard,
					Stderr:       io.Discard,
				},
			}

			// Run blocks until the executor pod finishes, which never happens
			// with the fake clientset; the Job is created before that.
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			_ = kcmd.Run(ctx)

			job, err := clientset.BatchV1().Jobs(testNamespace).Get(context.Background(), "task1-0", metav1.GetOptions{})
			if err != nil {
				t.Fatalf("executor Job was not created: %v", err)
			}

			var got string
			for _, v := range job.Spec.Template.Spec.Volumes {
				if v.PersistentVolumeClaim != nil {
					got = v.PersistentVolumeClaim.ClaimName
				}
			}
			if got != c.want {
				t.Errorf("executor Job PVC claimName = %q, want %q", got, c.want)
			}
		})
	}
}
