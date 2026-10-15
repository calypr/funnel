package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/hashicorp/go-multierror"
	"github.com/ohsu-comp-bio/funnel/compute/kubernetes/resources"
	"github.com/ohsu-comp-bio/funnel/compute/kubernetes/storageplan"
	"github.com/ohsu-comp-bio/funnel/tes"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// readTaskStorage reads the submission snapshot and its owning Job identity.
// A missing Job or a legacy Job has no storage plan.
func (b *Backend) readTaskStorage(ctx context.Context, taskID string) (*storageplan.Ownership, types.UID, error) {
	job, err := b.client.BatchV1().Jobs(b.conf.Kubernetes.JobsNamespace).Get(ctx, taskID, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("reading task storage ownership: %w", err)
	}
	snapshot := job.Annotations[storageplan.Annotation]
	workerSnapshot, workerIndex, hasWorkerSnapshot, err := workerStorageSnapshot(job)
	if err != nil {
		return nil, "", fmt.Errorf("reading worker storage snapshot: %w", err)
	}
	if snapshot == "" && hasWorkerSnapshot {
		snapshot = workerSnapshot
	}
	if snapshot == "" {
		if slices.Contains(job.Finalizers, storageplan.Finalizer) {
			return nil, "", fmt.Errorf("Job %s/%s retains the storage cleanup finalizer but has no storage plan annotation or %s worker environment snapshot", job.Namespace, job.Name, storageplan.Env)
		}
		return nil, job.UID, nil
	}
	ownership, err := storageplan.DecodeOwnership(snapshot)
	if err != nil {
		return nil, "", fmt.Errorf("reading task storage plan: %w", err)
	}
	if ownership.TaskID != taskID || ownership.Namespace != b.conf.Kubernetes.JobsNamespace {
		return nil, "", fmt.Errorf("task storage plan does not match Job identity")
	}
	if hasWorkerSnapshot {
		if workerSnapshot != snapshot {
			return nil, "", fmt.Errorf("Job %s/%s has conflicting storage plan annotation and worker environment snapshots", job.Namespace, job.Name)
		}
		if err := validateStoragePlanWorker(job, workerIndex, ownership); err != nil {
			return nil, "", err
		}
	}
	return ownership, job.UID, nil
}

func workerStorageSnapshot(job *batchv1.Job) (string, int, bool, error) {
	var snapshot string
	containerIndex := -1
	for i := range job.Spec.Template.Spec.Containers {
		for _, env := range job.Spec.Template.Spec.Containers[i].Env {
			if env.Name != storageplan.Env {
				continue
			}
			if env.ValueFrom != nil || env.Value == "" {
				return "", -1, false, fmt.Errorf("worker environment variable %s must contain a literal storage snapshot", storageplan.Env)
			}
			if containerIndex >= 0 {
				if env.Value != snapshot {
					return "", -1, false, fmt.Errorf("Job %s/%s has conflicting worker storage environment snapshots", job.Namespace, job.Name)
				}
				return "", -1, false, fmt.Errorf("Job %s/%s has duplicate worker storage environment snapshots", job.Namespace, job.Name)
			}
			snapshot = env.Value
			containerIndex = i
		}
	}
	return snapshot, containerIndex, containerIndex >= 0, nil
}

func validateStoragePlanWorker(job *batchv1.Job, containerIndex int, ownership *storageplan.Ownership) error {
	containers := job.Spec.Template.Spec.Containers
	if containerIndex < 0 || containerIndex >= len(containers) {
		return fmt.Errorf("Job %s/%s has an invalid storage plan worker container", job.Namespace, job.Name)
	}
	if ownership.WorkerContainer != "" {
		if containers[containerIndex].Name != ownership.WorkerContainer {
			return fmt.Errorf("Job %s/%s stores its storage plan outside worker container %q", job.Namespace, job.Name, ownership.WorkerContainer)
		}
		return nil
	}
	if len(containers) != 1 || containerIndex != 0 {
		return fmt.Errorf("Job %s/%s has an ambiguous worker container for its storage plan", job.Namespace, job.Name)
	}
	return nil
}

func isTaskNotFoundError(err error) bool {
	return errors.Is(err, tes.ErrNotFound) || status.Code(err) == codes.NotFound
}

// cleanResources deletes the resources created for a task.
func (b *Backend) cleanResources(ctx context.Context, taskId string) error {
	plan, ownerUID, storageErr := b.readTaskStorage(ctx, taskId)
	var errs error

	if storageErr != nil {
		errs = multierror.Append(errs, storageErr)
	} else if plan != nil && len(plan.OwnedVolumes) > 0 {
		// Keep the Job snapshot until every PV delete request is acknowledged.
		// Kubernetes' PV protection then waits for dependent PVCs to disappear.
		if err := b.deleteBoundVolumes(ctx, taskId, plan.Namespace, plan.OwnedVolumes, ownerUID); err != nil {
			errs = multierror.Append(errs, err)
		}
	}

	b.log.Debug("deleting Job", "taskID", taskId)
	err := resources.DeleteJob(ctx, b.conf, taskId, b.client, b.log)
	if err != nil {
		errs = multierror.Append(errs, err)
		b.log.Error("deleting Job", "error", err)
		if storageErr != nil {
			if err := resources.DeleteExecutorJobs(ctx, b.conf, taskId, b.client, b.log); err != nil {
				errs = multierror.Append(errs, err)
			}
		}
	} else if errs == nil && plan != nil && len(plan.OwnedVolumes) > 0 {
		if err := b.releaseStorageFinalizer(ctx, taskId, plan.Namespace, ownerUID); err != nil {
			return err
		}
	}

	saOpts := &resources.DeleteServiceAccountOptions{}
	if b.database != nil {
		task, err := b.database.GetTask(ctx, &tes.GetTaskRequest{Id: taskId, View: tes.View_FULL.String()})
		if err != nil {
			b.log.Debug("reading task for ServiceAccount cleanup; using task-scoped name", "taskID", taskId, "error", err)
		} else if task != nil {
			if workerSA := task.Tags["_WORKER_SA"]; workerSA != "" {
				saOpts.ServiceAccountName = workerSA
				saOpts.SharedSA = true
			}
		}
	}

	if storageErr == nil && ((plan == nil && b.conf.Kubernetes.Storage == nil) || b.conf.Kubernetes.ServiceAccountTemplate != "") {
		if err := resources.DeleteServiceAccount(ctx, taskId, b.conf.Kubernetes.JobsNamespace, b.client, b.log, saOpts); err != nil {
			errs = multierror.Append(errs, err)
			b.log.Error("deleting Worker ServiceAccount", "taskID", taskId, "error", err)
		}
	}

	if storageErr == nil && plan == nil && b.conf.Kubernetes.Storage == nil {
		if err := resources.DeletePV(ctx, taskId, b.conf.Kubernetes.JobsNamespace, b.client, b.log); err != nil {
			errs = multierror.Append(errs, err)
			b.log.Error("deleting Worker PV", "error", err)
		}
	}
	return errs
}

// CleanOrphanedResources deletes any Funnel-managed Kubernetes resources that are not associated
// with an active task in the database.
//
// This is intended to be called as a one-shot operation (e.g. from a Kubernetes CronJob) rather
// than as a long-running goroutine, so that cleanup is decoupled from the Funnel server lifecycle
// and multiple server replicas do not race to clean the same resources simultaneously.
func (b *Backend) CleanOrphanedResources(ctx context.Context) {
	b.log.Info("starting orphaned resource cleanup")
	namespace := b.conf.Kubernetes.JobsNamespace
	taskIDs := make(map[string]struct{})

	// Finalized Jobs preserve ownership even if current configuration no longer
	// provisions PVs, or a task plugin selected a different storage binding.
	jobs, err := b.client.BatchV1().Jobs(namespace).List(ctx, metav1.ListOptions{LabelSelector: "app=funnel-worker"})
	if err != nil {
		b.log.Error("CleanOrphanedResources: listing storage Jobs", "error", err)
	} else {
		for _, job := range jobs.Items {
			if !slices.Contains(job.Finalizers, storageplan.Finalizer) {
				continue
			}
			if job.DeletionTimestamp != nil {
				if err := b.cleanResources(ctx, job.Name); err != nil {
					b.log.Error("completing storage cleanup", "taskID", job.Name, "error", err)
				}
				continue
			}
			taskIDs[job.Name] = struct{}{}
		}
	}

	// Collect task IDs from resources that cleanResources manages directly.
	// ConfigMaps, PVCs, Roles, and RoleBindings are now owned by the Job via ownerReferences
	// and are garbage-collected by Kubernetes automatically — they are intentionally excluded here.

	var ownedPVs []corev1.PersistentVolume
	if b.usesPersistentVolumes() {
		pvs, err := b.client.CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{LabelSelector: fmt.Sprintf("app=funnel,namespace=%s", namespace)})
		if err != nil {
			b.log.Error("CleanOrphanedResources: listing PVs", "error", err)
		} else {
			for _, pv := range pvs.Items {
				if id := pv.Labels["taskId"]; id != "" {
					taskIDs[id] = struct{}{}
					if pv.Labels["funnel.calypr.org/storage-owned"] == "true" {
						ownedPVs = append(ownedPVs, pv)
					}
				}
			}
		}
	}

	// ServiceAccounts (shared SAs are not owned by a Job; task-scoped SAs may also be orphaned
	// if they were created before ownerRef support was added)
	if b.conf.Kubernetes.Storage == nil || b.conf.Kubernetes.ServiceAccountTemplate != "" {
		sas, err := b.client.CoreV1().ServiceAccounts(namespace).List(ctx, metav1.ListOptions{LabelSelector: "app=funnel"})
		if err != nil {
			b.log.Error("CleanOrphanedResources: listing ServiceAccounts", "error", err)
		} else {
			for _, r := range sas.Items {
				if id, ok := r.Labels["taskId"]; ok {
					taskIDs[id] = struct{}{}
				}
			}
		}

	}

	// Executor Jobs (label app=funnel-executor; named {taskID}-{index}).
	// These are not owned by the worker Job, so they must be discovered and cleaned explicitly.
	executorJobs, err := b.client.BatchV1().Jobs(namespace).List(ctx, metav1.ListOptions{LabelSelector: "app=funnel-executor"})
	if err != nil {
		b.log.Error("backlog cleanup: listing executor jobs", err)
	} else {
		for _, j := range executorJobs.Items {
			if taskID := extractTaskIDFromExecutorJobName(j.Name); taskID != "" {
				taskIDs[taskID] = struct{}{}
			}
		}
	}

	for taskID := range taskIDs {
		needsCleanup, err := b.isResourceCleanupNeeded(ctx, taskID)
		if err != nil {
			b.log.Error("CleanOrphanedResources: checking task state", "taskID", taskID, "error", err)
			continue
		}
		if !needsCleanup {
			continue
		}
		b.log.Info("CleanOrphanedResources: cleaning up resources for task", "taskID", taskID)
		if err := b.cleanResources(ctx, taskID); err != nil {
			b.log.Error("CleanOrphanedResources: failed to clean resources", "taskID", taskID, "error", err)
		}

		for _, pv := range ownedPVs {
			if pv.Labels["taskId"] != taskID {
				continue
			}
			if err := b.deleteBoundVolume(ctx, taskID, namespace, pv.Name); err != nil {
				b.log.Error("deleting orphaned bound PV", "pv", pv.Name, "error", err)
			}
		}

		// Brief pause between deletions to avoid hammering the K8s API server under high orphan counts.
		// TODO: consider batching (e.g. 1s per 10 tasks if any performance delays are observed).
		time.Sleep(500 * time.Millisecond)
	}
}
