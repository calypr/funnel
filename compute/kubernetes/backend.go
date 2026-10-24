// Package kubernetes contains code for accessing compute resources via the Kubernetes v1 Batch API.
package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"time"

	"dario.cat/mergo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/calypr/funnel/compute/kubernetes/resources"
	"github.com/calypr/funnel/compute/kubernetes/storageplan"
	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/logger"
	"github.com/calypr/funnel/plugins/proto"
	"github.com/calypr/funnel/tes"
	"github.com/calypr/funnel/util/k8sutil"
	googleproto "google.golang.org/protobuf/proto"

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
// Task creation resolves plugin configuration; cancellation uses recorded resources.
func (b *Backend) WriteEvent(ctx context.Context, ev *events.Event) error {
	switch ev.Type {
	case events.Type_TASK_CREATED:
		taskConfig := googleproto.Clone(b.conf).(*config.Config)
		b.log.Debug("taskConfig", "before plugin", taskConfig.Safe())
		if b.conf.Plugins != nil {
			resp, ok := ctx.Value("pluginResponse").(*proto.JobResponse)
			if !ok {
				return fmt.Errorf("missing or invalid plugin job response")
			}

			err := mergo.Merge(taskConfig, resp.Config, mergo.WithOverride)
			if err != nil {
				return fmt.Errorf("merging plugin config: %w", err)
			}
		}
		b.log.Debug("taskConfig", "after plugin", taskConfig.Safe())

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
	canceled, err := b.taskCanceled(ctx, task.Id)
	if err != nil {
		return fmt.Errorf("checking task state before Kubernetes submission: %w", err)
	}
	if canceled {
		b.log.Debug("skipping Kubernetes submission for canceled task", "taskID", task.Id)
		return nil
	}

	createErr := b.createResources(ctx, task, config)
	canceled, stateErr := b.taskCanceled(ctx, task.Id)
	if stateErr != nil {
		if createErr == nil {
			b.log.Error("deferring task reconciliation because its state is unavailable after submission", "taskID", task.Id, "error", stateErr)
			return nil
		}
		return errors.Join(fmt.Errorf("creating Worker resources: %w", createErr), fmt.Errorf("checking task state after Kubernetes submission: %w", stateErr))
	}
	if canceled {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if cleanupErr := b.cleanResources(cleanupCtx, task.Id); cleanupErr != nil {
			b.log.Error("cleaning up resources after task cancellation during submission", "taskID", task.Id, "error", cleanupErr)
		}
		return nil
	}

	if createErr != nil {
		b.log.Error("Error creating resources, writing SystemError event", "error", createErr, "task ID", task.Id)
		b.writeSystemError(ctx, task.Id, map[string]string{"error": createErr.Error()}, "Kubernetes job in FAILED state")
		return fmt.Errorf("creating Worker resources: %w", createErr)
	}

	return nil
}

// rollbackTask cancels a task after failed resource creation. Cancellation is
// best-effort during rollback, so a failure is logged, not propagated.
func (b *Backend) rollbackTask(taskID string, cause error) {
	if err := b.Cancel(context.Background(), taskID); err != nil {
		b.log.Error("failed to roll back task after resource creation error", "taskID", taskID, "rollbackError", err, "cause", cause)
	}
}

func (b *Backend) taskCanceled(ctx context.Context, taskID string) (bool, error) {
	if b.database == nil {
		return false, nil
	}
	task, err := b.database.GetTask(ctx, &tes.GetTaskRequest{Id: taskID, View: tes.View_MINIMAL.String()})
	if err != nil {
		return false, fmt.Errorf("reading task %q: %w", taskID, err)
	}
	if task == nil {
		return false, fmt.Errorf("reading task %q: database returned no task", taskID)
	}
	return task.State == tes.State_CANCELED, nil
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
	plan, err := storageplan.Resolve(task, config)
	if err != nil {
		return fmt.Errorf("resolving task storage: %w", err)
	}
	if config.Kubernetes.JobsNamespace != b.conf.Kubernetes.JobsNamespace {
		return fmt.Errorf("per-task configuration cannot change Kubernetes.JobsNamespace")
	}
	if plan != nil {
		if err := b.validateExistingStorage(ctx, plan); err != nil {
			return fmt.Errorf("validating bound storage: %w", err)
		}
	}
	// Create context with optional timeout
	var timeoutCtx context.Context = ctx
	var timeout time.Duration
	if config != nil && config.Kubernetes != nil && config.Kubernetes.Timeout != nil && config.Kubernetes.Timeout.GetDuration() != nil {
		timeout = config.Kubernetes.Timeout.GetDuration().AsDuration()

		var cancel context.CancelFunc
		timeoutCtx, cancel = context.WithTimeout(ctx, timeout) // derive from parent ctx
		defer cancel()
	}
	if err := b.validatePersistentVolumePermissions(timeoutCtx, plan); err != nil {
		return fmt.Errorf("validating persistent volume permissions: %w", err)
	}

	// Create Worker Job first so its UID can be used as an owner reference on
	// all subordinate namespaced resources, enabling automatic K8s GC cleanup.
	b.log.Debug("creating Worker Job", "taskID", task.Id)
	job, err := resources.CreateJob(timeoutCtx, task, config, b.client, b.log, plan)
	if err != nil {
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

	if plan != nil {
		if err := b.createBoundStorage(timeoutCtx, task.Id, config.Kubernetes.JobsNamespace, plan, *ownerRef); err != nil {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			cleanupErr := b.Cancel(cleanupCtx, task.Id)
			if cleanupErr != nil {
				cleanupErr = fmt.Errorf("rolling back task %q storage: %w", task.Id, cleanupErr)
			}
			return errors.Join(fmt.Errorf("creating bound storage: %w", err), cleanupErr)
		}
	}

	// Create ConfigMap (only when a template is configured; deployments using a
	// static shared ConfigMap via the WorkerTemplate volume spec skip this).
	if config.Kubernetes.ConfigMapTemplate != "" {
		b.log.Debug("creating Worker ConfigMap", "taskID", task.Id)
		err = resources.CreateConfigMap(timeoutCtx, task.Id, config, b.client, b.log, ownerRef)
		if err != nil {
			b.rollbackTask(task.Id, err)
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
				b.rollbackTask(task.Id, err)
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
			b.rollbackTask(task.Id, err)
			return fmt.Errorf("creating Worker Role: %w", err)
		}
	}

	if config.Kubernetes.RoleBindingTemplate != "" {
		b.log.Debug("creating Worker RoleBinding", "taskID", task.Id)
		err = resources.CreateRoleBinding(timeoutCtx, task, config, b.client, b.log, ownerRef)
		if err != nil {
			b.rollbackTask(task.Id, err)
			return fmt.Errorf("creating Worker RoleBinding: %w", err)
		}
	}

	// If the task has inputs, outputs, or declared volumes, create a PVC so
	// executor pods can share data via PVC subPath mounts.
	if plan == nil && (len(task.Inputs) > 0 || len(task.Outputs) > 0 || len(task.Volumes) > 0) {
		b.log.Debug("creating Worker PV", "taskID", task.Id)

		// Check to make sure required configs are present
		if len(config.GenericS3) == 0 ||
			config.GenericS3[0].Bucket == "" || config.GenericS3[0].Region == "" {
			return fmt.Errorf("Bucket or Region not found in GenericS3 config when attempting to create resources for task: %#v", task)
		}

		// Create PV (cluster-scoped — cannot be owned by a namespaced Job)
		diskGb := task.GetResources().GetDiskGb()
		err = resources.CreatePV(timeoutCtx, task.Id, diskGb, config, b.client, b.log)
		if err != nil {
			b.rollbackTask(task.Id, err)
			return fmt.Errorf("creating Worker PV: %w", err)
		}

		// Create PVC
		b.log.Debug("creating Worker PVC", "taskID", task.Id)
		err = resources.CreatePVC(timeoutCtx, task.Id, diskGb, config, b.client, b.log, ownerRef)
		if err != nil {
			b.rollbackTask(task.Id, err)
			return fmt.Errorf("creating Worker PVC: %w", err)
		}
	}

	return nil
}

var podWarningEventReasons = []string{
	"Failed",           // image pull failures, container start failures
	"BackOff",          // back-off restarting / pulling
	"ErrImagePull",     // explicit image-pull error
	"ImagePullBackOff", // image pull back-off
	"StartError",       // OCI runtime / entrypoint errors
}

const failedCreateThreshold = 5

// minFailureSpan is the minimum duration between the first and last FailedCreate
// event required before the reconciler acts. This prevents false-positives from
// rapid-fire bursts in the first few seconds of a job's life.
const minFailureSpan = 20 * time.Second
