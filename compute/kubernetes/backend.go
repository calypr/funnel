// Package kubernetes contains code for accessing compute resources via the Kubernetes v1 Batch API.
package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
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

	// Delete PVC
	err = resources.DeletePVC(ctx, taskId, b.conf.Kubernetes.JobsNamespace, b.client, b.log)
	if err != nil {
		errs = multierror.Append(errs, err)
		b.log.Error("deleting Worker PVC", "error", err)
	}

	// Delete per-task ConfigMap only if ConfigMapTemplate was configured
	if b.conf.Kubernetes.ConfigMapTemplate != "" {
		err = resources.DeleteConfigMap(ctx, taskId, b.conf.Kubernetes.JobsNamespace, b.client, b.log)
		if err != nil {
			errs = multierror.Append(errs, err)
			b.log.Error("deleting Worker ConfigMap", "error", err)
		}
	}

	// Delete RoleBinding
	err = resources.DeleteRoleBinding(ctx, taskId, b.conf.Kubernetes.JobsNamespace, b.client, b.log)
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

	// Delete Role
	err = resources.DeleteRole(ctx, taskId, b.conf.Kubernetes.JobsNamespace, b.client, b.log)
	if err != nil {
		errs = multierror.Append(errs, err)
		b.log.Error("deleting Worker Role", "error", err)
	}

	// Delete PV
	err = resources.DeletePV(ctx, taskId, b.conf.Kubernetes.JobsNamespace, b.client, b.log)
	if err != nil {
		errs = multierror.Append(errs, err)
		b.log.Error("deleting Worker PV", "error", err)
	}
	return errs
}

// isJobSchedulingTimedOut returns true if all pods for the given job have been
// stuck in Pending (with a scheduling condition) for longer than timeout.
// It returns false if any pod has been scheduled, or if pod status cannot be determined.
func (b *Backend) isJobSchedulingTimedOut(ctx context.Context, jobName string, timeout time.Duration) bool {
	pods, err := b.client.CoreV1().Pods(b.conf.Kubernetes.JobsNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("job-name=%s", jobName),
	})
	if err != nil {
		b.log.Error("reconcile: listing pods for job", "taskID", jobName, "error", err)
		return false
	}
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

func (b *Backend) cleanBacklog(ctx context.Context) {
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

func (b *Backend) reconcileJob(ctx context.Context, j *v1.Job, disableCleanup bool) {
	jobName := j.Name
	status := j.Status
	schedulingTimeout := b.conf.Kubernetes.Timeout.GetDuration()

	writeSystemError := func(reason string) {
		b.event.WriteEvent(ctx, events.NewState(jobName, tes.SystemError))
		b.event.WriteEvent(ctx, events.NewSystemLog(
			jobName, 0, 0, "error",
			"Kubernetes job in FAILED state",
			map[string]string{"error": reason},
		))
	}

	cleanResourcesIfEnabled := func() {
		if !disableCleanup {
			b.log.Debug("reconcile: cleaning up job", "taskID", jobName)
			if err := b.cleanResources(ctx, jobName); err != nil {
				b.log.Error("reconcile: failed to clean resources", "taskID", jobName, "error", err)
			}
		}
	}

	switch {
	case status.Active > 0:
		if schedulingTimeout != nil && b.isJobSchedulingTimedOut(ctx, jobName, schedulingTimeout.AsDuration()) {
			b.log.Debug("reconcile: worker pod scheduling timed out.", "taskID", jobName)
			writeSystemError("worker pod scheduling timed out")
			cleanResourcesIfEnabled()
		}

	case status.Succeeded > 0:
		b.log.Debug("reconcile: reconciled successful job", "taskID", jobName)
		cleanResourcesIfEnabled()

	case status.Failed > 0:
		// Only act if K8s has marked the Job as permanently failed (backoffLimit exhausted).
		// If Active > 0 is also set, K8s is still retrying — don't intervene.
		if status.Active > 0 {
			return
		}
		jobFailed := false
		for _, cond := range status.Conditions {
			if cond.Type == v1.JobFailed && cond.Status == corev1.ConditionTrue {
				jobFailed = true
				break
			}
		}
		if !jobFailed {
			// K8s hasn't given up yet — still within backoffLimit, retrying.
			return
		}
		task, err := b.database.GetTask(ctx, &tes.GetTaskRequest{Id: jobName, View: tes.View_MINIMAL.String()})
		if err != nil || task.State != tes.State_SYSTEM_ERROR {
			b.log.Debug("reconcile: writing system error event for failed job", "taskID", jobName)
			conds, err := json.Marshal(status.Conditions)
			if err != nil {
				b.log.Error("reconcile: marshaling failed job conditions", "taskID", jobName, "error", err)
			}
			writeSystemError(string(conds))
		}
		b.log.Debug("reconcile: reconciled failed job", "taskID", jobName)
		cleanResourcesIfEnabled()
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
	if !disableCleanup {
		for taskID := range k8sJobs {
			b.log.Info("reconcile: cleaning up job for terminal or missing Funnel task", "taskID", taskID)
			if err := b.cleanResources(ctx, taskID); err != nil {
				b.log.Error("reconcile: failed to clean orphaned resources", "taskID", taskID, "error", err)
			}
		}
	}

}

// reconcile is the ticker-based loop used when ExternalReconciler is false.
func (b *Backend) reconcile(ctx context.Context, rate time.Duration, disableCleanup bool) {
	if !disableCleanup {
		b.cleanBacklog(ctx)
	}

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

	pvcs, err := b.client.CoreV1().PersistentVolumeClaims(namespace).List(ctx, metav1.ListOptions{LabelSelector: "app=funnel"})
	if err != nil {
		b.log.Error("CleanOrphanedResources: listing PVCs", "error", err)
	} else {
		for _, r := range pvcs.Items {
			if id, ok := r.Labels["taskId"]; ok && id != "" {
				taskIDs[id] = struct{}{}
			}
		}
	}

	pvs, err := b.client.CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("app=funnel,namespace=%s", namespace),
	})
	if err != nil {
		b.log.Error("CleanOrphanedResources: listing PVs", "error", err)
	} else {
		for _, r := range pvs.Items {
			if id, ok := r.Labels["taskId"]; ok {
				taskIDs[id] = struct{}{}
			}
		}
	}

	// ConfigMaps: label-first, name-based fallback for resources missing the taskId label
	cms, err := b.client.CoreV1().ConfigMaps(namespace).List(ctx, metav1.ListOptions{LabelSelector: "app=funnel"})
	if err != nil {
		b.log.Error("CleanOrphanedResources: listing ConfigMaps", "error", err)
	} else {
		const cmPrefix = "funnel-worker-config-"
		//TODO: Deprecate the name-based fallback in favor of enforcing the presence of the taskId label on all resources
		for _, r := range cms.Items {
			if id, ok := r.Labels["taskId"]; ok {
				taskIDs[id] = struct{}{}
			} else if strings.HasPrefix(r.Name, cmPrefix) {
				taskIDs[strings.TrimPrefix(r.Name, cmPrefix)] = struct{}{}
			}
		}
	}

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

	roles, err := b.client.RbacV1().Roles(namespace).List(ctx, metav1.ListOptions{LabelSelector: "app=funnel"})
	if err != nil {
		b.log.Error("CleanOrphanedResources: listing Roles", "error", err)
	} else {
		for _, r := range roles.Items {
			if id, ok := r.Labels["taskId"]; ok {
				taskIDs[id] = struct{}{}
			}
		}
	}

	rbs, err := b.client.RbacV1().RoleBindings(namespace).List(ctx, metav1.ListOptions{LabelSelector: "app=funnel"})
	if err != nil {
		b.log.Error("CleanOrphanedResources: listing RoleBindings", "error", err)
	} else {
		for _, r := range rbs.Items {
			if id, ok := r.Labels["taskId"]; ok {
				taskIDs[id] = struct{}{}
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
