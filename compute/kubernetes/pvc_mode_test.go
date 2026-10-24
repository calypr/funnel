package kubernetes

import (
	"context"
	"os"
	"testing"

	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/logger"
	"github.com/ohsu-comp-bio/funnel/tes"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// pvcModeTestConfig returns a config using the real worker/PV/PVC templates
// shipped with Funnel (config/kubernetes/*.yaml) for the given PVCMode.
func pvcModeTestConfig(t *testing.T, pvcMode string) *config.Config {
	t.Helper()
	read := func(name string) string {
		b, err := os.ReadFile("../../config/kubernetes/" + name)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		return string(b)
	}

	conf := config.DefaultConfig()
	conf.Kubernetes.Namespace = "test-namespace"
	conf.Kubernetes.JobsNamespace = "test-namespace"
	conf.Kubernetes.WorkerTemplate = read("worker-job.yaml")
	conf.Kubernetes.PVTemplate = read("worker-pv.yaml")
	conf.Kubernetes.PVCTemplate = read("worker-pvc.yaml")
	conf.Kubernetes.PVCMode = pvcMode
	if pvcMode == config.PVCModePVC {
		conf.Kubernetes.StorageClassName = "nfs-csi"
	}
	conf.GenericS3 = []*config.GenericS3Storage{{Bucket: "bucket", Region: "region"}}
	return conf
}

// forbidPVs makes every request for (cluster-scoped) PersistentVolumes fail
// with Forbidden, emulating a namespaced deployment without a ClusterRole.
func forbidPVs(client *fake.Clientset) {
	client.PrependReactor("*", "persistentvolumes", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(
			schema.GroupResource{Resource: "persistentvolumes"}, "", nil)
	})
}

func TestSubmit_PVCModes(t *testing.T) {
	cases := []struct {
		mode             string
		wantPV           bool
		wantPVC          bool
		wantStorageClass string
		wantClaimName    string
	}{
		{config.PVCModeFull, true, true, "", "funnel-worker-pvc-task1"},
		{config.PVCModePVC, false, true, "nfs-csi", "funnel-worker-pvc-task1"},
		{config.PVCModeShared, false, false, "", "funnel-pvc"},
	}

	for _, c := range cases {
		t.Run(c.mode, func(t *testing.T) {
			ctx := context.Background()
			client := fake.NewSimpleClientset()
			if !c.wantPV {
				forbidPVs(client)
			}
			conf := pvcModeTestConfig(t, c.mode)
			b := &Backend{
				client: client,
				event:  &noopEventWriter{},
				log:    logger.NewLogger("test", logger.DefaultConfig()),
				conf:   conf,
			}

			task := &tes.Task{
				Id:        "task1",
				Resources: &tes.Resources{DiskGb: 5},
				Inputs:    []*tes.Input{{Url: "s3://bucket/file", Path: "/data/file"}},
				Executors: []*tes.Executor{{Image: "alpine", Command: []string{"echo"}}},
			}

			if err := b.Submit(ctx, task, conf); err != nil {
				t.Fatalf("Submit: %v", err)
			}

			if c.wantPV {
				if _, err := client.CoreV1().PersistentVolumes().Get(ctx, "funnel-worker-pv-task1", metav1.GetOptions{}); err != nil {
					t.Errorf("expected PV to be created: %v", err)
				}
			}

			pvc, err := client.CoreV1().PersistentVolumeClaims("test-namespace").Get(ctx, "funnel-worker-pvc-task1", metav1.GetOptions{})
			if c.wantPVC {
				if err != nil {
					t.Fatalf("expected PVC to be created: %v", err)
				}
				gotClass := ""
				if pvc.Spec.StorageClassName != nil {
					gotClass = *pvc.Spec.StorageClassName
				}
				if gotClass != c.wantStorageClass {
					t.Errorf("PVC storageClassName = %q, want %q", gotClass, c.wantStorageClass)
				}
				if c.wantStorageClass != "" && pvc.Spec.VolumeName != "" {
					t.Errorf("dynamically-provisioned PVC must not bind a volumeName, got %q", pvc.Spec.VolumeName)
				}
			} else if err == nil {
				t.Errorf("expected no per-task PVC in mode %q", c.mode)
			}

			job, err := client.BatchV1().Jobs("test-namespace").Get(ctx, "task1", metav1.GetOptions{})
			if err != nil {
				t.Fatalf("expected worker Job to be created: %v", err)
			}
			var claim string
			for _, v := range job.Spec.Template.Spec.Volumes {
				if v.PersistentVolumeClaim != nil {
					claim = v.PersistentVolumeClaim.ClaimName
				}
			}
			if claim != c.wantClaimName {
				t.Errorf("worker Job PVC claimName = %q, want %q", claim, c.wantClaimName)
			}

			// Cleanup must not touch PVs outside PVCModeFull (they would be
			// Forbidden without a ClusterRole).
			if err := b.cleanResources(ctx, task.Id); err != nil {
				t.Errorf("cleanResources: %v", err)
			}
		})
	}
}

func TestSubmit_PVCModeFullRequiresS3(t *testing.T) {
	conf := pvcModeTestConfig(t, config.PVCModeFull)
	conf.GenericS3 = nil
	b := &Backend{
		client: fake.NewSimpleClientset(),
		event:  &noopEventWriter{},
		log:    logger.NewLogger("test", logger.DefaultConfig()),
		conf:   conf,
	}
	task := &tes.Task{
		Id:        "task1",
		Inputs:    []*tes.Input{{Url: "s3://bucket/file", Path: "/data/file"}},
		Executors: []*tes.Executor{{Image: "alpine", Command: []string{"echo"}}},
	}
	if err := b.Submit(context.Background(), task, conf); err == nil {
		t.Error("expected an error when GenericS3 Bucket/Region is missing in PVCMode full")
	}

	// The other modes do not need S3 at all.
	for _, mode := range []string{config.PVCModePVC, config.PVCModeShared} {
		conf := pvcModeTestConfig(t, mode)
		conf.GenericS3 = nil
		b.conf = conf
		b.client = fake.NewSimpleClientset()
		if err := b.Submit(context.Background(), task, conf); err != nil {
			t.Errorf("mode %q: unexpected error without S3: %v", mode, err)
		}
	}
}

// TestNamespacedRBAC_NoClusterOrSACalls verifies that, with a static
// (Helm-managed) ServiceAccount and PVCMode "shared"/"pvc", neither task
// cleanup nor orphan cleanup touches PersistentVolumes or ServiceAccounts, so
// a deployment with namespaced RBAC only (no ClusterRole, no serviceaccounts
// permissions) runs without Forbidden errors.
func TestNamespacedRBAC_NoClusterOrSACalls(t *testing.T) {
	for _, mode := range []string{config.PVCModePVC, config.PVCModeShared} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			client := fake.NewSimpleClientset()
			var forbidden []string
			client.PrependReactor("*", "*", func(action k8stesting.Action) (bool, runtime.Object, error) {
				res := action.GetResource().Resource
				if res == "persistentvolumes" || res == "serviceaccounts" {
					forbidden = append(forbidden, action.GetVerb()+" "+res)
					return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: res}, "", nil)
				}
				return false, nil, nil
			})

			conf := pvcModeTestConfig(t, mode)
			b := &Backend{
				client:   client,
				event:    &noopEventWriter{},
				database: &mockDatabase{tasks: map[string]*tes.Task{}},
				log:      logger.NewLogger("test", logger.DefaultConfig()),
				conf:     conf,
			}

			task := &tes.Task{
				Id:        "task1",
				Inputs:    []*tes.Input{{Url: "s3://bucket/file", Path: "/data/file"}},
				Executors: []*tes.Executor{{Image: "alpine", Command: []string{"echo"}}},
			}
			if err := b.Submit(ctx, task, conf); err != nil {
				t.Fatalf("Submit: %v", err)
			}
			if err := b.cleanResources(ctx, task.Id); err != nil {
				t.Errorf("cleanResources: %v", err)
			}
			b.CleanOrphanedResources(ctx)

			if len(forbidden) > 0 {
				t.Errorf("unexpected calls requiring extra RBAC: %v", forbidden)
			}
		})
	}
}

// TestSubmit_WorkerImage verifies that Kubernetes.WorkerImage, when set,
// overrides the image of the Worker Job (by default the image of the running
// Funnel server pod).
func TestSubmit_WorkerImage(t *testing.T) {
	for _, workerImage := range []string{"", "example.org/funnel:test"} {
		t.Run(workerImage, func(t *testing.T) {
			ctx := context.Background()
			client := fake.NewSimpleClientset()
			conf := pvcModeTestConfig(t, config.PVCModeShared)
			conf.Kubernetes.WorkerImage = workerImage
			b := &Backend{
				client: client,
				event:  &noopEventWriter{},
				log:    logger.NewLogger("test", logger.DefaultConfig()),
				conf:   conf,
			}

			task := &tes.Task{
				Id:        "task1",
				Executors: []*tes.Executor{{Image: "alpine", Command: []string{"echo"}}},
			}
			if err := b.Submit(ctx, task, conf); err != nil {
				t.Fatalf("Submit: %v", err)
			}

			job, err := client.BatchV1().Jobs("test-namespace").Get(ctx, "task1", metav1.GetOptions{})
			if err != nil {
				t.Fatalf("expected worker Job to be created: %v", err)
			}
			if got := job.Spec.Template.Spec.Containers[0].Image; got != workerImage {
				t.Errorf("worker Job image = %q, want %q", got, workerImage)
			}
		})
	}
}
