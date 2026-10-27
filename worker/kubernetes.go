package worker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"text/template"
	"time"

	k8sbackend "github.com/calypr/funnel/compute/kubernetes"
	"github.com/calypr/funnel/compute/kubernetes/storageplan"
	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/logger"
	"github.com/calypr/funnel/tes"
	v1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	batchv1 "k8s.io/client-go/kubernetes/typed/batch/v1"
	"k8s.io/client-go/rest"
	"mvdan.cc/sh/v3/syntax"
)

// KubernetesCommand is responsible for configuring and running a task in a Kubernetes cluster.
type KubernetesCommand struct {
	StoragePlan     *storageplan.Plan
	TaskId          string
	JobId           int
	StdinFile       string
	StdoutFile      string
	StderrFile      string
	TaskTemplate    string
	Namespace       string // Funnel Server Namespace
	JobsNamespace   string // Funnel Worker + Executor Namespace (default: Namespace)
	NodeSelector    map[string]string
	Tolerations     []map[string]interface{}
	Resources       *tes.Resources
	ResourceLimits  *tes.Resources
	ServiceAccount  string
	ImagePullPolicy corev1.PullPolicy
	NeedsPVC        bool
	Clientset       kubernetes.Interface
	Command
}

type K8sExecutorErr struct {
	ExitCode int
	Reason   string
	Message  string
	JobName  string
}

type K8sSystemErr struct {
	Reason  string
	Message string
	Err     error
	error
}

func (e *K8sExecutorErr) Error() string {
	reason := e.Reason
	if reason == "" || reason == "Error" {
		reason = "ExitError"
	}
	return fmt.Sprintf("executor job %s failed with exit code %d (%s): %s",
		e.JobName, e.ExitCode, reason, e.Message)
}

func (e *K8sSystemErr) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("kubernetes system error (%s): %s: %v", e.Reason, e.Message, e.Err)
	}
	return fmt.Sprintf("kubernetes system error (%s): %s", e.Reason, e.Message)
}

func (e *K8sSystemErr) Unwrap() error {
	return e.Err
}

// normalizeShellCommand ensures a single-element command string is safe to
// pass to /bin/sh -c. If the string is already valid shell syntax it is
// returned unchanged. If the parser rejects it (e.g. an unterminated single
// quote in "echo Hello O'hare!"), each whitespace-separated token is wrapped
// in single quotes with any internal single quotes escaped, preserving the
// original word boundaries.
func normalizeShellCommand(s string) string {
	if _, err := syntax.NewParser().Parse(strings.NewReader(s), ""); err == nil {
		return s
	}
	tokens := strings.Fields(s)
	for i, tok := range tokens {
		escaped := strings.ReplaceAll(tok, "'", "'\\''")
		tokens[i] = "'" + escaped + "'"
	}
	return strings.Join(tokens, " ")
}

// Create the Executor K8s job from kubernetes-executor-template.yaml
// Funnel Worker job is created in compute/kubernetes/backend.go#CreateResources
func (kcmd KubernetesCommand) Run(ctx context.Context) error {
	var taskId = kcmd.TaskId
	tpl, err := template.New(taskId).Parse(kcmd.TaskTemplate)

	if err != nil {
		return &K8sSystemErr{
			Reason:  "TemplateParsingFailed",
			Message: "Failed to parse task template",
			Err:     err,
		}
	}

	var cmd = kcmd.ShellCommand

	// When stdio redirects are present, collapse the command into a single
	// shell string so the executor template's shell wrapper (which takes only
	// index .Command 0) receives the full command including redirects.
	hasRedirects := kcmd.StdinFile != "" || kcmd.StdoutFile != "" || kcmd.StderrFile != ""
	if hasRedirects {
		// Quote each argument to preserve spaces/special characters, then
		// append the redirect operators (which must not be quoted).
		parts := make([]string, len(cmd))
		for i, arg := range cmd {
			parts[i] = strings.ReplaceAll(arg, "'", "'\\''")
			parts[i] = "'" + parts[i] + "'"
		}
		shellCmd := strings.Join(parts, " ")
		if kcmd.StdinFile != "" {
			shellCmd += " < " + kcmd.StdinFile
		}
		if kcmd.StdoutFile != "" {
			shellCmd += " > " + kcmd.StdoutFile
		}
		if kcmd.StderrFile != "" {
			shellCmd += " 2> " + kcmd.StderrFile
		}
		cmd = []string{shellCmd}
	}

	// Normalize single-element shell scripts before passing them to /bin/sh -c.
	// Multi-element commands are exec'd directly and bypass the shell entirely.
	if len(cmd) == 1 && !hasRedirects {
		cmd[0] = normalizeShellCommand(cmd[0])
	}

	// Use a shell wrapper when the command is a single element (a shell script
	// string) or when stdio redirects are present.
	useShell := len(cmd) == 1 || hasRedirects

	templateData := map[string]interface{}{
		"TaskId":             taskId,
		"JobId":              kcmd.JobId,
		"Namespace":          kcmd.Namespace,
		"JobsNamespace":      kcmd.JobsNamespace,
		"Command":            cmd,
		"UseShell":           useShell,
		"Workdir":            kcmd.Workdir,
		"Volumes":            kcmd.Volumes,
		"Env":                kcmd.Env,
		"Cpus":               kcmd.Resources.CpuCores,
		"RamGb":              kcmd.Resources.RamGb,
		"DiskGb":             kcmd.Resources.DiskGb,
		"CpusLimit":          kcmd.ResourceLimits.CpuCores,
		"RamGbLimit":         kcmd.ResourceLimits.RamGb,
		"DiskGbLimit":        kcmd.ResourceLimits.DiskGb,
		"Image":              kcmd.Image,
		"ImagePullPolicy":    kcmd.ImagePullPolicy,
		"NeedsPVC":           kcmd.StoragePlan == nil && kcmd.NeedsPVC,
		"NodeSelector":       kcmd.NodeSelector,
		"Tolerations":        kcmd.Tolerations,
		"ServiceAccountName": kcmd.ServiceAccount,
	}

	logger.Debug("Creating executor job from template", "template", kcmd.TaskTemplate, "data", templateData)
	var buf bytes.Buffer
	err = tpl.Execute(&buf, templateData)
	if err != nil {
		return &K8sSystemErr{
			Reason:  "TemplateExecutionFailed",
			Message: "Failed to execute task template",
			Err:     err,
		}
	}

	logger.Debug("Decoding job template", "template", buf.String())
	decode := scheme.Codecs.UniversalDeserializer().Decode
	obj, _, err := decode(buf.Bytes(), nil, nil)
	if err != nil {
		return &K8sSystemErr{
			Reason:  "JobCreationFailed",
			Message: "Failed to create Kubernetes job (check templates, RBAC, resources)",
			Err:     err,
		}
	}

	job, ok := obj.(*v1.Job)
	if !ok {
		return &K8sSystemErr{
			Reason:  "JobCreationFailed",
			Message: "Decoded object is not a Job",
			Err:     fmt.Errorf("decoded object is not a Job"),
		}
	}

	if kcmd.StoragePlan != nil {
		mounts := make([]corev1.VolumeMount, 0, len(kcmd.Volumes))
		for _, volume := range kcmd.Volumes {
			mounts = append(mounts, corev1.VolumeMount{MountPath: volume.ContainerPath, ReadOnly: volume.Readonly})
		}
		if err := kcmd.StoragePlan.ApplyExecutor(job, taskId, mounts); err != nil {
			return &K8sSystemErr{Reason: "StorageBindingFailed", Message: "Failed to bind executor storage", Err: err}
		}
	}

	containers := job.Spec.Template.Spec.Containers
	if len(containers) == 0 {
		return &K8sSystemErr{
			Reason:  "JobCreationFailed",
			Message: "Executor job requires a regular container",
			Err:     fmt.Errorf("executor job has no regular containers"),
		}
	}
	executorContainerName := containers[0].Name
	if kcmd.StoragePlan != nil && kcmd.StoragePlan.ExecutorContainer != "" {
		executorContainerName = kcmd.StoragePlan.ExecutorContainer
	}

	logger.Debug("Creating Kubernetes clientset")
	clientset := kcmd.Clientset
	if clientset == nil {
		logger.Debug("No Kubernetes clientset provided, creating in-cluster clientset")
		var err error

		clientset, err = getKubernetesClientset()
		if err != nil {
			return &K8sSystemErr{
				Reason:  "ClientsetCreationFailed",
				Message: "Failed to get Kubernetes clientset",
				Err:     err,
			}
		}
	}

	logger.Debug("Creating Kubernetes job", "jobName", job.Name, "namespace", kcmd.JobsNamespace)
	var client = clientset.BatchV1().Jobs(kcmd.JobsNamespace)
	_, err = client.Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		// If the executor job already exists, delete and recreate it. This allows us to restart the
		// whole task in case of worker job error, even if the executor job is not configured to
		// allow restarts.
		if err.Error() == "jobs.batch \""+job.Name+"\" already exists" {
			logger.Debug("Executor job already exists: recreating it", "jobName", job.Name)
			if derr := deleteJob(ctx, clientset, client, job.Name, kcmd.JobsNamespace); derr != nil {
				logger.Debug("failed to delete existing job before recreate", "jobName", job.Name, "error", derr)
			}
			_, err = client.Create(ctx, job, metav1.CreateOptions{})
			if err != nil {
				return &K8sSystemErr{
					Reason:  "JobCreationFailed",
					Message: "Failed to create Kubernetes job",
					Err:     err,
				}
			}
		} else {
			return &K8sSystemErr{
				Reason:  "JobCreationFailed",
				Message: "Failed to create Kubernetes job",
				Err:     err,
			}
		}
	}

	logger.Debug("Job created successfully, waiting for pod to finish", "jobName", job.Name)
	executorJobName := fmt.Sprintf("%s-%d", taskId, kcmd.JobId)
	podLabelSelector := fmt.Sprintf("job-name=%s", executorJobName)
	pod, err := waitForPodFinish(ctx, clientset, kcmd.JobsNamespace, podLabelSelector, executorContainerName)
	if err != nil {
		var sysErr *K8sSystemErr
		if errors.As(err, &sysErr) && slices.Contains(terminalWaitingReasons, sysErr.Reason) {
			pods, listErr := clientset.CoreV1().Pods(kcmd.JobsNamespace).List(context.Background(), metav1.ListOptions{
				LabelSelector: fmt.Sprintf("job-name=%s", executorJobName),
			})
			if listErr == nil {
				if events := k8sbackend.FetchPodWarningEvents(context.Background(), clientset, kcmd.JobsNamespace, pods); events != "" {
					sysErr.Message = sysErr.Message + "\n" + events
				}
			}
			return sysErr
		}
		return &K8sSystemErr{
			Reason:  "PodWaitFailed",
			Message: "Error waiting for pod to finish",
			Err:     err,
		}
	}

	logger.Debug("Streaming pod logs", "podName", pod.Name)
	err = streamPodLogs(ctx, clientset, kcmd.JobsNamespace, pod.Name, executorContainerName, kcmd.Stdout, kcmd.Stderr)
	if err != nil {
		if status := findContainerStatus(pod, executorContainerName); status != nil && status.State.Terminated != nil && kcmd.Event != nil {
			_ = kcmd.Event.ExitCode(int(status.State.Terminated.ExitCode))
		}
		return &K8sSystemErr{
			Reason:  "LogStreamingFailed",
			Message: fmt.Sprintf("Failed to stream logs from pod %s", pod.Name),
			Err:     err,
		}
	}

	if len(pod.Status.ContainerStatuses) == 0 {
		return &K8sSystemErr{
			Reason:  "NoContainerStatuses",
			Message: fmt.Sprintf("No container statuses found for pod %s", pod.Name),
			Err:     fmt.Errorf("no container statuses found"),
		}
	}

	cStatus := findContainerStatus(pod, executorContainerName)
	if cStatus == nil {
		return &K8sSystemErr{
			Reason:  "ExecutorContainerStatusMissing",
			Message: fmt.Sprintf("executor job %s: status for container %q not found", job.Name, executorContainerName),
			Err:     fmt.Errorf("executor container status not found"),
		}
	}
	if cStatus.State.Terminated == nil {
		return &K8sSystemErr{
			Reason:  "ContainerNotTerminated",
			Message: fmt.Sprintf("executor job %s: container not in terminated state", job.Name),
			Err:     fmt.Errorf("container not in terminated state"),
		}
	}

	exitCode := int(cStatus.State.Terminated.ExitCode)
	reason := cStatus.State.Terminated.Reason
	message := cStatus.State.Terminated.Message

	logger.Debug("Container terminated",
		"exitCode", exitCode,
		"reason", reason,
		"message", message,
		"jobName", job.Name)

	if exitCode != 0 {
		jobName := fmt.Sprintf("%s-%d", taskId, kcmd.JobId)
		return &K8sExecutorErr{
			ExitCode: exitCode,
			Reason:   reason,
			Message:  message,
			JobName:  jobName,
		}
	}

	return nil
}

// streamPodLogs streams logs from a pod regardless of its state
// This works for Running, Succeeded, and Failed pods (as long as they haven't been deleted)
func streamPodLogs(ctx context.Context, clientset kubernetes.Interface, namespace, podName, containerName string, stdout io.Writer, stderr io.Writer) error {
	// Get logs from any pod state - Kubernetes API supports fetching logs from terminated pods
	// Follow=true ensures we stream logs until the pod completely finishes (closes the stream),
	// catching the final error logs that might be missed due to race conditions.
	req := clientset.CoreV1().Pods(namespace).GetLogs(podName, &corev1.PodLogOptions{
		Container: containerName,
		Follow:    true,
	})

	podLogs, err := req.Stream(ctx)
	if err != nil {
		return fmt.Errorf("streaming logs from pod %s: %v", podName, err)
	}
	defer podLogs.Close()

	// K8s merges stdout and stderr in the stream unless specialized handling is used.
	// We write everything to stdout for now, as separating them reliably requires handling the Docker log format
	// or similar, which might depend on the runtime.
	// If the user provided a stderr writer, we could write to it, but writing the whole merged stream to both
	// would likely be duplicated or confusing.
	_, err = io.Copy(stdout, podLogs)
	return err
}

// Deletes the job running the task.
func (kcmd KubernetesCommand) Stop() error {
	clientset, err := getKubernetesClientset()
	if err != nil {
		return err
	}

	jobName := fmt.Sprintf("%s-%d", kcmd.TaskId, kcmd.JobId)

	backgroundDeletion := metav1.DeletePropagationBackground
	// Stop runs during worker cancellation, so the delete must not derive from
	// an already-canceled context.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err = clientset.BatchV1().Jobs(kcmd.JobsNamespace).Delete(ctx, jobName, metav1.DeleteOptions{
		PropagationPolicy: &backgroundDeletion,
	})

	if err != nil {
		return fmt.Errorf("deleting job: %v", err)
	}

	return nil
}

func (kcmd KubernetesCommand) GetStdout() io.Writer {
	return kcmd.Stdout
}

func (kcmd KubernetesCommand) GetStderr() io.Writer {
	return kcmd.Stderr
}

// terminalWaitingReasons are container waiting states that will never
// self-resolve, so the executor job should be failed immediately.
var terminalWaitingReasons = []string{
	"CreateContainerConfigError", // missing secret / configmap
	"InvalidImageName",           // malformed image reference
	"CreateContainerError",       // OCI runtime failed to create container
	"ErrImagePull",               // image not found or pull failed
	"ImagePullBackOff",           // repeated image pull failure
	"RunContainerError",          // runtime failed to start container (e.g. bad entrypoint)
	"StartError",                 // OCI runtime runc create failed
}

// waitForPodFinish watches pod events until the container terminates, a
// terminal waiting state is detected, or the context is cancelled.
//
// The Kubernetes API server closes long-lived watch connections after a
// (server-configured) timeout, which for long-running tasks routinely fires
// after ~20-30 minutes. When that happens the watch's result channel is
// closed, which is NOT an error — the watch must simply be re-established,
// resuming from the last observed resourceVersion. Failing to distinguish
// this case from a genuinely missing pod previously caused long-running tasks
// to be marked SYSTEM_ERROR ("received nil pod object from watcher").
func waitForPodFinish(ctx context.Context, clientset kubernetes.Interface, namespace, labelSelector, containerName string) (*corev1.Pod, error) {
	// wait up to 5 min for the pod to appear
	appearanceTimer := time.NewTimer(5 * 60 * time.Second)
	defer appearanceTimer.Stop()

	// resourceVersion tracks the last pod event we observed so a re-established
	// watch resumes where the previous one left off rather than replaying old
	// events or missing intervening ones.
	var resourceVersion string

	watcher, err := newPodWatcher(ctx, clientset, namespace, labelSelector, resourceVersion)
	if err != nil {
		return nil, &K8sSystemErr{
			Reason:  "PodWatcherCreationFailed",
			Message: "Failed to create pod watcher",
			Err:     err,
		}
	}
	defer func() { watcher.Stop() }()

	for {
		select {
		case event, ok := <-watcher.ResultChan():
			// A closed channel means the server ended the watch (e.g. the
			// periodic watch timeout). Re-establish the watch and continue
			// rather than treating this as a fatal error.
			if !ok {
				logger.Debug("pod watch channel closed; re-establishing watch", "resourceVersion", resourceVersion)
				watcher.Stop()
				watcher, err = newPodWatcher(ctx, clientset, namespace, labelSelector, resourceVersion)
				if err != nil {
					return nil, &K8sSystemErr{
						Reason:  "PodWatcherCreationFailed",
						Message: "Failed to re-establish pod watcher after watch timeout",
						Err:     err,
					}
				}
				continue
			}

			if event.Type == watch.Error {
				// A "too old resource version" error means we cannot resume
				// from our tracked version; restart the watch from scratch.
				if status, ok := event.Object.(*metav1.Status); ok {
					if status.Reason == metav1.StatusReasonExpired || status.Reason == metav1.StatusReasonGone {
						logger.Debug("pod watch resourceVersion expired; restarting watch from latest", "message", status.Message)
						resourceVersion = ""
						watcher.Stop()
						watcher, err = newPodWatcher(ctx, clientset, namespace, labelSelector, resourceVersion)
						if err != nil {
							return nil, &K8sSystemErr{
								Reason:  "PodWatcherCreationFailed",
								Message: "Failed to restart pod watcher after resourceVersion expiry",
								Err:     err,
							}
						}
						continue
					}
					return nil, fmt.Errorf("pod watch error: %s", status.Message)
				}
				return nil, fmt.Errorf("unknown pod watch error")
			}

			pod, ok := event.Object.(*corev1.Pod)
			if !ok {
				continue
			}

			// Track the latest resourceVersion so a re-established watch resumes
			// from here.
			resourceVersion = pod.ResourceVersion

			// Pod exists: stop the appearance timer
			appearanceTimer.Stop()

			podPhase := pod.Status.Phase
			logger.Debug("Pod status:", "podPhase", podPhase)

			executorStatus := findContainerStatus(pod, containerName)
			allStatuses := slices.Clone(pod.Status.InitContainerStatuses)
			if executorStatus != nil {
				allStatuses = append(allStatuses, *executorStatus)
			}
			for _, cs := range allStatuses {
				// A container stuck in a terminal waiting state will never start;
				// fail immediately rather than waiting for the job's backoff limit.
				if w := cs.State.Waiting; w != nil && slices.Contains(terminalWaitingReasons, w.Reason) {
					msg := w.Message
					if msg == "" {
						msg = w.Reason
					}
					return nil, &K8sSystemErr{
						Reason:  w.Reason,
						Message: fmt.Sprintf("executor pod has a terminal container waiting error: %s", msg),
					}
				}
			}
			for _, cs := range pod.Status.InitContainerStatuses {
				if terminated := cs.State.Terminated; terminated != nil && terminated.ExitCode != 0 {
					return nil, &K8sSystemErr{
						Reason:  "InitContainerFailed",
						Message: fmt.Sprintf("init container %q exited with code %d", cs.Name, terminated.ExitCode),
					}
				}
			}
			if executorStatus != nil && executorStatus.State.Terminated != nil {
				logger.Debug("Executor container has terminated", "container", containerName)
				return pod, nil
			}

			// Handle pod deletion
			if event.Type == watch.Deleted {
				logger.Debug("pod was deleted before container terminated")
				return nil, fmt.Errorf("pod was deleted before container terminated")
			}

		case <-appearanceTimer.C:
			return nil, fmt.Errorf("timed out waiting for pod to appear")

		case <-ctx.Done():
			logger.Debug("context cancelled while waiting for pod termination")
			return nil, fmt.Errorf("context cancelled while waiting for pod termination")
		}
	}
}

func findContainerStatus(pod *corev1.Pod, containerName string) *corev1.ContainerStatus {
	for i := range pod.Status.ContainerStatuses {
		if pod.Status.ContainerStatuses[i].Name == containerName {
			return &pod.Status.ContainerStatuses[i]
		}
	}
	return nil
}

// newPodWatcher establishes a watch on pods matching labelSelector. When
// resourceVersion is non-empty the watch resumes from that version, allowing a
// watch that was closed by the API server (e.g. the periodic watch timeout) to
// be transparently re-established without replaying or missing events.
func newPodWatcher(ctx context.Context, clientset kubernetes.Interface, namespace, labelSelector, resourceVersion string) (watch.Interface, error) {
	return clientset.CoreV1().Pods(namespace).Watch(ctx, metav1.ListOptions{
		LabelSelector:   labelSelector,
		ResourceVersion: resourceVersion,
	})
}

// Deletes a job and waits for it to be deleted
func deleteJob(ctx context.Context, clientset kubernetes.Interface, client batchv1.JobInterface, jobName, namespace string) error {
	// delete the job
	var gracePeriod int64 = 0
	var prop metav1.DeletionPropagation = metav1.DeletePropagationForeground
	err := client.Delete(ctx, jobName, metav1.DeleteOptions{
		GracePeriodSeconds: &gracePeriod,
		PropagationPolicy:  &prop,
	})
	if err != nil {
		return &K8sSystemErr{
			Reason:  "JobDeletionFailed",
			Message: "Failed to delete job",
			Err:     err,
		}
	}

	// wait for a "deleted" event
	watcher, err := clientset.BatchV1().Jobs(namespace).Watch(ctx, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("metadata.name=%s", jobName),
	})
	if err != nil {
		return err
	}
	defer watcher.Stop()
	for event := range watcher.ResultChan() {
		if event.Type == watch.Deleted {
			logger.Debug("Job deleted successfully", "jobName", jobName)
			return nil
		}
	}

	return fmt.Errorf("timed out waiting for job deletion")
}

func getKubernetesClientset() (*kubernetes.Clientset, error) {
	kubeconfig, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}

	clientset, err := kubernetes.NewForConfig(kubeconfig)
	return clientset, err
}

// newKubernetesCommand validates and applies the executor resource limits,
// publishes the effective resources as an event, and assembles the
// KubernetesCommand for one executor of the task.
func (r *DefaultWorker) newKubernetesCommand(ctx context.Context, event *events.TaskWriter, task *tes.Task, d *tes.Executor, i int, command Command, resources *tes.Resources) (*KubernetesCommand, error) {
	pullPolicy := corev1.PullAlways
	if value := task.Tags["_IMAGE_PULL_POLICY"]; value != "" {
		pullPolicy = corev1.PullPolicy(value)
		switch pullPolicy {
		case corev1.PullAlways, corev1.PullIfNotPresent, corev1.PullNever:
		default:
			return nil, fmt.Errorf("invalid _IMAGE_PULL_POLICY %q: expected Always, IfNotPresent, or Never", value)
		}
	}

	resources, err := config.ValidateResources(resources, r.Executor.Resources)
	if err != nil {
		return nil, err
	}

	// Store the effective resources back to the task
	task.Resources = resources

	resourceLimits := config.GetResourceLimits(r.Executor.Resources)

	// Best-effort: the resources event is informational.
	_ = r.EventWriter.WriteEvent(ctx, events.NewResources(task.Id, task.Resources))

	kcmd := &KubernetesCommand{
		StoragePlan:     r.Executor.StoragePlan,
		TaskId:          task.Id,
		JobId:           i,
		StdinFile:       d.Stdin,
		StdoutFile:      d.Stdout,
		StderrFile:      d.Stderr,
		TaskTemplate:    r.Executor.Template,
		Namespace:       r.Executor.Namespace,
		JobsNamespace:   r.Executor.JobsNamespace,
		Resources:       resources,
		ResourceLimits:  resourceLimits,
		Command:         command,
		NeedsPVC:        len(task.GetInputs()) > 0 || len(task.GetOutputs()) > 0 || len(task.GetVolumes()) > 0,
		NodeSelector:    r.Executor.NodeSelector,
		Tolerations:     r.Executor.Tolerations,
		ServiceAccount:  fmt.Sprintf("funnel-worker-sa-%s-%s", r.Executor.JobsNamespace, task.Id),
		ImagePullPolicy: pullPolicy,
	}

	// Override ServiceAccountName if provided in Task Tags
	if saName, exists := task.Tags["_WORKER_SA"]; exists && saName != "" {
		kcmd.ServiceAccount = saName
	}

	return kcmd, nil
}
