package resources

import (
	"bytes"
	"context"
	"fmt"
	"html/template"

	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/logger"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
)

// Create the Worker/Executor PV from config/kubernetes-pv.yaml
func CreatePV(taskId string, config *config.Config, client kubernetes.Interface, log *logger.Logger) error {

	// Load templates
	t, err := template.New(taskId).Parse(config.Kubernetes.PVTemplate)
	if err != nil {
		return fmt.Errorf("parsing template: %v", err)
	}

	// Template parameters
	var buf bytes.Buffer
	err = t.Execute(&buf, map[string]interface{}{
		"TaskId":    taskId,
		"Namespace": config.Kubernetes.JobsNamespace,
		"Bucket":    config.GenericS3[0].Bucket,
		"Region":    config.GenericS3[0].Region,
		"KmsKeyID":  config.GenericS3[0].KmsKeyID,
	})
	if err != nil {
		return fmt.Errorf("%v", err)
	}

	decode := scheme.Codecs.UniversalDeserializer().Decode
	obj, _, err := decode(buf.Bytes(), nil, nil)
	if err != nil {
		return fmt.Errorf("%v", err)
	}

	pv, ok := obj.(*corev1.PersistentVolume)
	if !ok {
		return fmt.Errorf("failed to decode PV spec")
	}

	_, err = client.CoreV1().PersistentVolumes().Create(context.Background(), pv, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("%v", err)
	}

	return nil
}

// Add this helper function for PV cleanup
func DeletePV(ctx context.Context, taskID string, client kubernetes.Interface, log *logger.Logger) error {
	name := fmt.Sprintf("funnel-worker-pv-%s", taskID)
	// The PV may not have been made. Some jobs with no I/O don't need a PV or it may have already been deleted.
	_, err := client.CoreV1().PersistentVolumes().Get(ctx, name, metav1.GetOptions{})
	if err == nil {
		log.Debug("deleting Worker PV", "taskID", taskID)
		err := client.CoreV1().PersistentVolumes().Delete(ctx, name, metav1.DeleteOptions{})
		if err != nil {
			return fmt.Errorf("%v", err)
		}
	}
	return nil
}
