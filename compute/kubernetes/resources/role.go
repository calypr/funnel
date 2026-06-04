package resources

import (
	"bytes"
	"context"
	"fmt"
	"text/template"

	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/logger"
	"github.com/ohsu-comp-bio/funnel/tes"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
)

// Create the Worker/Executor Role from config/kubernetes-role.yaml
func CreateRole(ctx context.Context, task *tes.Task, conf *config.Config, client kubernetes.Interface, log *logger.Logger, ownerRef *metav1.OwnerReference) error {

	// Load templates
	t, err := template.New(task.Id).Parse(conf.Kubernetes.RoleTemplate)
	if err != nil {
		return fmt.Errorf("parsing template: %v", err)
	}

	// Template parameters
	// TODO: Handle cases where values/tags below are not supplied
	var buf bytes.Buffer
	err = t.Execute(&buf, map[string]interface{}{
		"TaskId":    task.Id,
		"Namespace": conf.Kubernetes.JobsNamespace,
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

	if ownerRef != nil {
		role.OwnerReferences = []metav1.OwnerReference{*ownerRef}
	}

	_, err = client.RbacV1().Roles(conf.Kubernetes.JobsNamespace).Create(ctx, role, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create Role: %v", err)
	}

	return nil
}

// DeleteRole deletes the Role created for a task.
func DeleteRole(ctx context.Context, taskID string, namespace string, client kubernetes.Interface, log *logger.Logger) error {
	roles, err := client.RbacV1().Roles(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("app=funnel,taskId=%s", taskID),
	})
	if err != nil {
		return fmt.Errorf("listing Roles for task %s: %v", taskID, err)
	}
	for _, role := range roles.Items {
		log.Debug("deleting Worker Role", "name", role.Name, "taskID", taskID)
		if err := client.RbacV1().Roles(namespace).Delete(ctx, role.Name, metav1.DeleteOptions{}); err != nil {
			return fmt.Errorf("deleting Role %s: %v", role.Name, err)
		}
	}
	return nil
}
