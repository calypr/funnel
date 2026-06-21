// Package kubernetes contains code for accessing compute resources via the Kubernetes v1 Batch API.
package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"dario.cat/mergo"
	v1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/hashicorp/go-multierror"
	"github.com/ohsu-comp-bio/funnel/compute/kubernetes/resources"
	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/events"
	"github.com/ohsu-comp-bio/funnel/logger"
	"github.com/ohsu-comp-bio/funnel/plugins/proto"
	"github.com/ohsu-comp-bio/funnel/tes"
	"github.com/ohsu-comp-bio/funnel/util/k8sutil"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Backend represents the K8s backend.
type Backend struct {
	client            kubernetes.Interface
	event             events.Writer
	database          tes.ReadOnlyServer
	log               *logger.Logger
	backendParameters map[string]string
	conf              *config.Config // Funnel configuration
	events.Computer
}

// NewBackend returns a new K8s Backend instance.
func NewBackend(ctx context.Context, conf *config.Config, reader tes.ReadOnlyServer, writer events.Writer, log *logger.Logger) (*Backend, error) {
	if conf.Kubernetes.WorkerTemplate == "" {
		return nil, fmt.Errorf("invalid configuration; must provide a kubernetes job template")
	}
	// Funnel Server Namespace
	if conf.Kubernetes.Namespace == "" {
		return nil, fmt.Errorf("invalid configuration; must provide a kubernetes namespace")
	}

	// Funnel Worker + Executor Namespace
	if conf.Kubernetes.JobsNamespace == "" {
		conf.Kubernetes.JobsNamespace = conf.Kubernetes.Namespace
	}

	clientset, err := k8sutil.NewK8sClient(conf)
	if err != nil {
		return nil, fmt.Errorf("creating kubernetes client: %v", err)
	}

	b := &Backend{
		client:   clientset,
		event:    writer,
		database: reader,
		log:      log,
		conf:     conf,
	}

	if !conf.Kubernetes.DisableReconciler {
		b.cleanBacklog(ctx, conf.Kubernetes.DisableJobCleanup)

		// TODO: Add a condition based on whether ExternalReconciler is enabled or not,
		// so that we don't start the internal reconciler when an external one is configured.
		// This is to avoid the possibility of multiple concurrent reconcilers running for each server instance.
		rate := conf.Kubernetes.ReconcileRate.AsDuration()
		go b.reconcile(ctx, rate, conf.Kubernetes.DisableJobCleanup)
	}

	return b, nil
}

func (b Backend) CheckBackendParameterSupport(task *tes.Task) error {
	if !task.Resources.GetBackendParametersStrict() {
		return nil
	}

	taskBackendParameters := task.Resources.GetBackendParameters()
	for k := range taskBackendParameters {
		_, ok := b.backendParameters[k]
		if !ok {
			return status.Errorf(codes.InvalidArgument, "backend parameters not supported: %s", k)
		}
	}

	return nil
}

// WriteEvent writes an event to the compute backend.
// Currently, only TASK_CREATED is handled, which calls Submit.
func (b *Backend) WriteEvent(ctx context.Context, ev *events.Event) error {
	// TODO: Should this be moved to the switch statement so it's only run on TASK_CREATED?
	var taskConfig *config.Config = b.conf
	b.log.Debug("taskConfig", "before plugin", taskConfig.Safe())
	if b.conf.Plugins != nil {
		resp, ok := ctx.Value("pluginResponse").(*proto.JobResponse)
		if !ok {
			return fmt.Errorf("Failed to unmarshal plugin response %v", ctx.Value("pluginResponse"))
		}

		// TODO: Test that plugin response is being correctly set in taskConfig after this merge
		err := mergo.Merge(taskConfig, resp.Config, mergo.WithOverride)
		if err != nil {
			return fmt.Errorf("Failed to merge plugin config %v", err)
		}
	}
	b.log.Debug("taskConfig", "after plugin", taskConfig.Safe())

	switch ev.Type {
	case events.Type_TASK_CREATED:
		res := b.Submit(ctx, ev.GetTask(), taskConfig)
		return res
	case events.Type_TASK_STATE:
		if ev.GetState() == tes.State_CANCELED {
			return b.Cancel(ctx, ev.Id)
		}
	}
	return nil
}

func (b *Backend) Close() {
	// TODO: Close database or clean resources?
}

// Submit creates both the PVC and the worker job with better error handling
func (b *Backend) Submit(ctx context.Context, task *tes.Task, config *config.Config) error {
	err := b.createResources(ctx, task, config)

	if err != nil {
		b.log.Error("Error creating resources, writing SystemError event", "error", err, "task ID", task.Id)
		_ = b.event.WriteEvent(ctx, events.NewState(task.Id, tes.SystemError))
		_ = b.event.WriteEvent(
			context.Background(),
			events.NewSystemLog(
				task.Id, 0, 0, "error",
				"Kubernetes job in FAILED state",
				map[string]string{"error": err.Error()},
			),
		)

		return fmt.Errorf("creating Worker resources: %v", err)
	}

	return nil
}

// Cancel removes tasks that are pending kubernetes v1/batch jobs.
func (b *Backend) Cancel(ctx context.Context, taskID string) error {
	// Always attempt resource cleanup when a cancel is requested.
	//
	// cleanResources is idempotent — each individual delete either succeeds or
	// ignores NotFound — so calling it on an already-clean task is safe.
	return b.cleanResources(ctx, taskID)
}

// createResources creates the resources needed for a task.
func (b *Backend) createResources(ctx context.Context, task *tes.Task, config *config.Config) error {
	// Create context with optional timeout
	var timeoutCtx context.Context = ctx
	var timeout time.Duration
	if config != nil && config.Kubernetes != nil && config.Kubernetes.Timeout != nil && config.Kubernetes.Timeout.GetDuration() != nil {
		timeout = config.Kubernetes.Timeout.GetDuration().AsDuration()

		var cancel context.CancelFunc
		timeoutCtx, cancel = context.WithTimeout(ctx, timeout) // derive from parent ctx
		defer cancel()
	}

	// Create Worker Job first so its UID can be used as an owner reference on
	// all subordinate namespaced resources, enabling automatic K8s GC cleanup.
	b.log.Debug("creating Worker Job", "taskID", task.Id)
	job, err := resources.CreateJob(timeoutCtx, task, config, b.client, b.log)
	if err != nil {
		_ = b.Cancel(context.Background(), task.Id)
		return fmt.Errorf("creating Worker Job: %w", err)
	}

	blockOwnerDeletion := true
	isController := true
	ownerRef := &metav1.OwnerReference{
		APIVersion:         "batch/v1",
		Kind:               "Job",
		Name:               job.Name,
		UID:                job.UID,
		BlockOwnerDeletion: &blockOwnerDeletion,
		Controller:         &isController,
	}

	// Create ConfigMap (only when a template is configured; deployments using a
	// static shared ConfigMap via the WorkerTemplate volume spec skip this).
	if config.Kubernetes.ConfigMapTemplate != "" {
		b.log.Debug("creating Worker ConfigMap", "taskID", task.Id)
		err = resources.CreateConfigMap(timeoutCtx, task.Id, config, b.client, b.log, ownerRef)
		if err != nil {
			_ = b.Cancel(context.Background(), task.Id)
			return fmt.Errorf("creating Worker ConfigMap: %w", err)
		}
	}

	// Create ServiceAccount, Role, and RoleBinding only when templates are
	// configured. Deployments that supply a pre-existing shared SA (e.g. via
	// _WORKER_SA tag or a static Helm-managed SA) skip these steps entirely.
	// External (user-managed) SAs are not owned by the Job — they outlive tasks.
	if config.Kubernetes.ServiceAccountTemplate != "" {
		saName := fmt.Sprintf("funnel-worker-sa-%s-%s", config.Kubernetes.JobsNamespace, task.Id)
		sharedSA := false
		if sa, exists := task.Tags["_WORKER_SA"]; exists && sa != "" {
			saName = sa
			sharedSA = true
		}

		// TODO: Add error handler to handle case where Get fails for reasons other than `NotFound`
		// e.g. network issues, permission issues, etc.
		_, err = b.client.CoreV1().ServiceAccounts(config.Kubernetes.JobsNamespace).Get(timeoutCtx, saName, metav1.GetOptions{})

		// ServiceAccount does not exist, create it
		if err != nil {
			b.log.Debug("Error getting ServiceAccount:", "ServiceAccount", saName, "taskID", task.Id, "error", err)
			b.log.Debug("Creating Worker ServiceAccount", "taskID", task.Id)
			// Only set the owner reference for task-level SAs; external SAs are shared and must not be GC'd with the job.
			saOwnerRef := ownerRef
			if sharedSA {
				saOwnerRef = nil
			}
			err = resources.CreateServiceAccount(timeoutCtx, task, config, b.client, b.log, saOwnerRef)
			if err != nil {
				_ = b.Cancel(context.Background(), task.Id)
				return fmt.Errorf("creating Worker ServiceAccount: %w", err)
			}
		} else {
			b.log.Debug("ServiceAccount already exists, skipping creation", "ServiceAccount", saName, "taskID", task.Id)
		}
	}

	if config.Kubernetes.RoleTemplate != "" {
		b.log.Debug("creating Worker Role", "taskID", task.Id)
		err = resources.CreateRole(timeoutCtx, task, config, b.client, b.log, ownerRef)
		if err != nil {
			_ = b.Cancel(context.Background(), task.Id)
			return fmt.Errorf("creating Worker Role: %w", err)
		}
	}

	if config.Kubernetes.RoleBindingTemplate != "" {
		b.log.Debug("creating Worker RoleBinding", "taskID", task.Id)
		err = resources.CreateRoleBinding(timeoutCtx, task, config, b.client, b.log, ownerRef)
		if err != nil {
			_ = b.Cancel(context.Background(), task.Id)
			return fmt.Errorf("creating Worker RoleBinding: %w", err)
		}
	}

	// If the task has inputs, outputs, or declared volumes, create a PVC so
	// executor pods can share data via PVC subPath mounts.
	if len(task.Inputs) > 0 || len(task.Outputs) > 0 || len(task.Volumes) > 0 {
		b.log.Debug("creating Worker PV", "taskID", task.Id)

		// Check to make sure required configs are present
		if len(config.GenericS3) == 0 ||
			config.GenericS3[0].Bucket == "" || config.GenericS3[0].Region == "" {
			return fmt.Errorf("Bucket or Region not found in GenericS3 config when attempting to create resources for task: %#v", task)
		}

		// Create PV (cluster-scoped — cannot be owned by a namespaced Job)
		err = resources.CreatePV(timeoutCtx, task.Id, config, b.client, b.log)
		if err != nil {
			_ = b.Cancel(context.Background(), task.Id)
			return fmt.Errorf("creating Worker PV: %w", err)
		}

		// Create PVC
		b.log.Debug("creating Worker PVC", "taskID", task.Id)
		err = resources.CreatePVC(timeoutCtx, task.Id, config, b.client, b.log, ownerRef)
		if err != nil {
			_ = b.Cancel(context.Background(), task.Id)
			return fmt.Errorf("creating Worker PVC: %w", err)
		}
	}

	return nil
}

// cleanResources deletes the resources created for a task.
func (b *Backend) cleanResources(ctx context.Context, taskId string) error {
	var errs error

	// Delete Job
	b.log.Debug("deleting Job", "taskID", taskId)
	err := resources.DeleteJob(ctx, b.conf, taskId, b.client, b.log)
	if err != nil {
		errs = multierror.Append(errs, err)
		b.log.Error("deleting Job", "error", err)
	}

	// Determine the ServiceAccount for this task.
	// Default to the conventional task-scoped name; override if the task
	// specifies an externally-managed SA via the _WORKER_SA tag.
	saOpts := &resources.DeleteServiceAccountOptions{}
	if b.database != nil {
		if task, err := b.database.GetTask(ctx, &tes.GetTaskRequest{Id: taskId, View: tes.View_FULL.String()}); err == nil {
			if workerSA := task.Tags["_WORKER_SA"]; workerSA != "" {
				saOpts.ServiceAccountName = workerSA
				saOpts.SharedSA = true
			}
		}
	}

	if err := resources.DeleteServiceAccount(ctx, taskId, b.conf.Kubernetes.JobsNamespace, b.client, b.log, saOpts); err != nil {
		errs = multierror.Append(errs, err)
		b.log.Error("deleting Worker ServiceAccount", "taskID", taskId, "error", err)
	}

	// Delete PV
	err = resources.DeletePV(ctx, taskId, b.conf.Kubernetes.JobsNamespace, b.client, b.log)
	if err != nil {
		errs = multierror.Append(errs, err)
		b.log.Error("deleting Worker PV", "error", err)
	}
	return errs
}

// isJobDone reports whether the job has finished — either succeeded
// or permanently failed (backoffLimit exhausted) — as opposed to still
// being retried or in progress.
func (b *Backend) isJobDone(jobStatus v1.JobStatus) bool {
	for _, cond := range jobStatus.Conditions {
		if cond.Status != corev1.ConditionTrue {
			continue
		}
		if cond.Type == v1.JobComplete || cond.Type == v1.JobFailed {
			return true
		}
	}
	return false
}

// hasTerminalContainerWaitingError returns true if any pod in pods has a
// container stuck in a waiting state whose reason is known to be permanent
// (e.g. CreateContainerConfigError). These pods will never transition to a
// running state on their own so the task must be failed early rather than
// waiting for the Job's backoff limit to be exhausted.
func hasTerminalContainerWaitingError(pods *corev1.PodList) (bool, string) {
	terminalWaitingReasons := []string{
		"CreateContainerConfigError", // missing secret / configmap
		"InvalidImageName",           // malformed image reference
		"CreateContainerError",       // OCI runtime failed to create container
		"ErrImagePull",               // image not found or pull failed
		"ImagePullBackOff",           // repeated image pull failure
		"RunContainerError",          // runtime failed to start container (e.g. bad entrypoint)
		"StartError",                 // OCI runtime runc create failed
	}
	for _, pod := range pods.Items {
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.State.Waiting == nil {
				continue
			}
			reason := cs.State.Waiting.Reason
			if slices.Contains(terminalWaitingReasons, reason) {
				msg := cs.State.Waiting.Message
				if msg == "" {
					msg = reason
				}
				return true, fmt.Sprintf("%s: %s", reason, msg)
			}
		}
	}
	return false, ""
}

// podWarningEventReasons lists the pod event reasons (from `kubectl describe pod`)
// that are safe to surface to users as system log entries. These are all
// informational failure signals with no risk of leaking sensitive runtime internals.
var podWarningEventReasons = []string{
	"Failed",           // image pull failures, container start failures
	"BackOff",          // back-off restarting / pulling
	"ErrImagePull",     // explicit image-pull error
	"ImagePullBackOff", // image pull back-off
	"StartError",       // OCI runtime / entrypoint errors
}

// FetchPodWarningEvents returns Warning events for the given pods whose reason
// is in podWarningEventReasons. The messages are deduplicated and returned as a
// newline-joined string. An empty string is returned when nothing useful is
// found. This surfaces the human-readable detail that appears in
// `kubectl describe pod` (e.g. "Error: secret \"foo\" not found") into the
// TES task system logs.
func FetchPodWarningEvents(ctx context.Context, clientset kubernetes.Interface, namespace string, pods *corev1.PodList) string {
	seen := make(map[string]struct{})
	var messages []string
	for _, pod := range pods.Items {
		evList, err := clientset.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{
			FieldSelector: fmt.Sprintf("involvedObject.name=%s,type=Warning", pod.Name),
		})
		if err != nil {
			continue
		}
		for _, ev := range evList.Items {
			if !slices.Contains(podWarningEventReasons, ev.Reason) {
				continue
			}
			key := ev.Reason + ":" + ev.Message
			if _, dup := seen[key]; !dup {
				seen[key] = struct{}{}
				messages = append(messages, fmt.Sprintf("%s: %s", ev.Reason, ev.Message))
			}
		}
	}
	return strings.Join(messages, "\n")
}

// isJobSchedulingTimedOut returns true if all pods for the given job have been
// stuck in Pending (with a scheduling condition) for longer than timeout.
// It returns false if any pod has been scheduled, or if pod status cannot be determined.
func (b *Backend) isJobSchedulingTimedOut(timeout time.Duration, pods *corev1.PodList) bool {

	if len(pods.Items) == 0 {
		return false
	}
	now := time.Now()
	for _, pod := range pods.Items {
		if pod.Status.Phase != corev1.PodPending {
			return false
		}
		// Find the most recent scheduling condition transition time
		for _, cond := range pod.Status.Conditions {
			if cond.Type == corev1.PodScheduled && cond.Status == corev1.ConditionFalse {
				if now.Sub(cond.LastTransitionTime.Time) >= timeout {
					return true
				}
			}
		}
	}
	return false
}

// getFailedPodInfo returns a human-readable summary of why the most recently
// terminated pod for jobName failed: "exit code N (Reason): Message". It is
// best-effort; an empty string is returned when no useful information is found.
func (b *Backend) getFailedPodInfo(ctx context.Context, pods *corev1.PodList) string {

	var latestFinish metav1.Time
	var result string
	for _, pod := range pods.Items {
		for _, cs := range pod.Status.ContainerStatuses {
			t := cs.State.Terminated
			if t == nil || t.ExitCode == 0 {
				continue
			}
			if latestFinish.IsZero() || t.FinishedAt.After(latestFinish.Time) {
				latestFinish = t.FinishedAt
				reason := t.Reason
				if reason == "" {
					reason = "ExitError"
				}
				result = fmt.Sprintf("exit code %d (%s): %s", t.ExitCode, reason, t.Message)
			}
		}
	}
	return result
}

// failedCreateThreshold is the minimum number of FailedCreate events required
// before the reconciler treats the job as persistently broken.
const failedCreateThreshold = 5

// minFailureSpan is the minimum duration between the first and last FailedCreate
// event required before the reconciler acts. This prevents false-positives from
// rapid-fire bursts in the first few seconds of a job's life.
const minFailureSpan = 20 * time.Second

// hasJobFailedCreateEvent returns (totalCount, message) when the job has
// accumulated enough FailedCreate events spread over enough real time and no
// SuccessfulCreate has occurred after the last failure. Returns (0, "") when
// the failures look transient or have self-resolved.
//
// A FailedCreate event is emitted when the Job controller tried to create a pod
// but was rejected before the pod object was ever persisted (e.g. Pod Security
// Admission enforcement). In that case there are no pod container statuses to
// inspect, so hasTerminalContainerWaitingError cannot detect the failure.
func (b *Backend) hasJobFailedCreateEvent(ctx context.Context, jobName string) (int, string) {
	ns := b.conf.Kubernetes.JobsNamespace

	allEvents, err := b.client.CoreV1().Events(ns).List(ctx, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("involvedObject.name=%s", jobName),
	})
	if err != nil {
		b.log.Error("reconcile: listing events for job", "taskID", jobName, "error", err)
		return 0, ""
	}

	var (
		firstFailed   metav1.Time
		latestFailed  metav1.Time
		latestMsg     string
		totalCount    int
		latestSuccess metav1.Time
	)

	for _, ev := range allEvents.Items {
		last := ev.LastTimestamp
		if last.IsZero() {
			last = metav1.Time{Time: ev.CreationTimestamp.Time}
		}
		switch ev.Reason {
		case "FailedCreate":
			c := int(ev.Count)
			if c < 1 {
				c = 1 // brand-new singleton events have Count=0
			}
			totalCount += c
			// Use FirstTimestamp (the time of the first occurrence in a
			// deduplicated event) to measure how long the failures have been
			// occurring. Fall back to LastTimestamp if FirstTimestamp is unset.
			first := ev.FirstTimestamp
			if first.IsZero() {
				first = last
			}
			if firstFailed.IsZero() || first.Before(&firstFailed) {
				firstFailed = first
			}
			if latestFailed.IsZero() || last.After(latestFailed.Time) {
				latestFailed = last
				latestMsg = ev.Message
			}
		case "SuccessfulCreate":
			if latestSuccess.IsZero() || last.After(latestSuccess.Time) {
				latestSuccess = last
			}
		}
	}

	if totalCount == 0 {
		return 0, ""
	}

	// If a SuccessfulCreate occurred after the last failure the Job controller
	// recovered on its own; do not surface this as an error.
	if !latestSuccess.IsZero() && latestSuccess.After(latestFailed.Time) {
		b.log.Debug("reconcile: FailedCreate resolved by later SuccessfulCreate", "taskID", jobName)
		return 0, ""
	}

	// Require both a minimum count and a minimum span between first and last
	// failure before treating this as persistent.
	failureSpan := latestFailed.Time.Sub(firstFailed.Time)
	if totalCount < failedCreateThreshold || failureSpan < minFailureSpan {
		b.log.Debug("reconcile: FailedCreate below persistence threshold, skipping",
			"taskID", jobName, "count", totalCount, "span", failureSpan)
		return 0, ""
	}

	b.log.Debug("reconcile: persistent unresolved FailedCreate events",
		"taskID", jobName, "count", totalCount, "span", failureSpan, "reason", latestMsg)
	return totalCount, latestMsg
}

// listAllWorkerJobs returns a map of taskID -> Job for all funnel-worker jobs.
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
	if !disableCleanup {
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
		if s.Succeeded > 0 || s.Failed > 0 {
			b.log.Debug("backlog cleanup: deleting completed job", "taskID", taskID)
			if err := b.cleanResources(ctx, taskID); err != nil {
				b.log.Error("backlog cleanup: failed to clean resources", "taskID", taskID, "error", err)
			}
			continue
		}

		// Orphaned jobs (Active) whose task no longer exists in the Funnel DB — left over from a previous deployment or server crash.
		if s.Active > 0 {
			_, err := b.database.GetTask(ctx, &tes.GetTaskRequest{Id: taskID, View: tes.View_MINIMAL.String()})
			if err != nil {
				b.log.Info("backlog cleanup: deleting orphaned active job with no matching task", "taskID", taskID)
				if err := b.cleanResources(ctx, taskID); err != nil {
					b.log.Error("backlog cleanup: failed to clean orphaned resources", "taskID", taskID, "error", err)
				}
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

func (b *Backend) writeSystemError(ctx context.Context, jobName string, errAttributes map[string]string, additionalMessage string) {
	if additionalMessage == "" {
		additionalMessage = "Kubernetes job in FAILED state"
	}

	b.event.WriteEvent(ctx, events.NewState(jobName, tes.SystemError))
	b.event.WriteEvent(ctx, events.NewSystemLog(
		jobName, 0, 0, "error", additionalMessage,
		errAttributes,
	))
}

func (b *Backend) reconcileJob(ctx context.Context, j *v1.Job, disableCleanup bool) {
	jobName := j.Name
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
			b.writeSystemError(ctx, jobName, map[string]string{"error": errDetail}, "Kubernetes worker pod has a terminal container waiting error")
			b.cleanResourcesIfEnabled(ctx, jobName, disableCleanup)
			return
		}

		// Check for FailedCreate events on the Job itself. This catches
		// cases where pod creation is rejected before a pod object is
		// ever persisted (e.g. Pod Security Admission enforcement blocks
		// the pod), so there are no pod container statuses to inspect.
		b.log.Debug("checking for FailedCreate events on job", "taskID", jobName)
		if count, reason := b.hasJobFailedCreateEvent(ctx, jobName); count > 0 {
			b.log.Debug("reconcile: worker job has FailedCreate event", "taskID", jobName, "count", count, "reason", reason)
			b.writeSystemError(ctx, jobName, map[string]string{"error": reason}, "worker job failed to create pod")
			b.cleanResourcesIfEnabled(ctx, jobName, disableCleanup)
			return
		}

		if schedulingTimeout != nil && b.isJobSchedulingTimedOut(schedulingTimeout.AsDuration(), pods) {
			b.log.Debug("reconcile: worker pod scheduling timed out.", "taskID", jobName)
			b.writeSystemError(ctx, jobName, map[string]string{"error": "worker pod scheduling timed out"}, "")
			b.cleanResourcesIfEnabled(ctx, jobName, disableCleanup)
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
		task, err := b.database.GetTask(ctx, &tes.GetTaskRequest{Id: jobName, View: tes.View_MINIMAL.String()})
		if err != nil || task.State != tes.State_SYSTEM_ERROR {
			b.log.Debug("reconcile: writing system error event for failed job", "taskID", jobName)
			conds, err := json.Marshal(status.Conditions)
			if err != nil {
				b.log.Error("reconcile: marshaling failed job conditions", "taskID", jobName, "error", err)
			}
			errDetails := map[string]string{"error": string(conds)}
			if podInfo := b.getFailedPodInfo(ctx, pods); podInfo != "" {
				errDetails["executor_error"] = podInfo
			}
			b.writeSystemError(ctx, jobName, errDetails, "")
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
			b.writeSystemError(ctx, jobName, map[string]string{"error": reason}, "Kubernetes worker job failed to create pod")
			b.cleanResourcesIfEnabled(ctx, jobName, disableCleanup)
		}
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
	nonTerminalStates := []tes.State{tes.State_QUEUED, tes.State_INITIALIZING, tes.State_RUNNING}
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
				fmt.Println("DEBUG: Reconciling task", task.Id, "with state", task.State)
				j, exists := k8sJobs[task.Id]
				delete(k8sJobs, task.Id) // matched — remove so it isn't treated as orphaned
				if exists {
					b.reconcileJob(ctx, j, disableCleanup)
				} else {
					b.log.Debug("reconcile: job not found for task", "taskID", task.Id)
					b.writeSystemError(ctx, task.Id, map[string]string{"error": "job not found"}, "Kubernetes job not found for task")
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
		return true, nil
	}
	switch task.State {
	case tes.State_COMPLETE, tes.State_EXECUTOR_ERROR, tes.State_SYSTEM_ERROR, tes.State_CANCELED:
		return true, nil
	default:
		return false, nil
	}
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

	// Collect task IDs from resources that cleanResources manages directly.
	// ConfigMaps, PVCs, Roles, and RoleBindings are now owned by the Job via ownerReferences
	// and are garbage-collected by Kubernetes automatically — they are intentionally excluded here.

	// PVs (cluster-scoped; cannot be owned by a namespaced Job, so must be cleaned explicitly)
	pvs, err := b.client.CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{LabelSelector: fmt.Sprintf("app=funnel,namespace=%s", namespace)})
	if err != nil {
		b.log.Error("CleanOrphanedResources: listing PVs", "error", err)
	} else {
		for _, r := range pvs.Items {
			if id, ok := r.Labels["taskId"]; ok {
				taskIDs[id] = struct{}{}
			}
		}
	}

	// ServiceAccounts (shared SAs are not owned by a Job; task-scoped SAs may also be orphaned
	// if they were created before ownerRef support was added)
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

		// Brief pause between deletions to avoid hammering the K8s API server under high orphan counts.
		// TODO: consider batching (e.g. 1s per 10 tasks if any performance delays are observed).
		time.Sleep(500 * time.Millisecond)
	}
}
