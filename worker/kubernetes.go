package worker

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"text/template"

	"github.com/ohsu-comp-bio/funnel/logger"
	"github.com/ohsu-comp-bio/funnel/tes"
	v1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
)

// KubernetesCommand is responsible for configuring and running a task in a Kubernetes cluster.
type KubernetesCommand struct {
	TaskId         string
	JobId          int
	StdinFile      string
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

// Utility function to correctly handle tasks with o/quotes in commands
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "\"" + strings.ReplaceAll(s, "'", `\\'`) + "\""
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

	if kcmd.StdinFile != "" {
		cmd = append(cmd, "<", kcmd.StdinFile)
	}

	for i, v := range cmd {
		if strings.Contains(v, " ") {
			cmd[i] = shellQuote(v)
		}
	}

	templateData := map[string]interface{}{
		"TaskId":             taskId,
		"JobId":              kcmd.JobId,
		"Namespace":          kcmd.Namespace,
		"JobsNamespace":      kcmd.JobsNamespace,
		"Command":            cmd,
		"Workdir":            kcmd.Workdir,
		"Volumes":            kcmd.Volumes,
		"Cpus":               kcmd.Resources.CpuCores,
		"RamGb":              kcmd.Resources.RamGb,
		"DiskGb":             kcmd.Resources.DiskGb,
		"CpusLimit":          kcmd.ResourceLimits.CpuCores,
		"RamGbLimit":         kcmd.ResourceLimits.RamGb,
		"DiskGbLimit":        kcmd.ResourceLimits.DiskGb,
		"Image":              kcmd.Image,
		"NeedsPVC":           kcmd.NeedsPVC,
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
		return &K8sSystemErr{
			Reason:  "JobCreationFailed",
			Message: "Failed to create Kubernetes job",
			Err:     err,
		}
	}

	logger.Debug("Job created successfully, waiting for pod to be running", "jobName", job.Name)
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

	logger.Debug("Waiting for pod to finish", "jobName", job.Name)
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
	for {
		select {
		case event := <-watcher.ResultChan():
			if event.Object == nil {
				return nil, fmt.Errorf("received nil pod object from watcher")
			}

			pod, ok := event.Object.(*corev1.Pod)
			if !ok {
				continue
			}

			// Check if container is terminated
			if len(pod.Status.ContainerStatuses) > 0 {
				cStatus := pod.Status.ContainerStatuses[0]
				if cStatus.State.Terminated != nil {
					return pod, nil
				}
			}

			// Handle pod deletion
			if event.Type == watch.Deleted {
				return nil, fmt.Errorf("pod was deleted before container terminated")
			}

		case <-ctx.Done():
			return nil, fmt.Errorf("context cancelled while waiting for pod termination")
		}
	}
}

func getKubernetesClientset() (*kubernetes.Clientset, error) {
	kubeconfig, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}

	clientset, err := kubernetes.NewForConfig(kubeconfig)
	return clientset, err
}
