package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/ohsu-comp-bio/funnel/compute/kubernetes/storageplan"
	"github.com/ohsu-comp-bio/funnel/events"
	"github.com/ohsu-comp-bio/funnel/logger"
	"github.com/ohsu-comp-bio/funnel/storage"
	"github.com/ohsu-comp-bio/funnel/tes"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestLifecycleCancelRacingPVCreate(t *testing.T) {
	ctx := context.Background()
	client := bindingClient()
	conf := bindingConfig(t, createdStorage(t, true))
	backend := &Backend{client: client, conf: conf, event: &noopEventWriter{}, log: logger.NewLogger("lifecycle", logger.DefaultConfig())}
	var raced bool
	client.PrependReactor("create", "persistentvolumes", func(action k8stesting.Action) (bool, runtime.Object, error) {
		pv := action.(k8stesting.CreateAction).GetObject().(*corev1.PersistentVolume)
		pv.UID = types.UID("pv-" + pv.Name)
		if !raced {
			raced = true
			gvr := batchv1.SchemeGroupVersion.WithResource("jobs")
			object, err := client.Tracker().Get(gvr, "tasks", "cancel-race")
			if err != nil {
				return true, nil, err
			}
			job := object.(*batchv1.Job).DeepCopy()
			deletedAt := metav1.NewTime(time.Now())
			job.DeletionTimestamp = &deletedAt
			if err := client.Tracker().Update(gvr, job, "tasks"); err != nil {
				return true, nil, err
			}
		}
		return false, nil, nil
	})
	client.PrependReactor("update", "jobs", func(action k8stesting.Action) (bool, runtime.Object, error) {
		job := action.(k8stesting.UpdateAction).GetObject().(*batchv1.Job)
		if job.Name == "cancel-race" && job.DeletionTimestamp != nil && !slices.Contains(job.Finalizers, storageplan.Finalizer) {
			if err := client.Tracker().Delete(batchv1.SchemeGroupVersion.WithResource("jobs"), "tasks", job.Name); err != nil {
				return true, nil, err
			}
			return true, nil, nil
		}
		return false, nil, nil
	})
	retainFinalizedJobsOnDelete(client, "tasks", nil)

	if err := backend.Submit(ctx, &tes.Task{Id: "cancel-race"}, conf); err == nil {
		t.Fatal("Submit should notice that the owning Job was deleted during PV creation")
	}
	if _, err := client.CoreV1().PersistentVolumes().Get(ctx, "data-cancel-race", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("PV created after cancellation was not rolled back: %v", err)
	}
	for _, action := range client.Actions() {
		if action.GetVerb() == "delete" && action.GetResource().Resource == "persistentvolumes" {
			options := action.(k8stesting.DeleteAction).GetDeleteOptions()
			if options.Preconditions == nil || options.Preconditions.UID == nil || *options.Preconditions.UID != types.UID("pv-data-cancel-race") {
				t.Fatalf("rollback delete lacked the submitted Job UID precondition: %+v", options)
			}
			return
		}
	}
	t.Fatal("rollback did not issue a PV delete")
}

func TestLifecycleMissingSnapshotFailsClosed(t *testing.T) {
	ctx := context.Background()
	conf := bindingConfig(t, createdStorage(t, true))
	conf.Kubernetes.Storage = nil
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "missing-snapshot", Namespace: "tasks", UID: "job-uid", Labels: map[string]string{"app": "funnel-worker"}, Finalizers: []string{storageplan.Finalizer}}}
	pv := &corev1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{
		Name: "data-missing-snapshot", UID: "pv-uid",
		Labels:      map[string]string{"app": "funnel", "taskId": job.Name, "namespace": "tasks", storageOwnedLabel: "true"},
		Annotations: map[string]string{storageOwnerUIDAnnotation: "job-uid"},
	}}
	executor := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: job.Name + "-0", Namespace: "tasks"}}
	client := fake.NewSimpleClientset(job, pv, executor)
	retainFinalizedJobsOnDelete(client, "tasks", nil)
	backend := &Backend{client: client, conf: conf, event: &noopEventWriter{}, log: logger.NewLogger("lifecycle", logger.DefaultConfig())}
	if err := backend.Cancel(ctx, job.Name); err == nil {
		t.Fatal("cleanup without an annotation or worker snapshot should fail closed")
	}
	gotJob, err := client.BatchV1().Jobs("tasks").Get(ctx, job.Name, metav1.GetOptions{})
	if err != nil || gotJob.DeletionTimestamp == nil || !slices.Contains(gotJob.Finalizers, storageplan.Finalizer) {
		t.Fatalf("cleanup did not stop the worker while retaining its finalizer: job=%+v err=%v", gotJob, err)
	}
	if _, err := client.CoreV1().PersistentVolumes().Get(ctx, pv.Name, metav1.GetOptions{}); err != nil {
		t.Fatalf("missing snapshot caused PV deletion without ownership data: %v", err)
	}
	if _, err := client.BatchV1().Jobs("tasks").Get(ctx, executor.Name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("cleanup did not request executor Job deletion: %v", err)
	}
	for _, action := range client.Actions() {
		if action.GetVerb() == "delete" && action.GetResource().Resource == "persistentvolumes" {
			t.Fatal("cleanup attempted PV deletion without the snapshot")
		}
	}
}

func TestLifecycleMissingSnapshotRejectedDeleteStillRemovesExecutor(t *testing.T) {
	ctx := context.Background()
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "missing-snapshot-rejected", Namespace: "tasks", UID: "job-uid", Finalizers: []string{storageplan.Finalizer}}}
	executor := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: job.Name + "-0", Namespace: "tasks"}}
	client := fake.NewSimpleClientset(job, executor)
	deleteErr := errors.New("worker Job delete rejected")
	client.PrependReactor("delete", "jobs", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if action.(k8stesting.DeleteAction).GetName() == job.Name {
			return true, nil, deleteErr
		}
		return false, nil, nil
	})
	backend := &Backend{client: client, conf: bindingConfig(t, nil), event: &noopEventWriter{}, log: logger.NewLogger("lifecycle", logger.DefaultConfig())}
	if err := backend.Cancel(ctx, job.Name); err == nil {
		t.Fatal("cleanup without a trustworthy snapshot should report its recovery failure")
	}
	gotJob, err := client.BatchV1().Jobs("tasks").Get(ctx, job.Name, metav1.GetOptions{})
	if err != nil || gotJob.DeletionTimestamp != nil || !slices.Contains(gotJob.Finalizers, storageplan.Finalizer) {
		t.Fatalf("rejected delete changed the finalized Job: job=%+v err=%v", gotJob, err)
	}
	if _, err := client.BatchV1().Jobs("tasks").Get(ctx, executor.Name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("executor delete was not attempted after worker delete rejection: %v", err)
	}
}

func TestLifecycleCancelDeliveredBeforeCreateDoesNotSubmit(t *testing.T) {
	ctx := context.Background()
	client := bindingClient()
	conf := bindingConfig(t, createdStorage(t, true))
	taskID := "cancel-before-submit"
	backend := &Backend{
		client:   client,
		conf:     conf,
		event:    &noopEventWriter{},
		log:      logger.NewLogger("lifecycle", logger.DefaultConfig()),
		database: &mockDatabase{tasks: map[string]*tes.Task{taskID: {Id: taskID, State: tes.State_CANCELED}}},
	}
	if err := backend.WriteEvent(ctx, events.NewState(taskID, tes.Canceled)); err != nil {
		t.Fatalf("cancel before Job exists: %v", err)
	}
	if err := backend.WriteEvent(ctx, events.NewTaskCreated(&tes.Task{Id: taskID})); err != nil {
		t.Fatalf("late submit: %v", err)
	}
	backend.reconcileOnce(ctx, false)
	if _, err := client.BatchV1().Jobs("tasks").Get(ctx, taskID, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("canceled task created a Job: %v", err)
	}
	if _, err := client.CoreV1().PersistentVolumes().Get(ctx, "data-"+taskID, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("canceled task created a PV: %v", err)
	}
	if _, err := client.CoreV1().PersistentVolumeClaims("tasks").Get(ctx, taskID+"-workspace", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("canceled task created a PVC: %v", err)
	}
}

func TestLifecycleTaskLookupFailurePreventsSubmission(t *testing.T) {
	ctx := context.Background()
	client := bindingClient()
	conf := bindingConfig(t, createdStorage(t, true))
	backend := &Backend{
		client:   client,
		conf:     conf,
		event:    &noopEventWriter{},
		log:      logger.NewLogger("lifecycle", logger.DefaultConfig()),
		database: lifecycleErrorDatabase{err: errors.New("permission denied")},
	}
	taskID := "lookup-failure"
	if err := backend.Submit(ctx, &tes.Task{Id: taskID}, conf); err == nil {
		t.Fatal("Submit should fail closed when the task state cannot be read")
	}
	if _, err := client.BatchV1().Jobs("tasks").Get(ctx, taskID, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("task state lookup failure still created a Job: %v", err)
	}
}

func TestLifecycleCancelDuringSubmitCleansResourcesWithoutSystemError(t *testing.T) {
	ctx := context.Background()
	client := bindingClient()
	conf := bindingConfig(t, createdStorage(t, true))
	taskID := "cancel-during-submit"
	task := &tes.Task{Id: taskID, State: tes.State_QUEUED}
	database := &mockDatabase{tasks: map[string]*tes.Task{taskID: task}}
	eventsWritten := &capturingEventWriter{}
	deleteErr := errors.New("cancel delete rejected")
	deleteAttempts := 0
	client.PrependReactor("delete", "jobs", func(k8stesting.Action) (bool, runtime.Object, error) {
		deleteAttempts++
		if deleteAttempts == 1 {
			return true, nil, deleteErr
		}
		return false, nil, nil
	})
	client.PrependReactor("create", "persistentvolumes", func(k8stesting.Action) (bool, runtime.Object, error) {
		task.State = tes.State_CANCELED
		return false, nil, nil
	})
	pvcCreations := 0
	client.PrependReactor("create", "persistentvolumeclaims", func(k8stesting.Action) (bool, runtime.Object, error) {
		pvcCreations++
		return false, nil, nil
	})
	backend := &Backend{client: client, conf: conf, event: eventsWritten, database: database, log: logger.NewLogger("lifecycle", logger.DefaultConfig())}

	if err := backend.Submit(ctx, task, conf); err != nil {
		t.Fatalf("Submit returned an error after cancellation: %v", err)
	}
	if deleteAttempts != 2 {
		t.Fatalf("got %d Job delete attempts, want failed rollback and post-cancel retry", deleteAttempts)
	}
	if pvcCreations != 0 {
		t.Fatalf("submission created %d PVCs after cancellation", pvcCreations)
	}
	if _, err := client.BatchV1().Jobs("tasks").Get(ctx, taskID, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatal("post-create cancellation left the worker Job behind")
	}
	if _, err := client.CoreV1().PersistentVolumes().Get(ctx, "data-"+taskID, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("post-create cancellation left its PV behind: %v", err)
	}
	if _, err := client.CoreV1().PersistentVolumeClaims("tasks").Get(ctx, taskID+"-workspace", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("post-create cancellation left its PVC behind: %v", err)
	}
	if eventsWritten.hasSystemError(taskID) {
		t.Fatal("post-create cancellation wrote SYSTEM_ERROR")
	}
}

func TestLifecycleCancelAfterResourceCreationCleansJob(t *testing.T) {
	ctx := context.Background()
	client := bindingClient()
	conf := bindingConfig(t, existingStorage(t, "external-workspace"))
	conf.Kubernetes.ConfigMapTemplate = configMapTemplate
	claim := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "external-workspace", Namespace: "tasks"}, Spec: corev1.PersistentVolumeClaimSpec{AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany}}}
	if _, err := client.CoreV1().PersistentVolumeClaims("tasks").Create(ctx, claim, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	taskID := "cancel-after-create"
	task := &tes.Task{Id: taskID, State: tes.State_QUEUED}
	eventsWritten := &capturingEventWriter{}
	client.PrependReactor("create", "configmaps", func(k8stesting.Action) (bool, runtime.Object, error) {
		task.State = tes.State_CANCELED
		return false, nil, nil
	})
	backend := &Backend{client: client, conf: conf, event: eventsWritten, database: &mockDatabase{tasks: map[string]*tes.Task{taskID: task}}, log: logger.NewLogger("lifecycle", logger.DefaultConfig())}
	if err := backend.Submit(ctx, task, conf); err != nil {
		t.Fatalf("Submit returned an error after cancellation: %v", err)
	}
	if _, err := client.BatchV1().Jobs("tasks").Get(ctx, taskID, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("post-creation cancellation left its worker Job: %v", err)
	}
	if eventsWritten.hasSystemError(taskID) {
		t.Fatal("post-creation cancellation wrote SYSTEM_ERROR")
	}
}

func TestLifecyclePostSubmitLookupFailureDefersToReconcile(t *testing.T) {
	ctx := context.Background()
	client := bindingClient()
	conf := bindingConfig(t, existingStorage(t, "external-workspace"))
	claim := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "external-workspace", Namespace: "tasks"}, Spec: corev1.PersistentVolumeClaimSpec{AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany}}}
	if _, err := client.CoreV1().PersistentVolumeClaims("tasks").Create(ctx, claim, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	task := &tes.Task{Id: "post-submit-read-failure", State: tes.State_QUEUED}
	database := &lifecycleSequencedDatabase{task: task, failAt: 2}
	eventsWritten := &capturingEventWriter{}
	backend := &Backend{client: client, conf: conf, event: eventsWritten, database: database, log: logger.NewLogger("lifecycle", logger.DefaultConfig())}
	if err := backend.Submit(ctx, task, conf); err != nil {
		t.Fatalf("Submit should defer reconciliation after a post-create lookup failure: %v", err)
	}
	if database.gets != 2 {
		t.Fatalf("Submit made %d task lookups, want pre-create and post-create checks", database.gets)
	}
	if _, err := client.BatchV1().Jobs("tasks").Get(ctx, task.Id, metav1.GetOptions{}); err != nil {
		t.Fatalf("post-create database failure deleted the submitted Job: %v", err)
	}
	if eventsWritten.hasSystemError(task.Id) {
		t.Fatal("post-create database failure wrote SYSTEM_ERROR")
	}

	task.State = tes.State_CANCELED
	backend.reconcileOnce(ctx, true)
	if _, err := client.BatchV1().Jobs("tasks").Get(ctx, task.Id, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("reconciliation did not clean the canceled Job after lookup recovered: %v", err)
	}
}

func TestLifecycleStorageSnapshotRecoversFromWorkerEnv(t *testing.T) {
	ctx := context.Background()
	client := bindingClient()
	conf := bindingConfig(t, createdStorage(t, true))
	backend := &Backend{client: client, conf: conf, event: &noopEventWriter{}, log: logger.NewLogger("lifecycle", logger.DefaultConfig())}
	taskID := "recover-snapshot"
	if err := backend.Submit(ctx, &tes.Task{Id: taskID}, conf); err != nil {
		t.Fatal(err)
	}
	job, err := client.BatchV1().Jobs("tasks").Get(ctx, taskID, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	delete(job.Annotations, storageplan.Annotation)
	if _, err := client.BatchV1().Jobs("tasks").Update(ctx, job, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := backend.Cancel(ctx, taskID); err != nil {
		t.Fatalf("cleanup did not recover the worker snapshot: %v", err)
	}
	if _, err := client.CoreV1().PersistentVolumes().Get(ctx, "data-"+taskID, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("recovered snapshot did not delete its owned PV: %v", err)
	}
}

func TestLifecycleCancelRecoversOwnershipFromLegacyOverlappingRoutes(t *testing.T) {
	ctx := context.Background()
	taskID := "legacy-overlap"
	jobUID := types.UID("legacy-job-uid")
	plan := &storageplan.Plan{
		Version:         4,
		TaskID:          taskID,
		Namespace:       "tasks",
		WorkDir:         "/work",
		WorkspaceVolume: "workspace",
		WorkerContainer: "main",
		Volumes: []corev1.Volume{
			{Name: "workspace", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: taskID + "-workspace"}}},
			{Name: "parent", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: taskID + "-workspace"}}},
			{Name: "child", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: taskID + "-workspace"}}},
		},
		WorkerMounts: []corev1.VolumeMount{
			{Name: "parent", MountPath: "/outputs/parent", SubPath: "."},
			{Name: "child", MountPath: "/outputs/child", SubPath: "nested"},
		},
		OwnedClaims:  []string{taskID + "-workspace"},
		OwnedVolumes: []string{"data-" + taskID},
		S3WriteMounts: []storage.S3WriteMount{
			{Endpoint: "https://s3.example.test", Bucket: "one", Root: "/outputs/parent", Timeout: time.Second},
			{Endpoint: "https://s3.example.test", Bucket: "two", Root: "/outputs/child", Timeout: time.Second},
		},
	}
	snapshotBytes, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := string(snapshotBytes)
	if _, err := storageplan.Decode(snapshot); err == nil {
		t.Fatal("runnable storage plan accepted overlapping legacy write routes")
	}

	for _, tc := range []struct {
		name               string
		annotationSnapshot string
	}{
		{name: "matching annotation and worker copies", annotationSnapshot: snapshot},
		{name: "worker environment fallback"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			job := &batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{
					Name: taskID, Namespace: "tasks", UID: jobUID,
					Labels:      map[string]string{"app": "funnel-worker"},
					Annotations: map[string]string{},
					Finalizers:  []string{storageplan.Finalizer},
				},
				Spec: batchv1.JobSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{
					Name: "main", Env: []corev1.EnvVar{{Name: storageplan.Env, Value: snapshot}},
				}}}}},
			}
			if tc.annotationSnapshot != "" {
				job.Annotations[storageplan.Annotation] = tc.annotationSnapshot
			}
			ownedPV := &corev1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{
				Name: "data-" + taskID, UID: "owned-pv-uid",
				Labels: map[string]string{
					"app": "funnel", "taskId": taskID, "namespace": "tasks", storageOwnedLabel: "true",
				},
				Annotations: map[string]string{storageOwnerUIDAnnotation: string(jobUID)},
			}}
			operatorPV := &corev1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{
				Name: "operator-" + taskID, UID: "operator-pv-uid",
				Labels: map[string]string{"app": "funnel", "taskId": taskID, "namespace": "tasks"},
			}}
			client := fake.NewSimpleClientset(job, ownedPV, operatorPV)
			retainFinalizedJobsOnDelete(client, "tasks", nil)
			backend := &Backend{client: client, conf: bindingConfig(t, createdStorage(t, true)), event: &noopEventWriter{}, log: logger.NewLogger("lifecycle", logger.DefaultConfig())}

			if err := backend.Cancel(ctx, taskID); err != nil {
				t.Fatalf("Cancel did not recover ownership from the old plan: %v", err)
			}
			if _, err := client.CoreV1().PersistentVolumes().Get(ctx, ownedPV.Name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
				t.Fatalf("cleanup did not delete the recorded owned PV: %v", err)
			}
			if _, err := client.CoreV1().PersistentVolumes().Get(ctx, operatorPV.Name, metav1.GetOptions{}); err != nil {
				t.Fatalf("cleanup deleted or changed the operator PV: %v", err)
			}
			gotJob, err := client.BatchV1().Jobs("tasks").Get(ctx, taskID, metav1.GetOptions{})
			if err != nil || slices.Contains(gotJob.Finalizers, storageplan.Finalizer) {
				t.Fatalf("cleanup did not release the storage finalizer: job=%+v err=%v", gotJob, err)
			}
			var deletedPVs []string
			for _, action := range client.Actions() {
				if action.GetVerb() == "delete" && action.GetResource().Resource == "persistentvolumes" {
					deletedPVs = append(deletedPVs, action.(k8stesting.DeleteAction).GetName())
				}
			}
			if len(deletedPVs) != 1 || deletedPVs[0] != ownedPV.Name {
				t.Fatalf("cleanup requested PV deletes %v, want only %q", deletedPVs, ownedPV.Name)
			}
		})
	}
}

func TestLifecycleConflictingWorkerSnapshotsFailClosed(t *testing.T) {
	ctx := context.Background()
	client := bindingClient()
	conf := bindingConfig(t, createdStorage(t, true))
	backend := &Backend{client: client, conf: conf, event: &noopEventWriter{}, log: logger.NewLogger("lifecycle", logger.DefaultConfig())}
	taskID := "conflicting-snapshot"
	if err := backend.Submit(ctx, &tes.Task{Id: taskID}, conf); err != nil {
		t.Fatal(err)
	}
	job, err := client.BatchV1().Jobs("tasks").Get(ctx, taskID, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for containerIndex := range job.Spec.Template.Spec.Containers {
		container := &job.Spec.Template.Spec.Containers[containerIndex]
		for envIndex := range container.Env {
			if container.Env[envIndex].Name == storageplan.Env {
				container.Env[envIndex].Value += " "
				found = true
			}
		}
	}
	if !found {
		t.Fatal("submitted worker has no storage snapshot environment variable")
	}
	if _, err := client.BatchV1().Jobs("tasks").Update(ctx, job, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := backend.Cancel(ctx, taskID); err == nil {
		t.Fatal("cleanup accepted conflicting annotation and worker snapshots")
	}
	if _, err := client.CoreV1().PersistentVolumes().Get(ctx, "data-"+taskID, metav1.GetOptions{}); err != nil {
		t.Fatalf("conflicting snapshot caused PV deletion: %v", err)
	}
}

func TestLifecycleCanceledJobRetriesRejectedDelete(t *testing.T) {
	ctx := context.Background()
	client := bindingClient()
	conf := bindingConfig(t, createdStorage(t, true))
	taskID := "retry-canceled-delete"
	task := &tes.Task{Id: taskID, State: tes.State_QUEUED}
	database := &mockDatabase{tasks: map[string]*tes.Task{taskID: task}}
	backend := &Backend{client: client, conf: conf, event: &noopEventWriter{}, database: database, log: logger.NewLogger("lifecycle", logger.DefaultConfig())}
	if err := backend.Submit(ctx, task, conf); err != nil {
		t.Fatal(err)
	}
	task.State = tes.State_CANCELED
	deleteErr := errors.New("delete rejected")
	deleteAttempts := 0
	client.PrependReactor("delete", "jobs", func(k8stesting.Action) (bool, runtime.Object, error) {
		deleteAttempts++
		if deleteAttempts == 1 {
			return true, nil, deleteErr
		}
		return false, nil, nil
	})
	if err := backend.Cancel(ctx, taskID); err == nil {
		t.Fatal("Cancel should report the rejected delete")
	}
	job, err := client.BatchV1().Jobs("tasks").Get(ctx, taskID, metav1.GetOptions{})
	if err != nil || job.DeletionTimestamp != nil {
		t.Fatalf("rejected delete should leave an unfinished Job: job=%+v err=%v", job, err)
	}

	backend.reconcileOnce(ctx, true)
	if _, err := client.BatchV1().Jobs("tasks").Get(ctx, taskID, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("reconciliation did not retry canceled Job cleanup: %v", err)
	}
	if deleteAttempts != 2 {
		t.Fatalf("got %d delete attempts, want initial rejection and reconciliation retry", deleteAttempts)
	}
}

func TestLifecycleOrphanCleanupPropagatesTaskLookupFailure(t *testing.T) {
	backend := &Backend{database: lifecycleErrorDatabase{err: errors.New("permission denied")}}
	needed, err := backend.isResourceCleanupNeeded(context.Background(), "unknown-task")
	if err == nil || needed {
		t.Fatalf("task lookup failure authorized orphan cleanup: needed=%t err=%v", needed, err)
	}
	for _, notFound := range []error{tes.ErrNotFound, status.Error(codes.NotFound, "missing")} {
		backend.database = lifecycleErrorDatabase{err: notFound}
		needed, err = backend.isResourceCleanupNeeded(context.Background(), "missing-task")
		if err != nil || !needed {
			t.Fatalf("not-found lookup should authorize orphan cleanup: needed=%t err=%v", needed, err)
		}
	}
}

type lifecycleErrorDatabase struct {
	err error
}

func (d lifecycleErrorDatabase) GetTask(context.Context, *tes.GetTaskRequest) (*tes.Task, error) {
	return nil, d.err
}

func (lifecycleErrorDatabase) ListTasks(context.Context, *tes.ListTasksRequest) (*tes.ListTasksResponse, error) {
	return &tes.ListTasksResponse{}, nil
}

func (lifecycleErrorDatabase) Close() {}

type lifecycleSequencedDatabase struct {
	task   *tes.Task
	gets   int
	failAt int
}

func (d *lifecycleSequencedDatabase) GetTask(context.Context, *tes.GetTaskRequest) (*tes.Task, error) {
	d.gets++
	if d.gets == d.failAt {
		return nil, errors.New("temporary task lookup failure")
	}
	return d.task, nil
}

func (*lifecycleSequencedDatabase) ListTasks(context.Context, *tes.ListTasksRequest) (*tes.ListTasksResponse, error) {
	return &tes.ListTasksResponse{}, nil
}

func (*lifecycleSequencedDatabase) Close() {}
