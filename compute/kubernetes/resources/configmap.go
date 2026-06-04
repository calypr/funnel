package resources

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"text/template"

	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/logger"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
)

// CreateConfigMap creates a per-task ConfigMap by rendering ConfigMapTemplate.
// The template receives the same variables available to WorkerTemplate so that
// operators can fully control the ConfigMap name, namespace, and data.
// This function is only called when ConfigMapTemplate is non-empty; deployments
// that mount a static shared ConfigMap (e.g. "funnel-config") via the
// WorkerTemplate volume spec should leave ConfigMapTemplate empty.
func CreateConfigMap(ctx context.Context, taskId string, conf *config.Config, client kubernetes.Interface, log *logger.Logger, ownerRef *metav1.OwnerReference) error {
	funcMap := template.FuncMap{
		"indent": func(spaces int, s string) string {
			pad := strings.Repeat(" ", spaces)
			return pad + strings.ReplaceAll(s, "\n", "\n"+pad)
		},
	}

	t, err := template.New(taskId).Funcs(funcMap).Parse(conf.Kubernetes.ConfigMapTemplate)

	if err != nil {
		return fmt.Errorf("parsing ConfigMapTemplate: %v", err)
	}

	configBytes, err := config.ToYaml(conf)
	if err != nil {
		return fmt.Errorf("marshaling config to YAML: %v", err)
	}

	var buf bytes.Buffer
	err = t.Execute(&buf, map[string]interface{}{
		"TaskId":    taskId,
		"Namespace": conf.Kubernetes.JobsNamespace,
		"Config":    string(configBytes),
	})
	if err != nil {
		return fmt.Errorf("executing ConfigMapTemplate: %v", err)
	}
	log.Debug("rendered ConfigMap template", "taskID", taskId, "configMap", buf.String())
	decode := scheme.Codecs.UniversalDeserializer().Decode
	obj, _, err := decode(buf.Bytes(), nil, nil)
	if err != nil {
		return fmt.Errorf("decoding ConfigMap spec: %v", err)
	}

	cm, ok := obj.(*corev1.ConfigMap)
	if !ok {
		return fmt.Errorf("ConfigMapTemplate did not produce a ConfigMap object")
	}

	if ownerRef != nil {
		cm.OwnerReferences = []metav1.OwnerReference{*ownerRef}
	}

	_, err = client.CoreV1().ConfigMaps(conf.Kubernetes.JobsNamespace).Create(ctx, cm, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("%v", err)
	}

	return nil
}

// DeleteConfigMap deletes the per-task ConfigMap created by CreateConfigMap.
// The name must match the metadata.name set in ConfigMapTemplate.
func DeleteConfigMap(ctx context.Context, taskId string, namespace string, client kubernetes.Interface, log *logger.Logger) error {
	name := fmt.Sprintf("funnel-worker-config-%s", taskId)
	log.Debug("deleting Worker configMap", "taskID", taskId)
	err := client.CoreV1().ConfigMaps(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("%v", err)
	}
	return nil
}
