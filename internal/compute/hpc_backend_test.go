package compute

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/tes"
)

func TestSetupTemplatedHPCSubmit(t *testing.T) {
	tmp, err := os.MkdirTemp("", "funnel-test-scheduler")
	if err != nil {
		t.Fatal(err)
	}

	conf := config.DefaultConfig()
	conf.Worker.WorkDir = tmp

	task := &tes.Task{
		Id: "test-taskid",
		Executors: []*tes.Executor{
			{
				Command: []string{"echo test"},
			},
		},
		Resources: &tes.Resources{
			CpuCores: 1,
			RamGb:    1.0,
			DiskGb:   10.0,
		},
	}

	tpl := `
#!/bin/bash
#TEST --name {{.TaskId}}
#TEST --flag
#TEST -e {{.WorkDir}}/stderr
#TEST -o {{.WorkDir}}/stdout
{{if ne .Cpus 0 -}}
{{printf "#TEST --cpus %d" .Cpus}}
{{- end}}
{{if ne .RamGb 0.0 -}}
{{printf "#TEST --mem %.0fGB" .RamGb}}
{{- end}}
{{if ne .DiskGb 0.0 -}}
{{printf "#TEST --disk %.1fGB" .DiskGb}}
{{- end}}

funnel worker run --taskID {{.TaskId}}
`

	b := HPCBackend{
		Name:      "test",
		SubmitCmd: "qsub",
		Template:  tpl,
		Conf:      conf,
	}

	sf, err := b.setupTemplatedHPCSubmit(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}

	actual, rerr := os.ReadFile(sf)
	if rerr != nil {
		t.Fatal(rerr)
	}

	expected := `
#!/bin/bash
#TEST --name test-taskid
#TEST --flag
#TEST -e %s/test-taskid/stderr
#TEST -o %s/test-taskid/stdout
#TEST --cpus 1
#TEST --mem 1GB
#TEST --disk 10.0GB

funnel worker run --taskID test-taskid
`

	expected = fmt.Sprintf(expected, tmp, tmp)

	if string(actual) != expected {
		t.Log("Expected", "", expected)
		t.Log("Actual", "", string(actual))
		t.Fatal("Unexpected content")
	}
}

func TestHPCBackendParametersAreSanitizedAndRendered(t *testing.T) {
	tmp := t.TempDir()
	conf := config.DefaultConfig()
	conf.Worker.WorkDir = tmp

	b := HPCBackend{Conf: conf, Name: "test", Template: `{{index .BackendParameters "gres"}} {{index .BackendParameters "partition"}} {{index .BackendParameters "unknown"}}`}
	if err := b.ConfigureBackendParameters(map[string]string{
		"GRES":      "gpu:[0-9]+|cpu",
		"Partition": "[a-z]+",
	}); err != nil {
		t.Fatal(err)
	}

	task := &tes.Task{
		Id: "parameters-task",
		Resources: &tes.Resources{BackendParameters: map[string]string{
			"gReS":      "gpu:2",
			"PARTITION": "accelerator",
			"ignored":   "not rendered",
		}},
	}
	if err := b.CheckBackendParameterSupport(task); err != nil {
		t.Fatalf("unexpected support error: %v", err)
	}

	keys := b.BackendParameterKeys()
	if fmt.Sprint(keys) != "[gres partition]" {
		t.Fatalf("unexpected backend parameter keys: %v", keys)
	}

	submitPath, err := b.setupTemplatedHPCSubmit(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(submitPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(actual), "gpu:2 accelerator "; got != want {
		t.Fatalf("unexpected rendered parameters: got %q, want %q", got, want)
	}
}

func TestHPCBackendParameterValidation(t *testing.T) {
	b := HPCBackend{}
	if err := b.ConfigureBackendParameters(map[string]string{"GPU": "gpu:[0-9]+", "line": "(?s).*"}); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		params  map[string]string
		strict  bool
		wantErr bool
	}{
		{name: "unknown non-strict is ignored", params: map[string]string{"unknown": "value"}},
		{name: "unknown strict is rejected", params: map[string]string{"unknown": "value"}, strict: true, wantErr: true},
		{name: "valid allowed value", params: map[string]string{"gpu": "gpu:0"}},
		{name: "invalid allowed value non-strict is rejected", params: map[string]string{"gpu": "cpu"}, wantErr: true},
		{name: "invalid allowed value strict is rejected", params: map[string]string{"gpu": "cpu"}, strict: true, wantErr: true},
		{name: "substring match is rejected", params: map[string]string{"gpu": "xgpu:0"}, wantErr: true},
		{name: "control character is rejected", params: map[string]string{"line": "gpu:0\n#SBATCH --partition=admin"}, wantErr: true},
		{name: "case-fold collision is rejected", params: map[string]string{"gpu": "gpu:0", "GPU": "gpu:1"}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			task := &tes.Task{Resources: &tes.Resources{
				BackendParameters:       test.params,
				BackendParametersStrict: test.strict,
			}}
			err := b.CheckBackendParameterSupport(task)
			if (err != nil) != test.wantErr {
				t.Fatalf("CheckBackendParameterSupport() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

func TestHPCBackendSubmitRejectsControlCharactersBeforeRenderOrCommand(t *testing.T) {
	tmp := t.TempDir()
	conf := config.DefaultConfig()
	conf.Worker.WorkDir = tmp
	marker := filepath.Join(tmp, "submit-called")
	submitCmd := filepath.Join(tmp, "submit.sh")
	if err := os.WriteFile(submitCmd, []byte("#!/bin/sh\ntouch \""+marker+"\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	b := HPCBackend{Conf: conf, Name: "test", SubmitCmd: submitCmd, Template: "{{index .BackendParameters \"gpu\"}}"}
	if err := b.ConfigureBackendParameters(map[string]string{"gpu": "(?s).*"}); err != nil {
		t.Fatal(err)
	}
	task := &tes.Task{
		Id: "invalid-parameter-task",
		Resources: &tes.Resources{BackendParameters: map[string]string{
			"gpu": "gpu:0\n#SBATCH --partition=admin",
		}},
	}
	if err := b.Submit(context.Background(), task); err == nil {
		t.Fatal("Submit() accepted a control character")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("submit command ran before rejecting the parameter: stat error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmp, task.Id)); !os.IsNotExist(err) {
		t.Fatalf("submit artifact was rendered before rejecting the parameter: stat error = %v", err)
	}
}

func TestHPCBackendRejectsInvalidParameterPatterns(t *testing.T) {
	var b HPCBackend
	if err := b.ConfigureBackendParameters(map[string]string{"gpu": "("}); err == nil {
		t.Fatal("ConfigureBackendParameters() accepted an invalid pattern")
	}
}
