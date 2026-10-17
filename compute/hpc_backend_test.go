package compute

import (
	"context"
	"fmt"
	"os"
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
