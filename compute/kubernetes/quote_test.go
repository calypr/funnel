package kubernetes

import (
	"bytes"
	"testing"
	"text/template"

	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/client-go/kubernetes/scheme"
)

// minimalExecutorTemplate mirrors the command/args section of executor-job.yaml
// so that quote-handling can be tested without a live cluster.
const minimalExecutorTemplate = `
apiVersion: batch/v1
kind: Job
metadata:
  name: {{.TaskId}}-{{.JobId}}
  namespace: default
spec:
  template:
    spec:
      restartPolicy: Never
      containers:
      - name: exec
        image: alpine
        {{- if .UseShell}}
        command: ["/bin/sh", "-c"]
        args:
          - {{printf "%q" (index .Command 0)}}
        {{- else}}
        command:
          {{- range .Command}}
          - {{printf "%q" .}}
          {{- end}}
        {{- end}}
`

func renderArgs(t *testing.T, command []string) ([]string, []string) {
	t.Helper()

	useShell := len(command) == 1

	tpl, err := template.New("exec").Parse(minimalExecutorTemplate)
	if err != nil {
		t.Fatalf("parse template: %v", err)
	}

	data := map[string]interface{}{
		"TaskId":   "task-1",
		"JobId":    0,
		"Command":  command,
		"UseShell": useShell,
	}

	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		t.Fatalf("execute template: %v", err)
	}

	obj, _, err := scheme.Codecs.UniversalDeserializer().Decode(buf.Bytes(), nil, nil)
	if err != nil {
		t.Fatalf("decode rendered YAML: %v\n---\n%s", err, buf.String())
	}

	job, ok := obj.(*batchv1.Job)
	if !ok {
		t.Fatalf("decoded object is not a Job: %T", obj)
	}

	containers := job.Spec.Template.Spec.Containers
	if len(containers) == 0 {
		t.Fatal("no containers in rendered job")
	}
	return containers[0].Command, containers[0].Args
}

func TestQuoteHandling(t *testing.T) {
	tests := []struct {
		name        string
		command     []string
		wantCommand []string
		wantArgs    []string
	}{
		{
			name:        "multi-element exec — plain words",
			command:     []string{"echo", "hello"},
			wantCommand: []string{"echo", "hello"},
			wantArgs:    nil,
		},
		{
			name:        "multi-element exec — space in argument",
			command:     []string{"echo", "Hello World!"},
			wantCommand: []string{"echo", "Hello World!"},
			wantArgs:    nil,
		},
		{
			name:        "multi-element exec — single quote in argument",
			command:     []string{"echo", "Hello O'hare!"},
			wantCommand: []string{"echo", "Hello O'hare!"},
			wantArgs:    nil,
		},
		{
			name:        "multi-element exec — double quote in argument",
			command:     []string{"echo", `"double quoted"`},
			wantCommand: []string{"echo", `"double quoted"`},
			wantArgs:    nil,
		},
		{
			name:        "multi-element exec — mixed quotes",
			command:     []string{"echo", `"mix 'of' quotes"`},
			wantCommand: []string{"echo", `"mix 'of' quotes"`},
			wantArgs:    nil,
		},
		{
			name:        "multi-element exec — backslash",
			command:     []string{"echo", `back\slash`},
			wantCommand: []string{"echo", `back\slash`},
			wantArgs:    nil,
		},
		{
			name:        "multi-element exec — empty argument preserved",
			command:     []string{"echo", ""},
			wantCommand: []string{"echo", ""},
			wantArgs:    nil,
		},
		{
			name:        "multi-element exec — curl with header",
			command:     []string{"curl", "-s", "https://api.github.com/zen", "-H", "Accept: application/json,text/event-stream"},
			wantCommand: []string{"curl", "-s", "https://api.github.com/zen", "-H", "Accept: application/json,text/event-stream"},
			wantArgs:    nil,
		},
		{
			name:        "multi-element exec — explicit sh -c",
			command:     []string{"sh", "-c", "echo 'inline shell' && ls -1"},
			wantCommand: []string{"sh", "-c", "echo 'inline shell' && ls -1"},
			wantArgs:    nil,
		},
		{
			name:        "single-element shell script — plain",
			command:     []string{"echo hello"},
			wantCommand: []string{"/bin/sh", "-c"},
			wantArgs:    []string{"echo hello"},
		},
		{
			name:        "single-element shell script — single quotes",
			command:     []string{"echo 'Hello Nextflow!'"},
			wantCommand: []string{"/bin/sh", "-c"},
			wantArgs:    []string{"echo 'Hello Nextflow!'"},
		},
		{
			name:        "single-element shell script — double quotes",
			command:     []string{`echo "double quoted value"`},
			wantCommand: []string{"/bin/sh", "-c"},
			wantArgs:    []string{`echo "double quoted value"`},
		},
		{
			name:        "single-element shell script — mixed quotes",
			command:     []string{`echo "mix 'of' \"quotes\""`},
			wantCommand: []string{"/bin/sh", "-c"},
			wantArgs:    []string{`echo "mix 'of' \"quotes\""`},
		},
		{
			name:        "single-element shell script — chained commands",
			command:     []string{`echo start && echo "inner 'quotes'" && echo end`},
			wantCommand: []string{"/bin/sh", "-c"},
			wantArgs:    []string{`echo start && echo "inner 'quotes'" && echo end`},
		},
		{
			name:        "single-element shell script — backtick substitution",
			command:     []string{"echo `uname -s`"},
			wantCommand: []string{"/bin/sh", "-c"},
			wantArgs:    []string{"echo `uname -s`"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotCommand, gotArgs := renderArgs(t, tc.command)

			if len(gotCommand) != len(tc.wantCommand) {
				t.Fatalf("command length mismatch: got %d (%v), want %d (%v)", len(gotCommand), gotCommand, len(tc.wantCommand), tc.wantCommand)
			}
			for i := range tc.wantCommand {
				if gotCommand[i] != tc.wantCommand[i] {
					t.Errorf("command[%d]: got %q, want %q", i, gotCommand[i], tc.wantCommand[i])
				}
			}

			if len(gotArgs) != len(tc.wantArgs) {
				t.Fatalf("args length mismatch: got %d (%v), want %d (%v)", len(gotArgs), gotArgs, len(tc.wantArgs), tc.wantArgs)
			}
			for i := range tc.wantArgs {
				if gotArgs[i] != tc.wantArgs[i] {
					t.Errorf("args[%d]: got %q, want %q", i, gotArgs[i], tc.wantArgs[i])
				}
			}
		})
	}
}
