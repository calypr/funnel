package resources

import (
	"context"
	"strings"
	"testing"

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
