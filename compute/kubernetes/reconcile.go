// Package kubernetes contains code for accessing compute resources via the Kubernetes v1 Batch API.
package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	v1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/calypr/funnel/compute/kubernetes/storageplan"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/tes"
)

func (b *Backend) listAllWorkerJobs(ctx context.Context) (map[string]*v1.Job, error) {
	jobs, err := b.client.BatchV1().Jobs(b.conf.Kubernetes.JobsNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app=funnel-worker",
	})
	if err != nil {
		return nil, err
	}
	k8sJobs := make(map[string]*v1.Job, len(jobs.Items))
	for i := range jobs.Items {
		k8sJobs[jobs.Items[i].Name] = &jobs.Items[i]
	}
	return k8sJobs, nil
}

func (b *Backend) cleanBacklog(ctx context.Context, disableCleanup bool) {
	if disableCleanup {
		return
	}

	k8sJobs, err := b.listAllWorkerJobs(ctx)
	if err != nil {
		b.log.Error("backlog cleanup: listing jobs", err)
		return
	}

	for taskID, j := range k8sJobs {
		s := j.Status

		// Completed jobs (Succeeded/Failed) that were not cleaned up before the server restarted.
		if s.Failed > 0 {
			b.reconcileJob(ctx, j, false)
			continue
		}
		if s.Succeeded > 0 {
			b.log.Debug("backlog cleanup: deleting completed job", "taskID", taskID)
			if err := b.cleanResources(ctx, taskID); err != nil {
				b.log.Error("backlog cleanup: failed to clean resources", "taskID", taskID, "error", err)
			}
			continue
		}

		// Orphaned jobs (Active) whose task no longer exists in the Funnel DB — left over from a previous deployment or server crash.
		if s.Active > 0 {
			_, err := b.database.GetTask(ctx, &tes.GetTaskRequest{Id: taskID, View: tes.View_MINIMAL.String()})
			if isTaskNotFoundError(err) {
				b.log.Info("backlog cleanup: deleting orphaned active job with no matching task", "taskID", taskID)
				if err := b.cleanResources(ctx, taskID); err != nil {
					b.log.Error("backlog cleanup: failed to clean orphaned resources", "taskID", taskID, "error", err)
				}
			} else if err != nil {
				b.log.Error("backlog cleanup: checking task state", "taskID", taskID, "error", err)
			}
		}
	}

	// In case there are any orphaned resources that were missed by the above loop (e.g. due to a transient DB error),
	// do one final sweep of all resources with no matching task.
	b.CleanOrphanedResources(ctx)
}

func (b *Backend) cleanResourcesIfEnabled(ctx context.Context, jobName string, disableCleanup bool) {
	if !disableCleanup {
		b.log.Debug("reconcile: cleaning up job", "taskID", jobName)
		if err := b.cleanResources(ctx, jobName); err != nil {
			b.log.Error("reconcile: failed to clean resources", "taskID", jobName, "error", err)
		}
	}
}

func (b *Backend) writeSystemError(ctx context.Context, jobName string, errAttributes map[string]string, additionalMessage string) bool {
	if additionalMessage == "" {
		additionalMessage = "Kubernetes job in FAILED state"
	}
	if b.database != nil {
		task, err := b.database.GetTask(ctx, &tes.GetTaskRequest{Id: jobName, View: tes.View_MINIMAL.String()})
		if err != nil {
			if isTaskNotFoundError(err) {
				return true
			}
			b.log.Error("reconcile: cannot confirm task state before writing SYSTEM_ERROR", "taskID", jobName, "error", err)
			return false
		}
		if task == nil {
			return false
		}
		if tes.TerminalState(task.State) {
			return true
		}
	}

	if werr := b.event.WriteEvent(ctx, events.NewSystemLog(
		jobName, 0, 0, "error", additionalMessage,
		errAttributes,
	)); werr != nil {
		b.log.Error("writing reconcile-failure log event", "taskID", jobName, "error", werr)
		return false
	}
	if werr := b.event.WriteEvent(ctx, events.NewState(jobName, tes.SystemError)); werr != nil {
		b.log.Error("writing SystemError event", "taskID", jobName, "error", werr)
		return false
	}
	return true
}

func (b *Backend) reconcileRecordedOwner(ctx context.Context, j *v1.Job, disableCleanup bool) bool {
	if b.database == nil {
		return false
	}
	task, err := b.database.GetTask(ctx, &tes.GetTaskRequest{Id: j.Name, View: tes.View_FULL.String()})
	if err != nil {
		b.log.Error("reconcile: reading task execution owner", "taskID", j.Name, "error", err)
		return false
	}
	if task == nil || tes.TerminalState(task.State) {
		return false
	}
	owner, err := events.TaskExecutionOwner(task)
	if err != nil {
		b.log.Error("reconcile: reading task execution owner metadata", "taskID", j.Name, "error", err)
		return false
	}
	if owner == nil || owner.Namespace != j.Namespace || j.UID == "" {
		return false
	}

	pod, err := b.client.CoreV1().Pods(owner.Namespace).Get(ctx, owner.PodName, metav1.GetOptions{})
	var reason string
	switch {
	case apierrors.IsNotFound(err):
		reason = "is missing"
	case err != nil:
		b.log.Error("reconcile: reading execution owner Pod", "taskID", j.Name, "namespace", owner.Namespace, "pod", owner.PodName, "error", err)
		return false
	case string(pod.UID) != owner.PodUid:
		reason = "was replaced by a different pod UID"
	case !podControlledByJob(pod, j):
		return false
	case pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed:
		reason = fmt.Sprintf("exited in phase %s", pod.Status.Phase)
	default:
		return false
	}

	ownerDescription := fmt.Sprintf("execution owner Pod %s/%s (UID %s) %s", owner.Namespace, owner.PodName, owner.PodUid, reason)
	b.log.Debug("reconcile: recorded execution owner Pod exited or was lost", "taskID", j.Name, "ownerPod", ownerDescription)
	if b.writeSystemError(ctx, j.Name, map[string]string{"error": ownerDescription}, "Kubernetes worker pod that claimed task execution exited or was lost") {
		b.cleanResourcesIfEnabled(ctx, j.Name, disableCleanup)
	}
	return true
}

func podControlledByJob(pod *corev1.Pod, job *v1.Job) bool {
	if pod.Namespace != job.Namespace || job.UID == "" {
		return false
	}
	for _, owner := range pod.OwnerReferences {
		if owner.Kind == "Job" && owner.Name == job.Name && owner.Controller != nil && *owner.Controller && owner.UID == job.UID {
			return true
		}
	}
	return false
}

func (b *Backend) reconcileJob(ctx context.Context, j *v1.Job, disableCleanup bool) {
	jobName := j.Name
	if j.DeletionTimestamp != nil && slices.Contains(j.Finalizers, storageplan.Finalizer) {
		if err := b.cleanResources(ctx, jobName); err != nil {
			b.log.Error("reconcile: completing storage cleanup", "taskID", jobName, "error", err)
		}
		return
	}
	if b.reconcileRecordedOwner(ctx, j, disableCleanup) {
		return
	}
	status := j.Status
	schedulingTimeout := b.conf.Kubernetes.Timeout.GetDuration()

	pods, err := b.client.CoreV1().Pods(b.conf.Kubernetes.JobsNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("job-name=%s", jobName),
	})
	if err != nil {
		b.log.Error("reconcile: failed to list pods for job", "taskID", jobName, "error", err)
		return
	}

	switch {
	case status.Active > 0:

		// Check for container waiting errors that will never self-resolve
		// (e.g. CreateContainerConfigError). These keep the Job Active
		// indefinitely, so we must detect and fail them explicitly.
		if terminal, reason := hasTerminalContainerWaitingError(pods); terminal {
			b.log.Debug("reconcile: worker pod has terminal container waiting error", "taskID", jobName, "reason", reason)
			errDetail := reason
			if podEvents := FetchPodWarningEvents(ctx, b.client, b.conf.Kubernetes.JobsNamespace, pods); podEvents != "" {
				errDetail = fmt.Sprintf("%s\n%s", reason, podEvents)
			}
			if b.writeSystemError(ctx, jobName, map[string]string{"error": errDetail}, "Kubernetes worker pod has a terminal container waiting error") {
				b.cleanResourcesIfEnabled(ctx, jobName, disableCleanup)
			}
			return
		}

		// Check for FailedCreate events on the Job itself. This catches
		// cases where pod creation is rejected before a pod object is
		// ever persisted (e.g. Pod Security Admission enforcement blocks
		// the pod), so there are no pod container statuses to inspect.
		b.log.Debug("checking for FailedCreate events on job", "taskID", jobName)
		if count, reason := b.hasJobFailedCreateEvent(ctx, jobName); count > 0 {
			b.log.Debug("reconcile: worker job has FailedCreate event", "taskID", jobName, "count", count, "reason", reason)
			if b.writeSystemError(ctx, jobName, map[string]string{"error": reason}, "worker job failed to create pod") {
				b.cleanResourcesIfEnabled(ctx, jobName, disableCleanup)
			}
			return
		}

		if schedulingTimeout != nil && b.isJobSchedulingTimedOut(schedulingTimeout.AsDuration(), pods) {
			b.log.Debug("reconcile: worker pod scheduling timed out.", "taskID", jobName)
			if b.writeSystemError(ctx, jobName, map[string]string{"error": "worker pod scheduling timed out"}, "") {
				b.cleanResourcesIfEnabled(ctx, jobName, disableCleanup)
			}
		}

	case status.Succeeded > 0:
		b.log.Debug("reconcile: reconciled successful job", "taskID", jobName)
		b.cleanResourcesIfEnabled(ctx, jobName, disableCleanup)

	case status.Failed > 0:
		b.log.Debug("reconcile: Job has non zero failed status", "taskID", jobName, "failed", status.Failed)
		// Only act if K8s has marked the Job as permanently failed (backoffLimit exhausted).
		// If Active > 0 is also set, K8s is still retrying — don't intervene.
		if status.Active > 0 {
			return
		}
		b.log.Debug("reconcile: checking is job marked as failed", "taskID", jobName)
		if !b.isJobDone(status) {
			// K8s hasn't given up yet — still within backoffLimit, retrying.
			b.log.Debug("reconcile: K8s hasn't given up yet — still within backoffLimit, retrying.", "taskID", jobName)
			return
		}
		b.log.Debug("reconcile: writing system error event for failed job", "taskID", jobName)
		conds, err := json.Marshal(status.Conditions)
		if err != nil {
			b.log.Error("reconcile: marshaling failed job conditions", "taskID", jobName, "error", err)
		}
		errDetails := map[string]string{"error": string(conds)}
		if podInfo := b.getFailedPodInfo(ctx, pods); podInfo != "" {
			errDetails["executor_error"] = podInfo
		}
		if !b.writeSystemError(ctx, jobName, errDetails, "") {
			return
		}

		b.cleanResourcesIfEnabled(ctx, jobName, disableCleanup)
	default:
		// All status counters are zero: the Job controller has not yet
		// recorded any Active/Succeeded/Failed pods. This happens when
		// every pod creation attempt is rejected before Kubernetes
		// persists a pod object (e.g. Pod Security Admission blocks the
		// pod). Check for FailedCreate events which are the only signal
		// available in this state.
		if count, reason := b.hasJobFailedCreateEvent(ctx, jobName); count > 0 {
			b.log.Debug("reconcile: worker job has FailedCreate event (zero-status)", "taskID", jobName, "count", count, "reason", reason)
			if b.writeSystemError(ctx, jobName, map[string]string{"error": reason}, "Kubernetes worker job failed to create pod") {
				b.cleanResourcesIfEnabled(ctx, jobName, disableCleanup)
			}
		}
	}
}

func (b *Backend) reconcileMissingJob(ctx context.Context, task *tes.Task, disableCleanup bool) {
	job, err := b.client.BatchV1().Jobs(b.conf.Kubernetes.JobsNamespace).Get(ctx, task.Id, metav1.GetOptions{})
	if err == nil {
		b.reconcileJob(ctx, job, disableCleanup)
		return
	}
	if !apierrors.IsNotFound(err) {
		b.log.Error("reconcile: getting worker job absent from initial list", "taskID", task.Id, "error", err)
		return
	}

	switch task.State {
	case tes.State_QUEUED:
		b.log.Debug("reconcile: worker job not created for queued task yet", "taskID", task.Id)
	case tes.State_INITIALIZING, tes.State_RUNNING:
		b.log.Debug("reconcile: job not found for started task", "taskID", task.Id)
		b.writeSystemError(ctx, task.Id, map[string]string{"error": "job not found"}, "Kubernetes job not found for task")
	}
}

// reconcileOnce performs a single reconciliation pass.
func (b *Backend) reconcileOnce(ctx context.Context, disableCleanup bool) {
	k8sJobs, err := b.listAllWorkerJobs(ctx)
	if err != nil {
		b.log.Error("reconcile: listing jobs", err)
		return
	}

	// Page through all non-terminal Funnel tasks and reconcile against K8s Jobs.
	// Matched jobs are removed from k8sJobs so any remainder can be identified as orphaned.
	//
	// A task can be listed under two states in one pass: it may transition
	// (e.g. INITIALIZING to RUNNING) between the two ListTasks calls. The
	// matched set dedupes those sightings so the second listing cannot be
	// mistaken for a job that vanished — a false "job not found" here cancels
	// a healthy running task.
	nonTerminalStates := []tes.State{tes.State_QUEUED, tes.State_INITIALIZING, tes.State_RUNNING}
	matched := make(map[string]bool)
	for _, state := range nonTerminalStates {
		pageToken := ""
		for {
			lresp, err := b.database.ListTasks(ctx, &tes.ListTasksRequest{
				State:     state,
				PageSize:  100,
				PageToken: pageToken,
			})
			if err != nil {
				b.log.Error("reconcile: listing tasks", "state", state, "error", err)
				break
			}
			for _, task := range lresp.Tasks {
				if matched[task.Id] {
					continue
				}
				j, exists := k8sJobs[task.Id]
				delete(k8sJobs, task.Id) // matched — remove so it isn't treated as orphaned
				matched[task.Id] = true
				if exists {
					b.reconcileJob(ctx, j, disableCleanup)
				} else {
					b.reconcileMissingJob(ctx, task, disableCleanup)
				}
			}
			pageToken = lresp.NextPageToken
			if pageToken == "" {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
	}

	// Any jobs still in k8sJobs were not matched to any Funnel task — orphaned or in terminal states.
	for taskID, job := range k8sJobs {
		if job.DeletionTimestamp != nil && slices.Contains(job.Finalizers, storageplan.Finalizer) {
			b.reconcileJob(ctx, job, disableCleanup)
			continue
		}
		if b.database != nil {
			task, taskErr := b.database.GetTask(ctx, &tes.GetTaskRequest{Id: taskID, View: tes.View_MINIMAL.String()})
			if taskErr == nil && task != nil && task.State == tes.State_CANCELED {
				if err := b.cleanResources(ctx, taskID); err != nil {
					b.log.Error("reconcile: retrying canceled task cleanup", "taskID", taskID, "error", err)
				}
				continue
			}
			if taskErr == nil && task == nil {
				b.log.Error("reconcile: database returned no task for unmatched Job", "taskID", taskID)
				continue
			}
			if taskErr != nil && !isTaskNotFoundError(taskErr) {
				b.log.Error("reconcile: checking task state for unmatched Job", "taskID", taskID, "error", taskErr)
				continue
			}
		}
		b.log.Debug("reconcile: Task is either orphaned or in a terminal state", "taskID", taskID)
		if !b.isJobDone(job.Status) {
			b.log.Debug("reconcile: K8s hasn't given up yet — still within backoffLimit, retrying.", "taskID", taskID, "failed", job.Status.Failed, "backoffLimit", *job.Spec.BackoffLimit)
			continue
		}
		b.cleanResourcesIfEnabled(ctx, taskID, disableCleanup)
	}

}

// reconcile is the ticker-based loop used when ExternalReconciler is false.
func (b *Backend) reconcile(ctx context.Context, rate time.Duration, disableCleanup bool) {
	ticker := time.NewTicker(rate)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b.reconcileOnce(ctx, disableCleanup)
		}
	}
}

// extractTaskIDFromExecutorJobName parses the taskID from an executor job name.
// Executor jobs are named "{taskID}-{index}" where index is a non-negative integer.
// Returns an empty string if the name does not match the expected pattern.
func extractTaskIDFromExecutorJobName(name string) string {
	idx := strings.LastIndex(name, "-")
	if idx <= 0 {
		return ""
	}
	suffix := name[idx+1:]
	for _, c := range suffix {
		if c < '0' || c > '9' {
			return ""
		}
	}
	if len(suffix) == 0 {
		return ""
	}
	return name[:idx]
}

// isResourceCleanupNeeded returns true when the task is confirmed gone (NotFound)
// or in a terminal state.
func (b *Backend) isResourceCleanupNeeded(ctx context.Context, taskID string) (bool, error) {
	task, err := b.database.GetTask(ctx, &tes.GetTaskRequest{Id: taskID, View: tes.View_MINIMAL.String()})
	if err != nil {
		if isTaskNotFoundError(err) {
			return true, nil
		}
		return false, fmt.Errorf("checking task state for cleanup: %w", err)
	}
	if task == nil {
		return false, fmt.Errorf("checking task state for cleanup: database returned no task")
	}
	switch task.State {
	case tes.State_COMPLETE, tes.State_EXECUTOR_ERROR, tes.State_SYSTEM_ERROR, tes.State_CANCELED:
		return true, nil
	default:
		return false, nil
	}
}
