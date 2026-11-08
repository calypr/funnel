package worker

import (
	"bytes"
	_ "embed"
	"testing"
	"text/template"

	batchv1 "k8s.io/api/batch/v1"
	"sigs.k8s.io/yaml"
)

//go:embed testdata/executor-job.yaml
var executorTemplateFixture string

func TestExecutorTemplatePreservesScalars(t *testing.T) {
	tpl, err := template.New("executor").Parse(executorTemplateFixture)
	if err != nil {
		t.Fatal(err)
	}
	image, workdir, mount := "registry.example/tool:tag\ncontinued", "/work\ncontinued", "/data\ncontinued"
	data := map[string]interface{}{"TaskId": "task-1", "JobId": 0, "Namespace": "test", "JobsNamespace": "test", "Image": image, "Workdir": workdir, "Command": []string{"echo", "ok"}, "ServiceAccountName": "task-account", "Cpus": 1, "RamGb": 1, "DiskGb": 1, "NeedsPVC": true, "Volumes": []Volume{{ContainerPath: mount}}}
	var rendered bytes.Buffer
	if err := tpl.Execute(&rendered, data); err != nil {
		t.Fatal(err)
	}
	var job batchv1.Job
	if err := yaml.UnmarshalStrict(rendered.Bytes(), &job); err != nil {
		t.Fatalf("executor template did not preserve scalar data: %v", err)
	}
	container := job.Spec.Template.Spec.Containers[0]
	if container.Image != image || container.WorkingDir != workdir || container.VolumeMounts[0].MountPath != mount || container.VolumeMounts[0].SubPath != "task-1"+mount {
		t.Fatal("executor template changed scalar data")
	}
	if container.SecurityContext != nil || job.Spec.Template.Spec.HostNetwork {
		t.Fatal("scalar data changed pod policy")
	}
}
