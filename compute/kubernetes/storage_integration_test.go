package kubernetes

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ohsu-comp-bio/funnel/compute/kubernetes/storageplan"
	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/events"
	"github.com/ohsu-comp-bio/funnel/logger"
	"github.com/ohsu-comp-bio/funnel/tes"
	"google.golang.org/protobuf/types/known/structpb"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// TestNativeStorageLive verifies shared filesystem mounts and Kubernetes GC.
// It requires a disposable cluster with the standard StorageClass and alpine:3.21.
func TestNativeStorageLive(t *testing.T) {
	kubeconfig := os.Getenv("FUNNEL_KUBERNETES_TEST_KUBECONFIG")
	if kubeconfig == "" {
		t.Skip("set FUNNEL_KUBERNETES_TEST_KUBECONFIG to run against a disposable cluster")
	}
	restConfig, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		t.Fatal(err)
	}
	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name                             string
		external, pair, removeAnnotation bool
	}{{name: "dynamicPVC"}, {name: "existingClaim", external: true}, {name: "explicitPV", pair: true}, {name: "explicitPVWithoutAnnotation", pair: true, removeAnnotation: true}} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()
			namespace, err := client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "funnel-storage-live-"}}, metav1.CreateOptions{})
			if err != nil {
				t.Fatal(err)
			}
			ns := namespace.Name
			t.Cleanup(func() {
				cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cleanupCancel()
				if err := client.CoreV1().Namespaces().Delete(cleanupCtx, ns, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
					t.Errorf("deleting test namespace: %v", err)
				}
			})
			defer func() {
				if t.Failed() {
					logLiveStorageFailure(t, client, ns)
				}
			}()
			conf := config.DefaultConfig()
			conf.Worker.WorkDir = "/work"
			conf.Kubernetes.Namespace, conf.Kubernetes.JobsNamespace = ns, ns
			conf.Kubernetes.DisableReconciler = true
			conf.Kubernetes.ConfigMapTemplate, conf.Kubernetes.ServiceAccountTemplate, conf.Kubernetes.RoleTemplate, conf.Kubernetes.RoleBindingTemplate = "", "", "", ""
			conf.Kubernetes.WorkerTemplate = liveStorageWorkerTemplate
			claimTemplate := liveStorageClaimTemplate
			storageConfig := &config.KubernetesStorage{
				WorkspaceVolume:      "workspace",
				VolumeClaimTemplates: []*structpb.Struct{nativeResource(t, claimTemplate)},
			}
			if tc.pair {
				claimTemplate = strings.Replace(liveStorageClaimTemplate, "storageClassName: standard", "storageClassName: ''", 1)
				claimTemplate = strings.Replace(claimTemplate, "spec:\n", "spec:\n  volumeName: data-{{.TaskId}}\n", 1)
				storageConfig.VolumeClaimTemplates = []*structpb.Struct{nativeResource(t, claimTemplate)}
				storageConfig.PersistentVolumeTemplates = []*structpb.Struct{nativeResource(t, liveStorageVolumeTemplate)}
			}
			if tc.external {
				storageClass := "standard"
				_, err := client.CoreV1().PersistentVolumeClaims(ns).Create(ctx, &corev1.PersistentVolumeClaim{
					ObjectMeta: metav1.ObjectMeta{Name: "operator-workspace", Namespace: ns},
					Spec:       corev1.PersistentVolumeClaimSpec{AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}, StorageClassName: &storageClass, Resources: corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("1Gi")}}},
				}, metav1.CreateOptions{})
				if err != nil {
					t.Fatal(err)
				}
				storageConfig.Volumes = []*structpb.Struct{nativeResource(t, `name: workspace
persistentVolumeClaim:
  claimName: operator-workspace`)}
				storageConfig.VolumeClaimTemplates = nil
			}
			conf.Kubernetes.Storage = storageConfig
			backend := &Backend{client: client, conf: conf, event: events.Noop{}, log: logger.NewLogger("storage-live", logger.DefaultConfig())}
			task := &tes.Task{Id: fmt.Sprintf("task-%d", time.Now().UnixNano())}
			if err := backend.Submit(ctx, task, conf); err != nil {
				t.Fatalf("submitting worker: %v", err)
			}
			worker, err := client.BatchV1().Jobs(ns).Get(ctx, task.Id, metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			plan, err := storageplan.Decode(worker.Annotations[storageplan.Annotation])
			if err != nil {
				t.Fatalf("reading persisted plan: %v", err)
			}
			claimName := plan.Volumes[0].PersistentVolumeClaim.ClaimName
			wantClaimName := task.Id + "-workspace"
			if tc.external {
				wantClaimName = "operator-workspace"
			}
			if claimName != wantClaimName {
				t.Fatalf("unexpected workspace claim name: got %q, want %q", claimName, wantClaimName)
			}
			if tc.external && (len(plan.OwnedClaims) != 0 || len(plan.OwnedVolumes) != 0) {
				t.Fatal("operator claim became task-owned")
			}
			if !tc.external && (len(plan.OwnedClaims) != 1 || plan.OwnedClaims[0] != claimName) {
				t.Fatal("generated claim ownership was not persisted")
			}
			if tc.pair && (len(plan.OwnedVolumes) != 1 || plan.OwnedVolumes[0] != "data-"+task.Id) {
				t.Fatal("generated PV ownership was not persisted")
			}
			if tc.pair && !slices.Contains(worker.Finalizers, storageplan.Finalizer) {
				t.Fatal("worker lacks owned-PV cleanup finalizer")
			}
			deadline := int64(110)
			executor := &batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{Name: task.Id + "-0", Namespace: ns, Labels: map[string]string{"app": "funnel-executor"}},
				Spec:       batchv1.JobSpec{BackoffLimit: new(int32), ActiveDeadlineSeconds: &deadline, Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{RestartPolicy: corev1.RestartPolicyNever, Containers: []corev1.Container{{Name: "executor", Image: "alpine:3.21", ImagePullPolicy: corev1.PullIfNotPresent, Command: []string{"/bin/sh", "-ec"}, Args: []string{"for i in $(seq 1 90); do if test -s /inputs/data; then tr a-z A-Z < /inputs/data > /outputs/result; exit 0; fi; sleep 1; done; exit 1"}}}}}},
			}
			if err := plan.ApplyExecutor(executor, task.Id, []corev1.VolumeMount{{MountPath: "/inputs"}, {MountPath: "/outputs"}}); err != nil {
				t.Fatalf("applying executor mounts: %v", err)
			}
			if _, err := client.BatchV1().Jobs(ns).Create(ctx, executor, metav1.CreateOptions{}); err != nil {
				t.Fatalf("creating executor: %v", err)
			}
			if err := waitLiveStorageJob(ctx, client, ns, executor.Name); err != nil {
				t.Fatal(err)
			}
			if err := waitLiveStorageJob(ctx, client, ns, worker.Name); err != nil {
				t.Fatal(err)
			}
			t.Log("executor read worker-created hello; worker verified executor-created HELLO through the shared claim")
			if tc.pair {
				pv, err := client.CoreV1().PersistentVolumes().Get(ctx, plan.OwnedVolumes[0], metav1.GetOptions{})
				if err != nil {
					t.Fatal(err)
				}
				if pv.Spec.ClaimRef == nil || pv.Spec.ClaimRef.Name != claimName || pv.Spec.ClaimRef.Namespace != ns {
					t.Fatalf("PV claim reference must use the generated claim name: %+v", pv.Spec.ClaimRef)
				}
				if !slices.Contains(pv.Finalizers, "kubernetes.io/pv-protection") {
					t.Fatal("bound PV lacks native protection finalizer")
				}
			}
			if tc.removeAnnotation {
				patch := []byte(`{"metadata":{"annotations":{"` + storageplan.Annotation + `":null}}}`)
				if _, err := client.BatchV1().Jobs(ns).Patch(ctx, task.Id, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
					t.Fatalf("removing storage annotation: %v", err)
				}
				recovered, ownerUID, err := backend.readTaskStorage(ctx, task.Id)
				if err != nil || recovered == nil || ownerUID != worker.UID || len(recovered.OwnedVolumes) != 1 || recovered.OwnedVolumes[0] != plan.OwnedVolumes[0] {
					t.Fatalf("recovering storage plan from worker environment: plan=%+v owner=%q error=%v", recovered, ownerUID, err)
				}
			}
			if err := backend.Cancel(ctx, task.Id); err != nil {
				t.Fatalf("cancel cleanup: %v", err)
			}
			if tc.external {
				claim, err := client.CoreV1().PersistentVolumeClaims(ns).Get(ctx, claimName, metav1.GetOptions{})
				if err != nil {
					t.Fatalf("operator claim did not survive cleanup: %v", err)
				}
				if claim.DeletionTimestamp != nil || len(claim.OwnerReferences) != 0 {
					t.Fatal("operator claim was attached to task cleanup")
				}
				t.Log("operator-managed claim survived cancellation")
			} else {
				if err := waitLiveStorageClaimDeleted(ctx, client, ns, claimName); err != nil {
					t.Fatal(err)
				}
				t.Log("task-owned claim was garbage-collected after cancellation")
				if tc.pair {
					if err := waitLiveStorageVolumeDeleted(ctx, client, plan.OwnedVolumes[0]); err != nil {
						t.Fatal(err)
					}
					t.Log("task-owned PV was deleted after PVC GC released native PV protection")
				}
			}
		})
	}
}

const liveStorageWorkerTemplate = `apiVersion: batch/v1
kind: Job
metadata:
  name: {{.TaskId}}
  namespace: {{.JobsNamespace}}
  labels:
    app: funnel-worker
spec:
  backoffLimit: 0
  activeDeadlineSeconds: 110
  template:
    spec:
      restartPolicy: Never
      containers:
      - name: worker
        image: alpine:3.21
        imagePullPolicy: IfNotPresent
        command: ["/bin/sh", "-ec"]
        args:
        - |
          mkdir -p /work/{{.TaskId}}/inputs /work/{{.TaskId}}/outputs
          echo hello > /work/{{.TaskId}}/inputs/data
          for i in $(seq 1 90); do
            if test -s /work/{{.TaskId}}/outputs/result; then
              test "$(cat /work/{{.TaskId}}/outputs/result)" = HELLO
              exit 0
            fi
            sleep 1
          done
          exit 1
`

const liveStorageClaimTemplate = `metadata:
  name: workspace
  namespace: "{{.Namespace}}"
spec:
  accessModes: [ReadWriteOnce]
  storageClassName: standard
  resources:
    requests:
      storage: 1Gi
`

const liveStorageVolumeTemplate = `apiVersion: v1
kind: PersistentVolume
metadata:
  name: data-{{.TaskId}}
spec:
  accessModes: [ReadWriteOnce]
  storageClassName: ''
  persistentVolumeReclaimPolicy: Retain
  capacity:
    storage: 1Gi
  hostPath:
    path: /tmp/funnel-storage-{{.TaskId}}
    type: DirectoryOrCreate
`

func waitLiveStorageJob(ctx context.Context, client kubernetes.Interface, ns, name string) error {
	for {
		job, err := client.BatchV1().Jobs(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("waiting for job %s: %w", name, err)
		}
		for _, condition := range job.Status.Conditions {
			if condition.Status != corev1.ConditionTrue {
				continue
			}
			if condition.Type == batchv1.JobFailed {
				return fmt.Errorf("job %s failed: %s: %s", name, condition.Reason, condition.Message)
			}
			if condition.Type == batchv1.JobComplete {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for job %s: %w", name, ctx.Err())
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func waitLiveStorageClaimDeleted(ctx context.Context, client kubernetes.Interface, ns, name string) error {
	for {
		_, err := client.CoreV1().PersistentVolumeClaims(ns).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("waiting for claim cleanup: %w", err)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("claim %s was not garbage-collected: %w", name, ctx.Err())
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func waitLiveStorageVolumeDeleted(ctx context.Context, client kubernetes.Interface, name string) error {
	for {
		_, err := client.CoreV1().PersistentVolumes().Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("waiting for PV cleanup: %w", err)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("PV %s was not deleted: %w", name, ctx.Err())
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func logLiveStorageFailure(t *testing.T, client kubernetes.Interface, ns string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pods, err := client.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Logf("listing diagnostic pods: %v", err)
	} else {
		for _, pod := range pods.Items {
			t.Logf("pod %s phase=%s reason=%s message=%s containers=%+v", pod.Name, pod.Status.Phase, pod.Status.Reason, pod.Status.Message, pod.Status.ContainerStatuses)
			tail := int64(30)
			logs, err := client.CoreV1().Pods(ns).GetLogs(pod.Name, &corev1.PodLogOptions{TailLines: &tail}).DoRaw(ctx)
			if err != nil {
				t.Logf("pod %s logs: %v", pod.Name, err)
			} else {
				t.Logf("pod %s logs: %s", pod.Name, logs)
			}
		}
	}
	clusterEvents, err := client.CoreV1().Events(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Logf("listing diagnostic events: %v", err)
		return
	}
	for _, event := range clusterEvents.Items {
		t.Logf("event %s %s: %s", event.InvolvedObject.Name, event.Reason, event.Message)
	}
}
