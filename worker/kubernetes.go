package worker

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"text/template"
	"time"

	"github.com/ohsu-comp-bio/funnel/logger"
	"github.com/ohsu-comp-bio/funnel/tes"
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
	TaskId         string
	JobId          int
	StdinFile      string
	StdoutFile     string
	StderrFile     string
	TaskTemplate   string
	Namespace      string // Funnel Server Namespace
	JobsNamespace  string // Funnel Worker + Executor Namespace (default: Namespace)
	NodeSelector   map[string]string
	Tolerations    []map[string]interface{}
	Resources      *tes.Resources
	ResourceLimits *tes.Resources
	ServiceAccount string
	NeedsPVC       bool
	Clientset      kubernetes.Interface
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
	return fmt.Sprintf("executor job %s failed with exit code %d (%s): %s",
		e.JobName, e.ExitCode, e.Reason, e.Message)
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
		"NeedsPVC":           kcmd.NeedsPVC,
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

	logger.Debug("Creating Kubernetes clientset", "clientset", kcmd.Clientset)
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
			deleteJob(ctx, clientset, client, job.Name, kcmd.JobsNamespace)
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
	podWatcher, err := clientset.CoreV1().Pods(kcmd.JobsNamespace).Watch(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("job-name=%s-%d", taskId, kcmd.JobId),
	})
	if err != nil {
		return &K8sSystemErr{
			Reason:  "PodWatcherCreationFailed",
			Message: "Failed to create pod watcher",
			Err:     err,
		}
	}
	defer podWatcher.Stop()
	pod, err := waitForPodFinish(ctx, podWatcher)
	if err != nil {
		return &K8sSystemErr{
			Reason:  "PodWaitFailed",
			Message: "Error waiting for pod to finish",
			Err:     err,
		}
	}

	logger.Debug("Streaming pod logs", "podName", pod.Name)
	err = streamPodLogs(ctx, kcmd.JobsNamespace, pod.Name, kcmd.Stdout, kcmd.Stderr)
	if err != nil {
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

	// TODO: Review effects (e.g. does this cover all Executors?)
	cStatus := pod.Status.ContainerStatuses[0]
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
func streamPodLogs(ctx context.Context, namespace string, podName string, stdout io.Writer, stderr io.Writer) error {
	clientset, err := getKubernetesClientset()
	if err != nil {
		return fmt.Errorf("getting kubernetes clientset: %v", err)
	}

	// Get logs from any pod state - Kubernetes API supports fetching logs from terminated pods
	// Follow=true ensures we stream logs until the pod completely finishes (closes the stream),
	// catching the final error logs that might be missed due to race conditions.
	req := clientset.CoreV1().Pods(namespace).GetLogs(podName, &corev1.PodLogOptions{
		Follow: true,
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
	err = clientset.BatchV1().Jobs(kcmd.JobsNamespace).Delete(context.TODO(), jobName, metav1.DeleteOptions{
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

// Waits until the job finishes
func waitForPodFinish(ctx context.Context, watcher watch.Interface) (*corev1.Pod, error) {
	// wait up to 5 min for the pod to appear
	appearanceTimer := time.NewTimer(5 * 60 * time.Second)
	defer appearanceTimer.Stop()

	for {
		select {
		case event := <-watcher.ResultChan():
			if event.Type == watch.Error {
				if status, ok := event.Object.(*metav1.Status); ok {
					return nil, fmt.Errorf("pod watch error: %s", status.Message)
				}
				return nil, fmt.Errorf("unknown pod watch error")
			}

			if event.Object == nil { // no pod; watcher times out
				logger.Debug("received nil pod object from watcher")
				return nil, fmt.Errorf("received nil pod object from watcher")
			}

			pod, ok := event.Object.(*corev1.Pod)
			if !ok {
				continue
			}

			// Pod exists: stop the appearance timer
			appearanceTimer.Stop()

			// Check if container is terminated
			podPhase := pod.Status.Phase
			logger.Debug("Pod status:", "podPhase", podPhase)
			if len(pod.Status.ContainerStatuses) > 0 {
				cStatus := pod.Status.ContainerStatuses[0]
				if cStatus.State.Terminated != nil {
					logger.Debug("Container has terminated")
					return pod, nil
				}
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
