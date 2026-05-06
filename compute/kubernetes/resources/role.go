package resources

import (
	"bytes"
	"context"
	"fmt"
	"html/template"

	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/logger"
	"github.com/ohsu-comp-bio/funnel/tes"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
)

// Create the Worker/Executor Role from config/kubernetes-role.yaml
func CreateRole(task *tes.Task, config *config.Config, client kubernetes.Interface, log *logger.Logger) error {

	// Load templates
	t, err := template.New(task.Id).Parse(config.Kubernetes.RoleTemplate)
	if err != nil {
		return fmt.Errorf("parsing template: %v", err)
	}

	// Template parameters
	// TODO: Handle cases where values/tags below are not supplied
	var buf bytes.Buffer
	err = t.Execute(&buf, map[string]interface{}{
		"TaskId":    task.Id,
		"Namespace": config.Kubernetes.JobsNamespace,
	})
	if err != nil {
		return fmt.Errorf("%v", err)
	}

	decode := scheme.Codecs.UniversalDeserializer().Decode
	obj, _, err := decode(buf.Bytes(), nil, nil)
	if err != nil {
		return fmt.Errorf("failed to decode Role spec: %v", err)
	}

	role, ok := obj.(*rbacv1.Role)
	if !ok {
		return fmt.Errorf("failed to verify Role spec")
	}

	_, err = client.RbacV1().Roles(config.Kubernetes.JobsNamespace).Create(context.Background(), role, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create Role: %v", err)
	}

	return nil
}

// Add this helper function for Role cleanup
func DeleteRole(ctx context.Context, taskID string, client kubernetes.Interface, log *logger.Logger) error {
	// TODO: Implement deletion of Roles
	return nil
}
