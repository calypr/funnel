package kubernetes

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ohsu-comp-bio/funnel/compute/kubernetes/storageplan"
	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/events"
	"github.com/ohsu-comp-bio/funnel/logger"
	plugin "github.com/ohsu-comp-bio/funnel/plugins/proto"
	"github.com/ohsu-comp-bio/funnel/storage"
	"github.com/ohsu-comp-bio/funnel/tes"
	"google.golang.org/protobuf/types/known/structpb"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	"sigs.k8s.io/yaml"
)

const boundClaimTemplate = `metadata:
  name: workspace
spec:
  storageClassName: research
  accessModes: [ReadWriteMany]
  resources:
    requests:
      storage: 1Gi
`

const boundPairClaimTemplate = `metadata:
  name: workspace
spec:
  volumeName: data-{{.TaskId}}
  storageClassName: research
  accessModes: [ReadWriteMany]
  resources:
    requests:
      storage: 1Gi
`

const boundVolumeTemplate = `apiVersion: v1
kind: PersistentVolume
metadata:
  name: data-{{.TaskId}}
spec:
  storageClassName: research
  capacity:
    storage: 1Gi
  accessModes: [ReadWriteMany]
  persistentVolumeReclaimPolicy: Retain
  csi:
    driver: example.csi.invalid
    volumeHandle: data-{{.TaskId}}
`

func nativeResource(t *testing.T, manifest string) *structpb.Struct {
	t.Helper()
	var resource map[string]interface{}
	if err := yaml.Unmarshal([]byte(manifest), &resource); err != nil {
		t.Fatalf("parsing native Kubernetes resource: %v", err)
	}
	value, err := structpb.NewStruct(resource)
	if err != nil {
		t.Fatalf("converting native Kubernetes resource: %v", err)
	}
	return value
}

func bindingConfig(t *testing.T, storageConfig *config.KubernetesStorage) *config.Config {
	t.Helper()
	conf := config.DefaultConfig()
	workerTemplate, err := os.ReadFile("../../config/kubernetes/worker-job.yaml")
	if err != nil {
		t.Fatal(err)
	}
	conf.Kubernetes.WorkerTemplate = string(workerTemplate)
	conf.Kubernetes.Namespace = "server"
	conf.Kubernetes.JobsNamespace = "tasks"
	conf.Kubernetes.ServiceAccountTemplate = ""
	conf.Kubernetes.RoleTemplate = ""
	conf.Kubernetes.RoleBindingTemplate = ""
	conf.Kubernetes.Storage = storageConfig
	conf.GenericS3 = nil
	return conf
}

func existingStorage(t *testing.T, claim string) *config.KubernetesStorage {
	t.Helper()
	return &config.KubernetesStorage{
		WorkspaceVolume: "workspace",
		Volumes: []*structpb.Struct{nativeResource(t, `name: workspace
persistentVolumeClaim:
  claimName: `+claim)},
	}
}

func createdStorage(t *testing.T, withPV bool) *config.KubernetesStorage {
	t.Helper()
	claimTemplate := boundClaimTemplate
	var pvTemplates []*structpb.Struct
	if withPV {
		claimTemplate = boundPairClaimTemplate
		pvTemplates = []*structpb.Struct{nativeResource(t, boundVolumeTemplate)}
	}
	return &config.KubernetesStorage{
		WorkspaceVolume:           "workspace",
		VolumeClaimTemplates:      []*structpb.Struct{nativeResource(t, claimTemplate)},
		PersistentVolumeTemplates: pvTemplates,
	}
}

func bindingClient() *fake.Clientset {
	client := fake.NewSimpleClientset()
	client.PrependReactor("create", "jobs", func(action k8stesting.Action) (bool, runtime.Object, error) {
		job := action.(k8stesting.CreateAction).GetObject().(*batchv1.Job)
		job.UID = types.UID("uid-" + job.Name)
		return false, nil, nil
	})
	return client
}

func retainFinalizedJobsOnDelete(client *fake.Clientset, namespace string, onDelete func(string)) {
	client.PrependReactor("delete", "jobs", func(action k8stesting.Action) (bool, runtime.Object, error) {
		name := action.(k8stesting.DeleteAction).GetName()
		if onDelete != nil {
			onDelete(name)
		}
		object, err := client.Tracker().Get(batchv1.SchemeGroupVersion.WithResource("jobs"), namespace, name)
		if err != nil {
			return false, nil, nil
		}
		job := object.(*batchv1.Job).DeepCopy()
		if len(job.Finalizers) == 0 {
			return false, nil, nil
		}
		if job.DeletionTimestamp == nil {
			job.DeletionTimestamp = &metav1.Time{Time: time.Unix(1, 0)}
			if err := client.Tracker().Update(batchv1.SchemeGroupVersion.WithResource("jobs"), job, namespace); err != nil {
				return true, nil, err
			}
		}
		return true, nil, nil
	})
}

func TestNativeStorageSubmitAndCleanup(t *testing.T) {
	for _, tc := range []struct {
		name    string
		storage *config.KubernetesStorage
		claim   string
		ownsPV  bool
	}{
		{"existing", existingStorage(t, "research-data"), "research-data", false},
		{"claim", createdStorage(t, false), "task-a-workspace", false},
		{"pair", createdStorage(t, true), "task-a-workspace", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			client := bindingClient()
			conf := bindingConfig(t, tc.storage)
			external := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "research-data", Namespace: "tasks", Labels: map[string]string{"owner": "operator"}}, Spec: corev1.PersistentVolumeClaimSpec{AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany}}}
			if _, err := client.CoreV1().PersistentVolumeClaims("tasks").Create(ctx, external, metav1.CreateOptions{}); err != nil {
				t.Fatal(err)
			}
			client.ClearActions()
			backend := &Backend{client: client, conf: conf, event: &noopEventWriter{}, log: logger.NewLogger("test", logger.DefaultConfig())}
			task := &tes.Task{Id: "task-a", Inputs: []*tes.Input{{Path: "/inputs/a", Url: "https://example.org/a"}}}
			if err := backend.Submit(ctx, task, conf); err != nil {
				t.Fatal(err)
			}
			job, err := client.BatchV1().Jobs("tasks").Get(ctx, task.Id, metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			plan, err := storageplan.Decode(job.Annotations[storageplan.Annotation])
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Volumes) != 1 || plan.Volumes[0].Name != "workspace" || plan.Volumes[0].PersistentVolumeClaim.ClaimName != tc.claim || plan.Namespace != "tasks" {
				t.Fatalf("wrong submission snapshot: %+v", plan)
			}
			if tc.name != "existing" {
				claim, err := client.CoreV1().PersistentVolumeClaims("tasks").Get(ctx, tc.claim, metav1.GetOptions{})
				if err != nil || claim.Name != "task-a-workspace" || claim.Namespace != "tasks" {
					t.Fatalf("template must create the task-scoped claim: claim=%v error=%v", claim, err)
				}
			}
			if tc.ownsPV {
				if len(plan.OwnedClaims) != 1 || len(plan.OwnedVolumes) != 1 || plan.OwnedVolumes[0] != "data-task-a" {
					t.Fatalf("native PVC/PV ownership was not recorded: %+v", plan)
				}
				claim, err := client.CoreV1().PersistentVolumeClaims("tasks").Get(ctx, tc.claim, metav1.GetOptions{})
				if err != nil || claim.Spec.VolumeName != "data-task-a" {
					t.Fatalf("PVC must explicitly bind to its rendered PV: claim=%v error=%v", claim, err)
				}
				pv, err := client.CoreV1().PersistentVolumes().Get(ctx, "data-task-a", metav1.GetOptions{})
				if err != nil || pv.Spec.ClaimRef == nil || pv.Spec.ClaimRef.Name != tc.claim || pv.Spec.ClaimRef.Namespace != "tasks" {
					t.Fatalf("PV must be cross-referenced to its rendered PVC: PV=%v error=%v", pv, err)
				}
			} else if len(plan.OwnedVolumes) != 0 {
				t.Fatalf("unexpected task-owned PVs: %v", plan.OwnedVolumes)
			}
			if tc.name != "existing" {
				claim, err := client.CoreV1().PersistentVolumeClaims("tasks").Get(ctx, tc.claim, metav1.GetOptions{})
				if err != nil || len(claim.OwnerReferences) != 1 || claim.OwnerReferences[0].UID != job.UID {
					t.Fatalf("claim must belong to submitted Job: claim=%v error=%v", claim, err)
				}
			}
			if err := backend.Cancel(ctx, task.Id); err != nil {
				t.Fatal(err)
			}
			// Retrying cleanup must remain safe after the Job snapshot is gone.
			if err := backend.Cancel(ctx, task.Id); err != nil {
				t.Fatal(err)
			}
			if tc.ownsPV {
				if _, err := client.CoreV1().PersistentVolumes().Get(ctx, "data-task-a", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
					t.Fatalf("owned PV remained after cancellation: %v", err)
				}
			} else {
				for _, action := range client.Actions() {
					if action.GetResource().Resource == "persistentvolumes" {
						t.Fatalf("namespaced binding accessed PV API: %v", action)
					}
				}
			}
			claim, err := client.CoreV1().PersistentVolumeClaims("tasks").Get(ctx, external.Name, metav1.GetOptions{})
			if err != nil || claim.Labels["owner"] != "operator" || len(claim.OwnerReferences) != 0 {
				t.Fatalf("external claim changed: %v %v", claim, err)
			}
		})
	}
}

func TestStoragePlanSurvivesConfigurationChange(t *testing.T) {
	ctx := context.Background()
	client := bindingClient()
	conf := bindingConfig(t, createdStorage(t, true))
	backend := &Backend{client: client, conf: conf, event: &noopEventWriter{}, log: logger.NewLogger("test", logger.DefaultConfig())}
	if err := backend.Submit(ctx, &tes.Task{Id: "snapshot"}, conf); err != nil {
		t.Fatal(err)
	}
	// A replacement backend has different configuration but reads the old Job's ownership.
	restarted := &Backend{client: client, conf: bindingConfig(t, existingStorage(t, "other-claim")), event: &noopEventWriter{}, log: backend.log}
	if err := restarted.Cancel(ctx, "snapshot"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CoreV1().PersistentVolumes().Get(ctx, "data-snapshot", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("cleanup did not use persisted ownership: %v", err)
	}
}

func TestStorageSubmissionFailureCleansOwnedPV(t *testing.T) {
	ctx := context.Background()
	client := bindingClient()
	client.PrependReactor("create", "persistentvolumeclaims", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("quota exceeded")
	})
	conf := bindingConfig(t, createdStorage(t, true))
	backend := &Backend{client: client, conf: conf, event: &noopEventWriter{}, log: logger.NewLogger("test", logger.DefaultConfig())}
	if err := backend.Submit(ctx, &tes.Task{Id: "failure"}, conf); err == nil || !strings.Contains(err.Error(), "quota exceeded") {
		t.Fatalf("expected claim creation error, got %v", err)
	}
	if _, err := client.CoreV1().PersistentVolumes().Get(ctx, "data-failure", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("failed submission left owned PV: %v", err)
	}
}

func TestStorageSubmissionPreservesCreationAndRollbackErrors(t *testing.T) {
	ctx := context.Background()
	client := bindingClient()
	creationErr := errors.New("quota exceeded")
	rollbackErr := errors.New("PV deletion unavailable")
	client.PrependReactor("create", "persistentvolumeclaims", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, creationErr
	})
	client.PrependReactor("delete", "persistentvolumes", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, rollbackErr
	})
	retainFinalizedJobsOnDelete(client, "tasks", nil)
	conf := bindingConfig(t, createdStorage(t, true))
	backend := &Backend{client: client, conf: conf, event: &noopEventWriter{}, log: logger.NewLogger("test", logger.DefaultConfig())}
	err := backend.Submit(ctx, &tes.Task{Id: "rollback-failure"}, conf)
	if !errors.Is(err, creationErr) || !errors.Is(err, rollbackErr) {
		t.Fatalf("submission must retain both creation and cleanup failures: %v", err)
	}
	job, err := client.BatchV1().Jobs("tasks").Get(ctx, "rollback-failure", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(job.Finalizers) == 0 || job.Annotations[storageplan.Annotation] == "" {
		t.Fatal("failed rollback lost its cleanup record")
	}
}

func TestStoragePluginOverridesAreTaskLocal(t *testing.T) {
	ctx := context.Background()
	client := bindingClient()
	conf := bindingConfig(t, existingStorage(t, "default-claim"))
	conf.Plugins = &config.Plugins{}
	backend := &Backend{client: client, conf: conf, event: &noopEventWriter{}, log: logger.NewLogger("test", logger.DefaultConfig())}
	for _, id := range []string{"alice", "bob"} {
		if _, err := client.CoreV1().PersistentVolumeClaims("tasks").Create(ctx, &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: id + "-claim", Namespace: "tasks"}, Spec: corev1.PersistentVolumeClaimSpec{AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany}}}, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
		override := &config.Config{Kubernetes: &config.Kubernetes{Storage: bindingConfig(t, existingStorage(t, id+"-claim")).Kubernetes.Storage}}
		taskCtx := context.WithValue(ctx, "pluginResponse", &plugin.JobResponse{Config: override})
		if err := backend.WriteEvent(taskCtx, events.NewTaskCreated(&tes.Task{Id: id})); err != nil {
			t.Fatal(err)
		}
		job, err := client.BatchV1().Jobs("tasks").Get(ctx, id, metav1.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		plan, err := storageplan.Decode(job.Annotations[storageplan.Annotation])
		if err != nil || plan.Volumes[0].PersistentVolumeClaim.ClaimName != id+"-claim" {
			t.Fatalf("wrong tenant binding: %v %v", plan, err)
		}
	}
	persistentVolumeClaim, ok := conf.Kubernetes.Storage.Volumes[0].AsMap()["persistentVolumeClaim"].(map[string]interface{})
	if !ok || persistentVolumeClaim["claimName"] != "default-claim" {
		t.Fatal("plugin mutated server defaults")
	}
}

func TestInvalidStorageFailsBeforeCreatingResources(t *testing.T) {
	client := bindingClient()
	conf := bindingConfig(t, existingStorage(t, "shared"))
	conf.Kubernetes.Storage.WorkspaceVolume = "missing"
	backend := &Backend{client: client, conf: conf, event: &noopEventWriter{}, log: logger.NewLogger("test", logger.DefaultConfig())}
	if err := backend.Submit(context.Background(), &tes.Task{Id: "invalid"}, conf); err == nil {
		t.Fatal("invalid storage succeeded")
	}
	if len(client.Actions()) != 0 {
		t.Fatalf("invalid binding reached Kubernetes: %v", client.Actions())
	}
}

func TestS3MountedReadVolumeReferencesResolveBeforeResourceCreation(t *testing.T) {
	for _, volumeName := range []string{"missing", "workspace"} {
		t.Run(volumeName, func(t *testing.T) {
			ctx := context.Background()
			client := bindingClient()
			conf := bindingConfig(t, existingStorage(t, "shared"))
			conf.Kubernetes.Storage.VolumeMounts = []*structpb.Struct{nativeResource(t, `name: workspace
mountPath: /reference
readOnly: true`)}
			conf.GenericS3 = []*config.GenericS3Storage{{
				Endpoint: "https://s3.example.test",
				Key:      "access-key",
				Secret:   "access-secret",
				Bucket:   "reference",
				MountedRead: &config.S3MountedRead{
					Volume: volumeName,
				},
			}}
			if _, err := client.CoreV1().PersistentVolumeClaims("tasks").Create(ctx, &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "shared", Namespace: "tasks"}, Spec: corev1.PersistentVolumeClaimSpec{AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany}}}, metav1.CreateOptions{}); err != nil {
				t.Fatal(err)
			}
			client.ClearActions()
			backend := &Backend{client: client, conf: conf, event: &noopEventWriter{}, log: logger.NewLogger("test", logger.DefaultConfig())}
			err := backend.Submit(ctx, &tes.Task{Id: "input-binding"}, conf)
			if volumeName == "missing" {
				if err == nil || !strings.Contains(err.Error(), "unknown") {
					t.Fatalf("missing reference was not rejected: %v", err)
				}
				if len(client.Actions()) != 0 {
					t.Fatalf("invalid input reference reached Kubernetes: %v", client.Actions())
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			job, err := client.BatchV1().Jobs("tasks").Get(ctx, "input-binding", metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			plan, err := storageplan.Decode(job.Annotations[storageplan.Annotation])
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.S3ReadMounts) != 1 || plan.S3ReadMounts[0] != (storage.S3ReadMount{Endpoint: "https://s3.example.test", Bucket: "reference", Prefix: "", Root: "/reference"}) {
				t.Fatalf("submitted Job lost the volume-based S3 read mount: %+v", plan.S3ReadMounts)
			}
		})
	}
}

func TestStorageCleanupRetriesAfterPVDeleteFailure(t *testing.T) {
	for _, tc := range []struct {
		name   string
		active int32
	}{
		{name: "pending"},
		{name: "active", active: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			client := bindingClient()
			conf := bindingConfig(t, createdStorage(t, true))
			taskID := "retry-" + tc.name
			database := &mockReadOnlyServer{tasks: []*tes.Task{{Id: taskID, State: tes.State_QUEUED}}}
			backend := &Backend{client: client, conf: conf, database: database, event: &noopEventWriter{}, log: logger.NewLogger("test", logger.DefaultConfig())}
			if err := backend.Submit(ctx, &tes.Task{Id: taskID}, conf); err != nil {
				t.Fatal(err)
			}
			job, err := client.BatchV1().Jobs("tasks").Get(ctx, taskID, metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			database.mu.Lock()
			database.tasks[0].State = tes.State_CANCELED
			database.mu.Unlock()
			job.Status.Active = tc.active
			if _, err := client.BatchV1().Jobs("tasks").UpdateStatus(ctx, job, metav1.UpdateOptions{}); err != nil {
				t.Fatal(err)
			}
			executorName := taskID + "-0"
			if _, err := client.BatchV1().Jobs("tasks").Create(ctx, &batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{Name: executorName, Namespace: "tasks", Labels: map[string]string{"app": "funnel-executor"}},
			}, metav1.CreateOptions{}); err != nil {
				t.Fatal(err)
			}

			pvFailure := errors.New("temporarily unavailable")
			var workerDeleteRequests, executorDeleteRequests, pvDeleteRequests int
			retainFinalizedJobsOnDelete(client, "tasks", func(name string) {
				switch name {
				case taskID:
					workerDeleteRequests++
				case executorName:
					executorDeleteRequests++
				}
			})
			client.PrependReactor("update", "jobs", func(action k8stesting.Action) (bool, runtime.Object, error) {
				updated := action.(k8stesting.UpdateAction).GetObject().(*batchv1.Job)
				if updated.Name != taskID || updated.DeletionTimestamp == nil {
					return false, nil, nil
				}
				for _, finalizer := range updated.Finalizers {
					if finalizer == storageplan.Finalizer {
						return false, nil, nil
					}
				}
				if err := client.Tracker().Delete(batchv1.SchemeGroupVersion.WithResource("jobs"), "tasks", taskID); err != nil {
					return true, nil, err
				}
				return true, nil, nil
			})
			client.PrependReactor("delete", "persistentvolumes", func(k8stesting.Action) (bool, runtime.Object, error) {
				pvDeleteRequests++
				if pvDeleteRequests == 1 {
					return true, nil, pvFailure
				}
				return false, nil, nil
			})
			client.ClearActions()

			if err := backend.Cancel(ctx, taskID); !errors.Is(err, pvFailure) {
				t.Fatalf("Cancel error = %v, want transient PV deletion failure", err)
			}
			if workerDeleteRequests != 1 || executorDeleteRequests != 1 {
				t.Fatalf("first Cancel requested worker deletes %d times and executor deletes %d times", workerDeleteRequests, executorDeleteRequests)
			}
			job, err = client.BatchV1().Jobs("tasks").Get(ctx, taskID, metav1.GetOptions{})
			if err != nil || job.DeletionTimestamp == nil || job.Annotations[storageplan.Annotation] == "" {
				t.Fatalf("failed cleanup must retain the deleting Job snapshot: job=%v error=%v", job, err)
			}
			if !slices.Contains(job.Finalizers, storageplan.Finalizer) {
				t.Fatalf("failed cleanup released its storage finalizer: %v", job.Finalizers)
			}
			if _, err := client.CoreV1().PersistentVolumes().Get(ctx, "data-"+taskID, metav1.GetOptions{}); err != nil {
				t.Fatalf("failed PV delete unexpectedly removed the owned PV: %v", err)
			}
			backend.conf = bindingConfig(t, existingStorage(t, "different-default"))

			backend.reconcileOnce(ctx, false)

			if _, err := client.BatchV1().Jobs("tasks").Get(ctx, taskID, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
				t.Fatalf("ordinary reconciliation did not finish worker cleanup: %v", err)
			}
			if _, err := client.CoreV1().PersistentVolumes().Get(ctx, "data-"+taskID, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
				t.Fatalf("ordinary reconciliation did not retry owned PV deletion: %v", err)
			}
			if _, err := client.BatchV1().Jobs("tasks").Get(ctx, executorName, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
				t.Fatalf("first Cancel did not remove executor Job: %v", err)
			}
			if pvDeleteRequests != 2 {
				t.Fatalf("got %d PV delete requests, want initial failure and reconcile retry", pvDeleteRequests)
			}
		})
	}
}

func TestStorageCleanupRetainsFinalizerAfterJobDeleteFailure(t *testing.T) {
	ctx := context.Background()
	client := bindingClient()
	conf := bindingConfig(t, createdStorage(t, true))
	backend := &Backend{client: client, conf: conf, event: &noopEventWriter{}, log: logger.NewLogger("test", logger.DefaultConfig())}
	if err := backend.Submit(ctx, &tes.Task{Id: "retry-job"}, conf); err != nil {
		t.Fatal(err)
	}
	fail := true
	client.PrependReactor("delete", "jobs", func(k8stesting.Action) (bool, runtime.Object, error) {
		if fail {
			fail = false
			return true, nil, errors.New("temporarily unavailable")
		}
		return false, nil, nil
	})
	if err := backend.Cancel(ctx, "retry-job"); err == nil {
		t.Fatal("expected transient Job deletion failure")
	}
	job, err := client.BatchV1().Jobs("tasks").Get(ctx, "retry-job", metav1.GetOptions{})
	if err != nil || len(job.Finalizers) == 0 {
		t.Fatalf("failed Job deletion lost its cleanup finalizer: %v %v", job, err)
	}
	backend.conf = bindingConfig(t, existingStorage(t, "different-default"))
	backend.database = &mockDatabase{tasks: map[string]*tes.Task{"retry-job": {Id: "retry-job", State: tes.Complete}}}
	backend.CleanOrphanedResources(ctx)
	if _, err := client.BatchV1().Jobs("tasks").Get(ctx, "retry-job", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("orphan cleanup did not retry Job deletion: %v", err)
	}
}

func TestStorageOrphanCleanupUsesFinalizedJobAfterConfigChange(t *testing.T) {
	ctx := context.Background()
	client := bindingClient()
	conf := bindingConfig(t, createdStorage(t, true))
	backend := &Backend{client: client, conf: conf, event: &noopEventWriter{}, log: logger.NewLogger("test", logger.DefaultConfig())}
	if err := backend.Submit(ctx, &tes.Task{Id: "orphan"}, conf); err != nil {
		t.Fatal(err)
	}
	backend.conf = bindingConfig(t, existingStorage(t, "different-default"))
	backend.database = &mockDatabase{tasks: map[string]*tes.Task{"orphan": {Id: "orphan", State: tes.Complete}}}
	client.ClearActions()
	backend.CleanOrphanedResources(ctx)
	for _, action := range client.Actions() {
		if action.GetResource().Resource == "persistentvolumes" && action.GetVerb() == "list" {
			t.Fatal("namespaced configuration must discover ownership through Jobs")
		}
	}
	if _, err := client.CoreV1().PersistentVolumes().Get(ctx, "data-orphan", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("orphan cleanup lost submitted PV names: %v", err)
	}
}

func TestCleanOrphanedResourcesFindsNonFinalizedWorkerJobs(t *testing.T) {
	for _, tc := range []struct {
		name      string
		task      *tes.Task
		wantClean bool
	}{
		{name: "orphan", wantClean: true},
		{name: "active", task: &tes.Task{Id: "dynamic-claim-task", State: tes.State_RUNNING}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			const taskID = "dynamic-claim-task"
			client := bindingClient()
			conf := bindingConfig(t, createdStorage(t, false))
			backend := &Backend{client: client, conf: conf, event: &noopEventWriter{}, log: logger.NewLogger("test", logger.DefaultConfig())}
			if err := backend.Submit(ctx, &tes.Task{Id: taskID}, conf); err != nil {
				t.Fatal(err)
			}
			job, err := client.BatchV1().Jobs("tasks").Get(ctx, taskID, metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if len(job.Finalizers) != 0 {
				t.Fatalf("dynamic-claim Job unexpectedly has finalizers: %v", job.Finalizers)
			}
			plan, err := storageplan.Decode(job.Annotations[storageplan.Annotation])
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.OwnedVolumes) != 0 {
				t.Fatalf("dynamic-claim Job unexpectedly owns PVs: %v", plan.OwnedVolumes)
			}
			claimName := taskID + "-workspace"
			claim, err := client.CoreV1().PersistentVolumeClaims("tasks").Get(ctx, claimName, metav1.GetOptions{})
			if err != nil || len(claim.OwnerReferences) != 1 || claim.OwnerReferences[0].UID != job.UID {
				t.Fatalf("PVC must be owned by its worker Job: claim=%v error=%v", claim, err)
			}

			tasks := map[string]*tes.Task{}
			if tc.task != nil {
				tasks[taskID] = tc.task
			}
			backend.database = &mockDatabase{tasks: tasks}
			// The fake client does not garbage-collect dependents from Job owner references.
			client.PrependReactor("delete", "jobs", func(k8stesting.Action) (bool, runtime.Object, error) {
				object, err := client.Tracker().List(
					corev1.SchemeGroupVersion.WithResource("persistentvolumeclaims"),
					corev1.SchemeGroupVersion.WithKind("PersistentVolumeClaim"),
					"tasks",
				)
				if err != nil {
					return true, nil, err
				}
				for _, claim := range object.(*corev1.PersistentVolumeClaimList).Items {
					for _, owner := range claim.OwnerReferences {
						if owner.UID == job.UID {
							if err := client.Tracker().Delete(corev1.SchemeGroupVersion.WithResource("persistentvolumeclaims"), "tasks", claim.Name); err != nil {
								return true, nil, err
							}
						}
					}
				}
				return false, nil, nil
			})

			backend.CleanOrphanedResources(ctx)

			_, jobErr := client.BatchV1().Jobs("tasks").Get(ctx, taskID, metav1.GetOptions{})
			_, claimErr := client.CoreV1().PersistentVolumeClaims("tasks").Get(ctx, claimName, metav1.GetOptions{})
			if tc.wantClean {
				if !apierrors.IsNotFound(jobErr) || !apierrors.IsNotFound(claimErr) {
					t.Fatalf("orphan Job and its owned PVC must be cleaned: job error=%v, PVC error=%v", jobErr, claimErr)
				}
				return
			}
			if jobErr != nil || claimErr != nil {
				t.Fatalf("active task resources must remain: job error=%v, PVC error=%v", jobErr, claimErr)
			}
		})
	}
}

func TestStorageNamespaceOverrideRejectedBeforeCreation(t *testing.T) {
	client := bindingClient()
	conf := bindingConfig(t, existingStorage(t, "shared"))
	backend := &Backend{client: client, conf: conf, event: &noopEventWriter{}, log: logger.NewLogger("test", logger.DefaultConfig())}
	override := bindingConfig(t, existingStorage(t, "shared"))
	override.Kubernetes.JobsNamespace = "another-namespace"
	if err := backend.Submit(context.Background(), &tes.Task{Id: "namespace"}, override); err == nil || !strings.Contains(err.Error(), "JobsNamespace") {
		t.Fatalf("expected namespace override error, got %v", err)
	}
	if len(client.Actions()) != 0 {
		t.Fatalf("namespace override reached Kubernetes: %v", client.Actions())
	}
}

func TestStorageSinglePodWorkspaceRejected(t *testing.T) {
	client := bindingClient()
	storageConfig := createdStorage(t, false)
	storageConfig.VolumeClaimTemplates[0] = nativeResource(t, strings.ReplaceAll(boundClaimTemplate, "ReadWriteMany", "ReadWriteOncePod"))
	conf := bindingConfig(t, storageConfig)
	backend := &Backend{client: client, conf: conf, event: &noopEventWriter{}, log: logger.NewLogger("test", logger.DefaultConfig())}
	if err := backend.Submit(context.Background(), &tes.Task{Id: "single-pod"}, conf); err == nil {
		t.Fatal("single-pod workspace cannot serve worker and executor")
	}
	if len(client.Actions()) != 0 {
		t.Fatalf("unsupported workspace reached Kubernetes: %v", client.Actions())
	}
}
