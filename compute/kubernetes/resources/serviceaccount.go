package resources

import (
	"bytes"
	"context"
	"fmt"
	"text/template"

	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/logger"
	"github.com/ohsu-comp-bio/funnel/tes"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
)

// Create the Worker/Executor ServiceAccount from config/kubernetes-serviceaccount.yaml
func CreateServiceAccount(ctx context.Context, task *tes.Task, conf *config.Config, client kubernetes.Interface, log *logger.Logger, ownerRef *metav1.OwnerReference) error {

	// Load templates
	t, err := template.New(task.Id).Parse(conf.Kubernetes.ServiceAccountTemplate)
	if err != nil {
		return fmt.Errorf("parsing template: %v", err)
	}

	// Template parameters
	// TODO: Handle cases where values/tags below are not supplied
	var buf bytes.Buffer
	templateData := map[string]interface{}{
		"TaskId":             task.Id,
		"Namespace":          conf.Kubernetes.JobsNamespace,
		"ServiceAccountName": fmt.Sprintf("funnel-worker-sa-%s-%s", conf.Kubernetes.JobsNamespace, task.Id),
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
		return fmt.Errorf("failed to cast to ServiceAccount spec")
	}

	if ownerRef != nil {
		sa.OwnerReferences = []metav1.OwnerReference{*ownerRef}
	}

	_, err = client.CoreV1().ServiceAccounts(conf.Kubernetes.JobsNamespace).Create(ctx, sa, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create ServiceAccount: %v", err)
	}

	return nil
}

// isServiceAccountAttachedToPods returns true as soon as it finds one active
// (non-terminating) pod using the given ServiceAccount.
func isServiceAccountAttachedToPods(ctx context.Context, saName, namespace string, client kubernetes.Interface) (bool, error) {
	pods, err := client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("spec.serviceAccountName=%s", saName),
	})
	if err != nil {
		return false, fmt.Errorf("listing pods using ServiceAccount %s: %v", saName, err)
	}
	for _, pod := range pods.Items {
		if pod.DeletionTimestamp == nil {
			return true, nil // early return on first active pod
		}
	}
	return false, nil
}

type DeleteServiceAccountOptions struct {
	// ServiceAccountName overrides the default derived SA name (funnel-worker-$namespace-$taskID).
	// Required when the SA is externally managed (e.g. a Gen3Workflow per-user SA).
	ServiceAccountName string

	// SharedSA indicates the SA is shared across tasks. When true, deletion is
	// skipped if any other active pods are still using it.
	SharedSA bool
}

// DeleteServiceAccount deletes the ServiceAccount for a task.
//
// By default (no options), it targets the conventional SA name
// funnel-worker-$namespace-$taskID and deletes it unconditionally.
//
// If opts.SharedSA is true, the SA is treated as shared — deletion is skipped
// if any active pod is still using it, and the reconciler will retry on the
// next pass when the last pod finishes.
func DeleteServiceAccount(ctx context.Context, taskID, namespace string, client kubernetes.Interface, log *logger.Logger, opts *DeleteServiceAccountOptions) error {
	saName := fmt.Sprintf("funnel-worker-%s-%s", namespace, taskID)
	sharedSA := false

	if opts != nil {
		if opts.ServiceAccountName != "" {
			saName = opts.ServiceAccountName
		}
		sharedSA = opts.SharedSA
	}

	if sharedSA {
		inUse, err := isServiceAccountAttachedToPods(ctx, saName, namespace, client)
		if err != nil {
			return fmt.Errorf("checking pod attachment for ServiceAccount %s: %v", saName, err)
		}
		if inUse {
			log.Debug("skipping ServiceAccount deletion — still in use; reconciler will retry", "name", saName, "taskID", taskID)
			return nil
		}
	}

	log.Debug("deleting Worker ServiceAccount", "name", saName, "taskID", taskID)
	if err := client.CoreV1().ServiceAccounts(namespace).Delete(ctx, saName, metav1.DeleteOptions{}); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("deleting ServiceAccount %s: %v", saName, err)
	}
	return nil
}
