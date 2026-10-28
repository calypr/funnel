package resources

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"text/template"

	"github.com/calypr/funnel/compute/kubernetes/storageplan"
	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/logger"
	"github.com/calypr/funnel/tes"
	v1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
)

// WorkerContainerAnnotation selects the regular Pod container running Funnel's worker.
const WorkerContainerAnnotation = "funnel.calypr.org/worker-container"

// SanitizeLabelValue converts an arbitrary string into a valid Kubernetes label
// value: replaces any character outside [A-Za-z0-9._-] with '-', strips
// leading/trailing non-alphanumeric characters, and truncates to 63 chars.
var labelInvalidChars = regexp.MustCompile(`[^A-Za-z0-9._-]`)
var labelEdgeTrim = regexp.MustCompile(`^[^A-Za-z0-9]+|[^A-Za-z0-9]+$`)

func SanitizeLabelValue(s string) string {
	s = labelInvalidChars.ReplaceAllString(s, "-")
	if len(s) > 63 {
		s = s[:63]
	}
	s = labelEdgeTrim.ReplaceAllString(s, "")
	return s
}

// Create the Funnel Worker job from kubernetes-template.yaml
// Executor job is created in worker/kubernetes.go#Run
func CreateJob(ctx context.Context, task *tes.Task, conf *config.Config, client kubernetes.Interface, log *logger.Logger, plan *storageplan.Plan) (*v1.Job, error) {
	// Parse Worker Template

	log.Debug("Creating job from template", "template", conf.Kubernetes.WorkerTemplate)
	t, err := template.New(task.Id).Parse(conf.Kubernetes.WorkerTemplate)
	if err != nil {
		return nil, fmt.Errorf("%v", err)
	}

	image := conf.Kubernetes.WorkerImage
	if image == "" {
		pods, err := client.CoreV1().Pods(conf.Kubernetes.Namespace).List(ctx, metav1.ListOptions{
			LabelSelector: "app=funnel",
		})
		if err != nil {
			return nil, fmt.Errorf("failed to list pods: %v", err)
		}
		if len(pods.Items) > 0 && len(pods.Items[0].Spec.Containers) > 0 {
			image = pods.Items[0].Spec.Containers[0].Image
		}
	}

	res := task.GetResources()
	if res == nil {
		res = &tes.Resources{}
	}

	// Resolve BackoffLimit: prefer backend_parameters["backoff_limit"], else default 10.
	backoffLimit := 10
	if bp := res.GetBackendParameters(); bp != nil {
		if v, ok := bp["backoff_limit"]; ok && v != "" {
			if n, err := strconv.Atoi(v); err == nil && n >= 0 {
				backoffLimit = n
			}
		}
	}

	templateData := map[string]interface{}{
		"TaskId":             task.Id,
		"TaskName":           task.Name,
		"TaskNameLabel":      SanitizeLabelValue(task.Name),
		"Namespace":          conf.Kubernetes.Namespace,
		"JobsNamespace":      conf.Kubernetes.JobsNamespace,
		"Cpus":               res.GetCpuCores(),
		"RamGb":              res.GetRamGb(),
		"DiskGb":             res.GetDiskGb(),
		"Image":              image,
		"BackoffLimit":       backoffLimit,
		"NeedsPVC":           plan == nil && (len(task.Inputs) > 0 || len(task.Outputs) > 0 || len(task.Volumes) > 0),
		"NodeSelector":       conf.Kubernetes.NodeSelector,
		"Tolerations":        conf.Kubernetes.Tolerations,
		"ServiceAccountName": fmt.Sprintf("funnel-worker-sa-%s-%s", conf.Kubernetes.JobsNamespace, task.Id),
	}

	// Override ServiceAccountName if provided in Task Tags
	if saName, exists := task.Tags["_WORKER_SA"]; exists && saName != "" {
		templateData["ServiceAccountName"] = saName
	}

	var buf bytes.Buffer
	err = t.Execute(&buf, templateData)
	if err != nil {
		return nil, fmt.Errorf("%v", err)
	}

	log.Debug("Job template", "template", buf.String())
	decode := scheme.Codecs.UniversalDeserializer().Decode
	obj, _, err := decode(buf.Bytes(), nil, nil)
	if err != nil {
		return nil, err
	}

	job, ok := obj.(*v1.Job)
	if !ok {
		return nil, fmt.Errorf("failed to decode job spec")
	}

	if plan != nil {
		if err := plan.ApplyWorker(job, conf.Worker.WorkDir, task.Id); err != nil {
			return nil, fmt.Errorf("binding worker storage: %w", err)
		}
	}
	if err := annotateWorkerContainer(job, plan); err != nil {
		return nil, fmt.Errorf("selecting worker container: %w", err)
	}
	configureWorkerAttempts(job)

	log.Debug("Creating job", "Job", job.Name, "JobsNamespace", conf.Kubernetes.JobsNamespace)
	created, err := client.BatchV1().Jobs(conf.Kubernetes.JobsNamespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("%v", err)
	}

	return created, nil
}

func annotateWorkerContainer(job *v1.Job, plan *storageplan.Plan) error {
	containers := job.Spec.Template.Spec.Containers
	selected := ""
	if plan != nil {
		selected = plan.WorkerContainer
	}
	if selected == "" && len(containers) == 1 {
		selected = containers[0].Name
	}
	if selected == "" && job.Spec.Template.Annotations != nil {
		selected = job.Spec.Template.Annotations[WorkerContainerAnnotation]
	}
	if selected == "" {
		return nil
	}
	for _, container := range containers {
		if container.Name == selected {
			if job.Spec.Template.Annotations == nil {
				job.Spec.Template.Annotations = make(map[string]string)
			}
			job.Spec.Template.Annotations[WorkerContainerAnnotation] = selected
			return nil
		}
	}
	return fmt.Errorf("pod template annotation %s names no regular container %q", WorkerContainerAnnotation, selected)
}

func configureWorkerAttempts(job *v1.Job) {
	// A claim identifies a pod, so a worker retry must get a new pod UID.
	job.Spec.Template.Spec.RestartPolicy = corev1.RestartPolicyNever
	identity := []corev1.EnvVar{
		{Name: events.OwnerNamespaceEnv, ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.namespace"}}},
		{Name: events.OwnerNameEnv, ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.name"}}},
		{Name: events.OwnerUIDEnv, ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.uid"}}},
	}
	for i := range job.Spec.Template.Spec.Containers {
		container := &job.Spec.Template.Spec.Containers[i]
		for _, variable := range identity {
			container.Env = slices.DeleteFunc(container.Env, func(existing corev1.EnvVar) bool { return existing.Name == variable.Name })
			container.Env = append(container.Env, variable)
		}
	}
}

// DeleteJob removes the worker job for a task and all associated executor jobs.
func DeleteJob(ctx context.Context, conf *config.Config, taskID string, client kubernetes.Interface, log *logger.Logger) error {
	jobsInterface := client.BatchV1().Jobs(conf.Kubernetes.JobsNamespace)

	var gracePeriod int64 = 0
	var prop metav1.DeletionPropagation = metav1.DeletePropagationForeground

	// Delete the worker job (named exactly as taskID)
	err := jobsInterface.Delete(ctx, taskID, metav1.DeleteOptions{
		GracePeriodSeconds: &gracePeriod,
		PropagationPolicy:  &prop,
	})
	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("%v", err)
	}

	// Delete executor jobs (named as {taskID}-{index})
	return DeleteExecutorJobs(ctx, conf, taskID, client, log)
}

// DeleteExecutorJobs deletes all executor jobs associated with a task.
// Executor jobs are named {taskID}-{index} and are created by the worker process.
func DeleteExecutorJobs(ctx context.Context, conf *config.Config, taskID string, client kubernetes.Interface, log *logger.Logger) error {
	jobsInterface := client.BatchV1().Jobs(conf.Kubernetes.JobsNamespace)

	jobs, err := jobsInterface.List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("listing jobs to find executor jobs for task %s: %v", taskID, err)
	}

	prefix := taskID + "-"
	var errs error
	var gracePeriod int64 = 0
	var prop metav1.DeletionPropagation = metav1.DeletePropagationForeground

	for _, job := range jobs.Items {
		if len(job.Name) > len(prefix) && job.Name[:len(prefix)] == prefix {
			log.Debug("deleting executor job", "jobName", job.Name, "taskID", taskID)
			delErr := jobsInterface.Delete(ctx, job.Name, metav1.DeleteOptions{
				GracePeriodSeconds: &gracePeriod,
				PropagationPolicy:  &prop,
			})
			// A job present in the List above can be removed (TTL controller or a
			// concurrent reconcile/cancel) before this Delete runs. A NotFound here
			// means the desired end state — the job is gone — so it is not an error.
			if delErr != nil && !errors.IsNotFound(delErr) {
				errs = fmt.Errorf("deleting executor job %s: %v", job.Name, delErr)
			}
		}
	}

	return errs
}
