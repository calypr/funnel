package resources

import (
	"context"
	"fmt"

	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/logger"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

func CreateConfigMap(taskId string, conf *config.Config, client kubernetes.Interface, log *logger.Logger) error {
	configBytes, err := config.ToYaml(conf)
	if err != nil {
		return fmt.Errorf("marshaling config to ConfigMap: %v", err)
	}

	// Create the ConfigMap that will contain the Funnel Worker Config (`funnel-worker.yaml`)
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("funnel-worker-config-%s", taskId),
			Namespace: conf.Kubernetes.JobsNamespace,
		},
		Data: map[string]string{
			"funnel-worker.yaml": string(configBytes),
		},
	}

	_, err = client.CoreV1().ConfigMaps(conf.Kubernetes.JobsNamespace).Create(context.Background(), cm, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("%v", err)
	}

	return nil
}

func DeleteConfigMap(ctx context.Context, taskId string, namespace string, client kubernetes.Interface, log *logger.Logger) error {
	name := fmt.Sprintf("funnel-worker-config-%s", taskId)
	cfg, _ := client.CoreV1().ConfigMaps(namespace).Get(ctx, name, metav1.GetOptions{})
	if cfg != nil {
		log.Debug("deleting Worker configMap", "taskID", taskId)
		err := client.CoreV1().ConfigMaps(namespace).Delete(ctx, name, metav1.DeleteOptions{})
		if err != nil {
			return fmt.Errorf("%v", err)
		}
	}
	return nil
}
