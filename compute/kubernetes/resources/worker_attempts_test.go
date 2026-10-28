package resources

import (
	"context"
	"strings"
	"testing"

	"github.com/calypr/funnel/compute/kubernetes/storageplan"
	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/logger"
	"github.com/calypr/funnel/tes"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestCreateJobGivesWorkerRetriesDistinctPodIdentities(t *testing.T) {
	conf := config.DefaultConfig()
	conf.Kubernetes.Namespace = namespace
	conf.Kubernetes.JobsNamespace = jobsNamespace
	conf.Kubernetes.WorkerImage = "funnel:test"
	conf.Kubernetes.WorkerTemplate = strings.Replace(workerImageTemplate, "  template:", "  backoffLimit: 7\n  template:", 1) + `        env:
        - name: FUNNEL_WORKER_POD_UID
          value: stale-template-value
        - name: USER_VARIABLE
          value: preserved
`
	job, err := CreateJob(context.Background(), &tes.Task{Id: testTaskID}, conf, fake.NewSimpleClientset(), logger.NewLogger("test", logger.DefaultConfig()), nil)
	if err != nil {
		t.Fatal(err)
	}
	if job.Spec.Template.Spec.RestartPolicy != corev1.RestartPolicyNever {
		t.Fatalf("restart policy = %s, want Never", job.Spec.Template.Spec.RestartPolicy)
	}
	if job.Spec.BackoffLimit == nil || *job.Spec.BackoffLimit != 7 {
		t.Fatalf("retry limit changed: %v", job.Spec.BackoffLimit)
	}
	if got := job.Spec.Template.Annotations[WorkerContainerAnnotation]; got != "worker" {
		t.Fatalf("worker container annotation = %q, want worker", got)
	}
	want := map[string]string{events.OwnerNamespaceEnv: "metadata.namespace", events.OwnerNameEnv: "metadata.name", events.OwnerUIDEnv: "metadata.uid"}
	seen := map[string]int{}
	for _, env := range job.Spec.Template.Spec.Containers[0].Env {
		if path, ok := want[env.Name]; ok {
			seen[env.Name]++
			if env.Value != "" || env.ValueFrom == nil || env.ValueFrom.FieldRef == nil || env.ValueFrom.FieldRef.FieldPath != path {
				t.Fatalf("identity environment = %+v", env)
			}
		}
		if env.Name == "USER_VARIABLE" && env.Value != "preserved" {
			t.Fatalf("user environment changed: %+v", env)
		}
	}
	for name := range want {
		if seen[name] != 1 {
			t.Fatalf("%s occurs %d times", name, seen[name])
		}
	}
}

const workerAndSidecarTemplate = `apiVersion: batch/v1
kind: Job
metadata:
  name: {{.TaskId}}
  namespace: {{.JobsNamespace}}
spec:
  template:
    metadata:
      annotations:
        funnel.calypr.org/worker-container: worker
    spec:
      restartPolicy: OnFailure
      containers:
      - name: worker
        image: {{.Image}}
      - name: sidecar
        image: {{.Image}}
`

func TestCreateJobUsesValidatedTemplateWorkerContainerAnnotation(t *testing.T) {
	conf := config.DefaultConfig()
	conf.Kubernetes.Namespace = namespace
	conf.Kubernetes.JobsNamespace = jobsNamespace
	conf.Kubernetes.WorkerImage = "funnel:test"
	conf.Kubernetes.WorkerTemplate = workerAndSidecarTemplate
	job, err := CreateJob(context.Background(), &tes.Task{Id: testTaskID}, conf, fake.NewSimpleClientset(), logger.NewLogger("test", logger.DefaultConfig()), nil)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if got := job.Spec.Template.Annotations[WorkerContainerAnnotation]; got != "worker" {
		t.Fatalf("worker container annotation = %q, want worker", got)
	}
}

func TestCreateJobPlanOverridesTemplateWorkerContainerAnnotation(t *testing.T) {
	conf := config.DefaultConfig()
	conf.Kubernetes.Namespace = namespace
	conf.Kubernetes.JobsNamespace = jobsNamespace
	conf.Kubernetes.WorkerImage = "funnel:test"
	conf.Kubernetes.WorkerTemplate = strings.Replace(workerAndSidecarTemplate, ": worker", ": sidecar", 1)
	plan := &storageplan.Plan{
		Version:         4,
		TaskID:          testTaskID,
		Namespace:       jobsNamespace,
		WorkDir:         conf.Worker.WorkDir,
		WorkspaceVolume: "workspace",
		WorkerContainer: "worker",
		Volumes: []corev1.Volume{{
			Name:         "workspace",
			VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: testTaskID + "-workspace"}},
		}},
	}
	job, err := CreateJob(context.Background(), &tes.Task{Id: testTaskID}, conf, fake.NewSimpleClientset(), logger.NewLogger("test", logger.DefaultConfig()), plan)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if got := job.Spec.Template.Annotations[WorkerContainerAnnotation]; got != "worker" {
		t.Fatalf("worker container annotation = %q, want plan selection worker", got)
	}
}

func TestCreateJobRejectsUnknownTemplateWorkerContainerAnnotation(t *testing.T) {
	conf := config.DefaultConfig()
	conf.Kubernetes.Namespace = namespace
	conf.Kubernetes.JobsNamespace = jobsNamespace
	conf.Kubernetes.WorkerImage = "funnel:test"
	conf.Kubernetes.WorkerTemplate = strings.Replace(workerAndSidecarTemplate, ": worker", ": missing", 1)
	if _, err := CreateJob(context.Background(), &tes.Task{Id: testTaskID}, conf, fake.NewSimpleClientset(), logger.NewLogger("test", logger.DefaultConfig()), nil); err == nil {
		t.Fatal("CreateJob accepted a worker-container annotation that names no container")
	}
}

func TestCreateJobLeavesUnknownMultiContainerWorkerUnannotated(t *testing.T) {
	conf := config.DefaultConfig()
	conf.Kubernetes.Namespace = namespace
	conf.Kubernetes.JobsNamespace = jobsNamespace
	conf.Kubernetes.WorkerImage = "funnel:test"
	conf.Kubernetes.WorkerTemplate = strings.Replace(workerAndSidecarTemplate, "      annotations:\n        funnel.calypr.org/worker-container: worker\n", "", 1)
	job, err := CreateJob(context.Background(), &tes.Task{Id: testTaskID}, conf, fake.NewSimpleClientset(), logger.NewLogger("test", logger.DefaultConfig()), nil)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if _, exists := job.Spec.Template.Annotations[WorkerContainerAnnotation]; exists {
		t.Fatalf("ambiguous multi-container worker annotation = %q, want absent", job.Spec.Template.Annotations[WorkerContainerAnnotation])
	}
}
