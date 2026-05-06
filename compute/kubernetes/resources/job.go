package resources

import (
	"bytes"
	"context"
	"fmt"
	"html/template"

	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/logger"
	"github.com/ohsu-comp-bio/funnel/tes"
	v1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
)

// Create the Funnel Worker job from kubernetes-template.yaml
// Executor job is created in worker/kubernetes.go#Run
func CreateJob(task *tes.Task, config *config.Config, client kubernetes.Interface, log *logger.Logger) error {
	// Parse Worker Template

	log.Debug("Creating job from template", "template", config.Kubernetes.WorkerTemplate)
	t, err := template.New(task.Id).Parse(config.Kubernetes.WorkerTemplate)
	if err != nil {
		return fmt.Errorf("%v", err)
	}

	pods, err := client.CoreV1().Pods(config.Kubernetes.Namespace).List(context.Background(), metav1.ListOptions{
		LabelSelector: "app=funnel",
	})
	if err != nil {
		return fmt.Errorf("failed to list pods: %v", err)
	}

	res := task.GetResources()
	if res == nil {
		res = &tes.Resources{}
	}

	templateData := map[string]interface{}{
		"TaskId":             task.Id,
		"Namespace":          config.Kubernetes.Namespace,
		"JobsNamespace":      config.Kubernetes.JobsNamespace,
		"Cpus":               res.GetCpuCores(),
		"RamGb":              res.GetRamGb(),
		"DiskGb":             res.GetDiskGb(),
		"Image":              pods.Items[0].Spec.Containers[0].Image,
		"NeedsPVC":           len(task.Inputs) > 0 || len(task.Outputs) > 0,
		"NodeSelector":       config.Kubernetes.NodeSelector,
		"Tolerations":        config.Kubernetes.Tolerations,
		"ServiceAccountName": fmt.Sprintf("funnel-worker-sa-%s-%s", config.Kubernetes.JobsNamespace, task.Id),
	}

	// Override ServiceAccountName if provided in Task Tags
	if saName, exists := task.Tags["_WORKER_SA"]; exists && saName != "" {
		templateData["ServiceAccountName"] = saName
	}

	var buf bytes.Buffer
	err = t.Execute(&buf, templateData)
	if err != nil {
		return fmt.Errorf("%v", err)
	}

	log.Debug("Job template", "template", buf.String())
	decode := scheme.Codecs.UniversalDeserializer().Decode
	obj, _, err := decode(buf.Bytes(), nil, nil)
	if err != nil {
		return err
	}

	job, ok := obj.(*v1.Job)
	if !ok {
		return fmt.Errorf("failed to decode job spec")
	}

	log.Debug("Creating job", "Job", job.Name, "JobsNamespace", config.Kubernetes.JobsNamespace)
	_, err = client.BatchV1().Jobs(config.Kubernetes.JobsNamespace).Create(context.Background(), job, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("%v", err)
	}

	return nil
}

// deleteJob removes deletes a kubernetes v1/batch job.
func DeleteJob(ctx context.Context, conf *config.Config, taskID string, client kubernetes.Interface, log *logger.Logger) error {

	jobsInterface := client.BatchV1().Jobs(conf.Kubernetes.JobsNamespace)

	var gracePeriod int64 = 0
	var prop metav1.DeletionPropagation = metav1.DeletePropagationForeground

	err := jobsInterface.Delete(ctx, taskID, metav1.DeleteOptions{
		GracePeriodSeconds: &gracePeriod,
		PropagationPolicy:  &prop,
	})

	if err != nil {
		return fmt.Errorf("%v", err)
	}

	return nil
}
