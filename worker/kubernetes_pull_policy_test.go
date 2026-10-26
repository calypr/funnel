package worker

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/tes"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestExecutorImagePullPolicy(t *testing.T) {
	tests := []struct {
		name string
		tags map[string]string
		want corev1.PullPolicy
	}{
		{name: "absent tag", want: corev1.PullAlways},
		{name: "empty tag", tags: map[string]string{"_IMAGE_PULL_POLICY": ""}, want: corev1.PullAlways},
		{name: "always", tags: map[string]string{"_IMAGE_PULL_POLICY": "Always"}, want: corev1.PullAlways},
		{name: "cached image", tags: map[string]string{"_IMAGE_PULL_POLICY": "IfNotPresent"}, want: corev1.PullIfNotPresent},
		{name: "local image only", tags: map[string]string{"_IMAGE_PULL_POLICY": "Never"}, want: corev1.PullNever},
		{name: "invalid policy", tags: map[string]string{"_IMAGE_PULL_POLICY": "sometimes"}},
		{name: "invalid multiline policy", tags: map[string]string{"_IMAGE_PULL_POLICY": "Always\n        privileged: true"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			conf := config.DefaultConfig()
			worker := DefaultWorker{
				Executor: Executor{
					Template:      string(config.MustAsset("config/kubernetes/executor-job.yaml")),
					Namespace:     "test",
					JobsNamespace: "test",
					Resources:     conf.Kubernetes.Resources,
				},
				EventWriter: events.Noop{},
			}
			task := &tes.Task{Id: "pull-policy", Tags: tt.tags}
			command, err := worker.newKubernetesCommand(ctx, events.NewTaskWriter(task.Id, 0, events.Noop{}), task, &tes.Executor{}, 0,
				Command{Image: "alpine:3.21", ShellCommand: []string{"echo", "ok"}}, &tes.Resources{CpuCores: 1, RamGb: 1, DiskGb: 1})
			if tt.want == "" {
				if err == nil || !strings.Contains(err.Error(), "_IMAGE_PULL_POLICY") {
					t.Fatalf("expected invalid pull-policy error, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}

			client := fake.NewSimpleClientset()
			var submitted *batchv1.Job
			stop := errors.New("stop after observing submitted job")
			client.PrependReactor("create", "jobs", func(action k8stesting.Action) (bool, runtime.Object, error) {
				submitted = action.(k8stesting.CreateAction).GetObject().(*batchv1.Job).DeepCopy()
				return true, nil, stop
			})
			command.Clientset = client
			err = command.Run(ctx)
			var systemErr *K8sSystemErr
			if !errors.As(err, &systemErr) || !errors.Is(systemErr.Err, stop) {
				t.Fatalf("expected to stop at job submission, got %v", err)
			}
			if submitted == nil {
				t.Fatal("executor job was not submitted")
			}
			if got := submitted.Spec.Template.Spec.Containers[0].ImagePullPolicy; got != tt.want {
				t.Fatalf("submitted executor image pull policy = %q, want %q", got, tt.want)
			}
		})
	}
}
