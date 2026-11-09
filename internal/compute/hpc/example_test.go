package hpc

import (
	"os"
	"strings"
	"testing"

	"github.com/calypr/funnel/api/tes"
	appconfig "github.com/calypr/funnel/internal/config"
)

func TestExampleHPCTemplatesRenderWorkerCommand(t *testing.T) {
	conf := appconfig.EmptyConfig()
	if err := appconfig.Parse([]byte(appconfig.Examples()["default-config"]), conf); err != nil {
		t.Fatal(err)
	}
	conf.Worker.WorkDir = t.TempDir()
	for _, example := range []struct{ name, template string }{
		{"htcondor", conf.HTCondor.Template},
		{"slurm", conf.Slurm.Template},
		{"pbs", conf.PBS.Template},
		{"gridengine", conf.GridEngine.Template},
	} {
		t.Run(example.name, func(t *testing.T) {
			backend := &Backend{Name: example.name, Conf: conf, Template: example.template}
			task := &tes.Task{Id: "example-task", Resources: &tes.Resources{CpuCores: 1, RamGb: 1, DiskGb: 1}}
			path, err := backend.setupTemplatedHPCSubmit(t.Context(), task)
			if err != nil {
				t.Fatal(err)
			}
			script, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(script), "<no value>") {
				t.Fatalf("example references unavailable template fields:\n%s", script)
			}
			if !strings.Contains(string(script), "worker run") || !strings.Contains(string(script), "--taskID example-task") {
				t.Fatalf("missing worker command:\n%s", script)
			}
		})
	}
}
