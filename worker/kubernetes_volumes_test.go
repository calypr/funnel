package worker

import (
	"bytes"
	"os"
	"testing"
	"text/template"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
)

func renderExecutorJobWithVolumes(t *testing.T, taskVolumes []string, pvcVolumes []Volume, needsPVC bool) *batchv1.Job {
	t.Helper()

	tmplBytes, err := os.ReadFile("../config/kubernetes/executor-job.yaml")
	if err != nil {
		t.Fatalf("read executor-job.yaml: %v", err)
	}

	tpl, err := template.New("executor").Parse(string(tmplBytes))
	if err != nil {
		t.Fatalf("parse template: %v", err)
	}

	data := map[string]interface{}{
		"TaskId":             "task-abc",
		"JobId":              0,
		"Namespace":          "test-ns",
		"JobsNamespace":      "test-ns",
		"Command":            []string{"echo ok"},
		"UseShell":           false,
		"Workdir":            "/",
		"Volumes":            pvcVolumes,
		"TaskVolumes":        taskVolumes,
		"Env":                map[string]string{},
		"Cpus":               "100m",
		"RamGb":              "128Mi",
		"DiskGb":             "1Gi",
		"CpusLimit":          "0",
		"RamGbLimit":         "0",
		"DiskGbLimit":        "0",
		"Image":              "alpine",
		"NeedsPVC":           needsPVC,
		"NodeSelector":       nil,
		"Tolerations":        nil,
		"ServiceAccountName": "funnel-sa-test-ns",
	}

	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		t.Fatalf("execute template: %v\nrendered:\n%s", err, buf.String())
	}

	obj, _, err := scheme.Codecs.UniversalDeserializer().Decode(buf.Bytes(), nil, nil)
	if err != nil {
		t.Fatalf("decode rendered template: %v\nrendered:\n%s", err, buf.String())
	}

	job, ok := obj.(*batchv1.Job)
	if !ok {
		t.Fatalf("decoded object is not Job: %T", obj)
	}
	return job
}

// TestTaskVolumesRenderedAsEmptyDir verifies that TES task.Volumes are rendered
// as emptyDir volumes (not PVC mounts) in the executor job pod spec.
func TestTaskVolumesRenderedAsEmptyDir(t *testing.T) {
	job := renderExecutorJobWithVolumes(t,
		[]string{"/vol", "/tmp/shared"},
		nil,  // no PVC volumes
		false, // no PVC needed
	)

	podSpec := job.Spec.Template.Spec

	// Expect two volumes, both emptyDir
	if len(podSpec.Volumes) != 2 {
		t.Fatalf("expected 2 volumes, got %d: %+v", len(podSpec.Volumes), podSpec.Volumes)
	}
	for _, v := range podSpec.Volumes {
		if v.EmptyDir == nil {
			t.Errorf("volume %q should be emptyDir, got: %+v", v.Name, v.VolumeSource)
		}
	}

	// Expect two volumeMounts in the container
	if len(podSpec.Containers) == 0 {
		t.Fatal("no containers in pod spec")
	}
	mounts := podSpec.Containers[0].VolumeMounts
	if len(mounts) != 2 {
		t.Fatalf("expected 2 volumeMounts, got %d: %+v", len(mounts), mounts)
	}

	mountPaths := map[string]bool{}
	for _, m := range mounts {
		mountPaths[m.MountPath] = true
	}
	for _, vol := range []string{"/vol", "/tmp/shared"} {
		if !mountPaths[vol] {
			t.Errorf("expected mountPath %q not found in mounts: %+v", vol, mounts)
		}
	}
}

// TestTaskVolumesDoNotUsePVC verifies that when a task only has volumes (no
// inputs/outputs), no PVC volume appears in the pod spec.
func TestTaskVolumesDoNotUsePVC(t *testing.T) {
	job := renderExecutorJobWithVolumes(t,
		[]string{"/data"},
		nil,
		false,
	)

	for _, v := range job.Spec.Template.Spec.Volumes {
		if v.PersistentVolumeClaim != nil {
			t.Errorf("unexpected PVC volume %q when task has only TES volumes", v.Name)
		}
	}
}

// TestTaskVolumesAndPVCCoexist verifies that a task with both inputs (PVC) and
// TES volumes renders both a PVC mount and emptyDir mounts without conflict.
func TestTaskVolumesAndPVCCoexist(t *testing.T) {
	pvcVols := []Volume{
		{HostPath: "/work/inputs/file.txt", ContainerPath: "/inputs/file.txt", Readonly: false},
	}
	job := renderExecutorJobWithVolumes(t,
		[]string{"/vol"},
		pvcVols,
		true, // has PVC for inputs
	)

	podSpec := job.Spec.Template.Spec

	var hasEmptyDir, hasPVC bool
	for _, v := range podSpec.Volumes {
		if v.EmptyDir != nil {
			hasEmptyDir = true
		}
		if v.PersistentVolumeClaim != nil {
			hasPVC = true
		}
	}

	if !hasEmptyDir {
		t.Error("expected at least one emptyDir volume for TES task volume")
	}
	if !hasPVC {
		t.Error("expected PVC volume for inputs")
	}

	// Verify no mountPath appears twice
	mounts := podSpec.Containers[0].VolumeMounts
	seen := map[string]int{}
	for _, m := range mounts {
		seen[m.MountPath]++
	}
	for path, count := range seen {
		if count > 1 {
			t.Errorf("mountPath %q appears %d times — duplicate mount", path, count)
		}
	}

	// Verify the emptyDir mount does not use a subPath (subPath is PVC-specific)
	for _, m := range mounts {
		if m.MountPath == "/vol" && m.SubPath != "" {
			t.Errorf("emptyDir mount at /vol should not have subPath, got %q", m.SubPath)
		}
	}

	_ = corev1.EmptyDirVolumeSource{} // ensure corev1 import used
}
