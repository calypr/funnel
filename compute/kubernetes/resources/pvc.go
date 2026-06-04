package resources

import (
	"bytes"
	"context"
	"fmt"
	"text/template"
	"time"

	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/logger"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
)

// Create the Worker/Executor PVC from config/kubernetes-pvc.yaml
// TODO: Move this config file to Helm Charts so users can see/customize it
func CreatePVC(ctx context.Context, taskId string, conf *config.Config, client kubernetes.Interface, log *logger.Logger, ownerRef *metav1.OwnerReference) error {

	jobNamespace := conf.Kubernetes.JobsNamespace

	// Load templates
	t, err := template.New(taskId).Parse(conf.Kubernetes.PVCTemplate)
	if err != nil {
		return fmt.Errorf("parsing template: %v", err)
	}

	// Template parameters
	var s3Bucket, s3Region string
	if len(conf.GenericS3) > 0 {
		s3Bucket = conf.GenericS3[0].Bucket
		s3Region = conf.GenericS3[0].Region
	}

	var buf bytes.Buffer
	err = t.Execute(&buf, map[string]interface{}{
		"TaskId":    taskId,
		"Namespace": jobNamespace,
		"Bucket":    s3Bucket,
		"Region":    s3Region,
	})
	if err != nil {
		return fmt.Errorf("%v", err)
	}

	decode := scheme.Codecs.UniversalDeserializer().Decode
	obj, _, err := decode(buf.Bytes(), nil, nil)
	if err != nil {
		return fmt.Errorf("%v", err)
	}

	pvc, ok := obj.(*corev1.PersistentVolumeClaim)
	if !ok {
		return fmt.Errorf("failed to decode PVC spec")
	}

	if ownerRef != nil {
		pvc.OwnerReferences = []metav1.OwnerReference{*ownerRef}
	}

	_, err = client.CoreV1().PersistentVolumeClaims(jobNamespace).Create(ctx, pvc, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("%v", err)
	}

	return nil
}

// DeletePVC removes the PVC for a task, retrying on conflict errors that occur
// when another process (e.g. reconciler + cancel running concurrently) modifies
// the PVC between our Get and Update calls.
func DeletePVC(ctx context.Context, taskID string, namespace string, client kubernetes.Interface, log *logger.Logger) error {
	name := fmt.Sprintf("funnel-worker-pvc-%s", taskID)

	const maxRetries = 5
	delay := 100 * time.Millisecond
	for i := range maxRetries {
		// The PVC may not exist (no I/O task, or already deleted).
		pvc, err := client.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				return nil
			}
			return fmt.Errorf("getting PVC %s: %v", name, err)
		}

		// Remove the pvc-protection finalizer so Kubernetes allows deletion.
		if len(pvc.Finalizers) > 0 {
			pvc.Finalizers = nil
			_, err = client.CoreV1().PersistentVolumeClaims(namespace).Update(ctx, pvc, metav1.UpdateOptions{})
			if err != nil {
				if errors.IsConflict(err) && i < maxRetries-1 {
					log.Debug("conflict removing PVC finalizers, retrying", "pvc", name, "attempt", i+1)
					time.Sleep(delay)
					delay *= 2
					continue
				}
				return fmt.Errorf("removing finalizers from PVC %s: %v", name, err)
			}
		}

		log.Debug("deleting Worker PVC", "taskID", taskID)
		err = client.CoreV1().PersistentVolumeClaims(namespace).Delete(ctx, name, metav1.DeleteOptions{})
		if err != nil && !errors.IsNotFound(err) {
			return fmt.Errorf("deleting PVC %s: %v", name, err)
		}
		return nil
	}

	return fmt.Errorf("removing finalizers from PVC %s: exceeded max retries", name)
}
