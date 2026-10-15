package resources

import (
	"context"
	"fmt"
	"testing"

	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/logger"
	"github.com/ohsu-comp-bio/funnel/tes"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

const workerImageTemplate = `apiVersion: batch/v1
kind: Job
metadata:
  name: {{.TaskId}}
  namespace: {{.JobsNamespace}}
spec:
  template:
    spec:
      restartPolicy: OnFailure
      containers:
      - name: worker
        image: {{.Image}}
`

func TestCreateJobUsesConfiguredWorkerImageWithoutListingPods(t *testing.T) {
	conf := config.DefaultConfig()
	conf.Kubernetes.Namespace = namespace
	conf.Kubernetes.JobsNamespace = jobsNamespace
	conf.Kubernetes.WorkerTemplate = workerImageTemplate
	conf.Kubernetes.WorkerImage = "funnel-worker:v2"

	client := fake.NewSimpleClientset()
	listedPods := false
	client.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		listedPods = true
		return true, nil, fmt.Errorf("unexpected pod list")
	})

	job, err := CreateJob(context.Background(), &tes.Task{Id: testTaskID}, conf, client, logger.NewLogger("test", logger.DefaultConfig()), nil)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if listedPods {
		t.Fatal("CreateJob listed server pods despite WorkerImage being configured")
	}
	if got := job.Spec.Template.Spec.Containers[0].Image; got != conf.Kubernetes.WorkerImage {
		t.Fatalf("worker image = %q, want %q", got, conf.Kubernetes.WorkerImage)
	}
}

func TestCreateJobDiscoversWorkerImageWhenUnset(t *testing.T) {
	conf := config.DefaultConfig()
	conf.Kubernetes.Namespace = namespace
	conf.Kubernetes.JobsNamespace = jobsNamespace
	conf.Kubernetes.WorkerTemplate = workerImageTemplate

	client := fake.NewSimpleClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "funnel-server",
			Namespace: namespace,
			Labels:    map[string]string{"app": "funnel"},
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "funnel", Image: "funnel-server:v1"}}},
	})

	job, err := CreateJob(context.Background(), &tes.Task{Id: testTaskID}, conf, client, logger.NewLogger("test", logger.DefaultConfig()), nil)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if got, want := job.Spec.Template.Spec.Containers[0].Image, "funnel-server:v1"; got != want {
		t.Fatalf("worker image = %q, want discovered image %q", got, want)
	}
}
