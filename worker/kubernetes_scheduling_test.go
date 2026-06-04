package worker

import (
	"bytes"
	"reflect"
	"testing"
	"text/template"

	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/client-go/kubernetes/scheme"
)

const executorSchedulingTemplate = `
apiVersion: batch/v1
kind: Job
metadata:
  name: {{.TaskId}}-{{.JobId}}
  namespace: {{.JobsNamespace}}
spec:
  template:
    spec:
      {{- if .NodeSelector }}
      nodeSelector:
        {{- range $key, $value := .NodeSelector }}
        {{ $key }}: {{ $value }}
        {{- end }}
      {{- end }}
      {{- if .Tolerations }}
      tolerations:
        {{- range .Tolerations }}
        - key: {{ .Key }}
          operator: {{ .Operator }}
          effect: {{ .Effect }}
          {{if .Value}}value: {{ .Value }}{{end}}
        {{- end }}
      {{- end }}
      restartPolicy: Never
      containers:
      - name: exec
        image: alpine
        command: ["/bin/sh", "-c"]
        args: ["echo ok"]
`

func renderExecutorJobForTest(t *testing.T, nodeSelector map[string]string, tolerations []map[string]interface{}) *batchv1.Job {
	t.Helper()

	tpl, err := template.New("executor").Parse(executorSchedulingTemplate)
	if err != nil {
		t.Fatalf("parse template: %v", err)
	}

	data := map[string]interface{}{
		"TaskId":        "task-1",
		"JobId":         0,
		"JobsNamespace": "test-ns",
		"NodeSelector":  nodeSelector,
		"Tolerations":   tolerations,
	}

	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		t.Fatalf("execute template: %v", err)
	}

	obj, _, err := scheme.Codecs.UniversalDeserializer().Decode(buf.Bytes(), nil, nil)
	if err != nil {
		t.Fatalf("decode template: %v", err)
	}

	job, ok := obj.(*batchv1.Job)
	if !ok {
		t.Fatalf("decoded object is not Job: %T", obj)
	}
	return job
}

func TestExecutorScheduling_SameNodeIntent(t *testing.T) {
	sharedSelector := map[string]string{
		"pool": "shared",
	}
	tolerations := []map[string]interface{}{
		{
			"Key":      "dedicated",
			"Operator": "Equal",
			"Value":    "shared",
			"Effect":   "NoSchedule",
		},
	}

	job := renderExecutorJobForTest(t, sharedSelector, tolerations)

	if !reflect.DeepEqual(job.Spec.Template.Spec.NodeSelector, sharedSelector) {
		t.Fatalf("nodeSelector mismatch: got=%v want=%v", job.Spec.Template.Spec.NodeSelector, sharedSelector)
	}
	if len(job.Spec.Template.Spec.Tolerations) != 1 {
		t.Fatalf("unexpected tolerations length: got=%d want=1", len(job.Spec.Template.Spec.Tolerations))
	}
}

func TestExecutorScheduling_DifferentNodeIntent(t *testing.T) {
	workerSelector := map[string]string{
		"pool": "worker-pool-a",
	}
	executorSelector := map[string]string{
		"pool": "executor-pool-b",
	}
	tolerations := []map[string]interface{}{
		{
			"Key":      "dedicated",
			"Operator": "Equal",
			"Value":    "executor",
			"Effect":   "NoSchedule",
		},
	}

	job := renderExecutorJobForTest(t, executorSelector, tolerations)

	if reflect.DeepEqual(job.Spec.Template.Spec.NodeSelector, workerSelector) {
		t.Fatalf("executor should not match worker selector when different-node intent is configured")
	}
	if !reflect.DeepEqual(job.Spec.Template.Spec.NodeSelector, executorSelector) {
		t.Fatalf("executor nodeSelector mismatch: got=%v want=%v", job.Spec.Template.Spec.NodeSelector, executorSelector)
	}
}
