package resources

import (
	"bytes"
	"context"
	"fmt"
	"html/template"

	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/logger"
	"github.com/ohsu-comp-bio/funnel/tes"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
)

// Create the Worker/Executor ServiceAccount from config/kubernetes-serviceaccount.yaml
func CreateServiceAccount(task *tes.Task, config *config.Config, client kubernetes.Interface, log *logger.Logger) error {

	// Load templates
	t, err := template.New(task.Id).Parse(config.Kubernetes.ServiceAccountTemplate)
	if err != nil {
		return fmt.Errorf("parsing template: %v", err)
	}

	// Template parameters
	// TODO: Handle cases where values/tags below are not supplied
	var buf bytes.Buffer
	templateData := map[string]interface{}{
		"TaskId":             task.Id,
		"Namespace":          config.Kubernetes.JobsNamespace,
		"ServiceAccountName": fmt.Sprintf("funnel-worker-sa-%s-%s", config.Kubernetes.JobsNamespace, task.Id),
	}

	// Override ServiceAccountName if provided in Task Tags
	if saName, exists := task.Tags["_WORKER_SA"]; exists && saName != "" {
		templateData["ServiceAccountName"] = saName
	}

	// Only include IamRoleArn if provided
	if roleArn, exists := task.Tags["_FUNNEL_WORKER_ROLE_ARN"]; exists && roleArn != "" {
		templateData["IamRoleArn"] = roleArn
	}

	err = t.Execute(&buf, templateData)
	if err != nil {
		return fmt.Errorf("%v", err)
	}

	decode := scheme.Codecs.UniversalDeserializer().Decode
	obj, _, err := decode(buf.Bytes(), nil, nil)
	if err != nil {
		return fmt.Errorf("failed to decode ServiceAccount spec: %v", err)
	}

	sa, ok := obj.(*corev1.ServiceAccount)
	if !ok {
		return fmt.Errorf("failed to decode ServiceAccount spec")
	}

	_, err = client.CoreV1().ServiceAccounts(config.Kubernetes.JobsNamespace).Create(context.Background(), sa, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create ServiceAccount: %v", err)
	}

	return nil
}

// Add this helper function for ServiceAccount cleanup
func DeleteServiceAccount(ctx context.Context, taskID string, client kubernetes.Interface, log *logger.Logger) error {
	// TODO: Implement deletion of ServiceAccounts
	return nil
}
