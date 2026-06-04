package kubernetes

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"text/template"

	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/client-go/kubernetes/scheme"
)

// fixtureTask is a minimal representation of a TES task fixture file.
// expected_stdout is a test-only field not part of the TES spec.
type fixtureTask struct {
	Name      string `json:"name"`
	Executors []struct {
		Command []string          `json:"command"`
		Env     map[string]string `json:"env"`
	} `json:"executors"`
	ExpectedStdout *string `json:"expected_stdout"`
}

// renderCommand renders the executor template for the given command slice and
// returns the container's command and args arrays exactly as Kubernetes would
// receive them.
func renderCommand(t *testing.T, command []string) ([]string, []string) {
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

// TestFixtureStdout loads every fixture that has an expected_stdout field,
// renders the executor template to obtain the exact command+args Kubernetes
// would use, runs that command locally, and asserts the output matches.
//
// Fixtures without expected_stdout are skipped — this covers cases like
// backtick.json (requires uname) or curl fixtures (require network) that are
// not safe to run in a unit test context.
func TestFixtureStdout(t *testing.T) {
	fixturesDir, err := filepath.Abs(filepath.Join("..", "..", "tests", "fixtures", "quotes"))
	if err != nil {
		t.Fatalf("resolve fixtures dir: %v", err)
	}

	files, err := filepath.Glob(filepath.Join(fixturesDir, "*.json"))
	if err != nil {
		t.Fatalf("glob fixtures: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no fixture files found")
	}

	for _, file := range files {
		file := file
		t.Run(filepath.Base(file), func(t *testing.T) {
			raw, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}

			var task fixtureTask
			if err := json.Unmarshal(raw, &task); err != nil {
				t.Fatalf("parse fixture: %v", err)
			}

			if task.ExpectedStdout == nil {
				t.Skipf("no expected_stdout — skipping stdout verification")
			}

			if len(task.Executors) == 0 || len(task.Executors[0].Command) == 0 {
				t.Fatal("fixture has no executor command")
			}

			command := task.Executors[0].Command
			env := task.Executors[0].Env

			gotCmd, gotArgs := renderCommand(t, command)

			// gotCmd is either ["/bin/sh", "-c"] or the exec command array.
			// Combine into a single exec.Command call.
			var name string
			var args []string
			if len(gotArgs) > 0 {
				// shell branch: gotCmd = ["/bin/sh", "-c"], gotArgs = ["script"]
				name = gotCmd[0]
				args = append(gotCmd[1:], gotArgs...)
			} else {
				// exec branch: gotCmd = ["prog", "arg1", ...]
				name = gotCmd[0]
				args = gotCmd[1:]
			}

			c := exec.Command(name, args...)

			// Inject env vars from the fixture (on top of a clean environment
			// so $HOME etc. don't bleed in from the test runner).
			if len(env) > 0 {
				for k, v := range env {
					c.Env = append(c.Env, k+"="+v)
				}
			}

			out, err := c.Output()
			if err != nil {
				t.Fatalf("run command %v %v: %v", name, args, err)
			}

			got := string(out)
			want := *task.ExpectedStdout
			// Normalise \n vs \r\n
			got = strings.ReplaceAll(got, "\r\n", "\n")

			if got != want {
				t.Errorf("stdout mismatch\n  got:  %q\n  want: %q", got, want)
			}
		})
	}
}
