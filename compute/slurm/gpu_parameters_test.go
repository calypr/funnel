package slurm

import (
	"context"
	"strings"
	"testing"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/tes"
)

func TestNativeGPUParameters(t *testing.T) {
	conf := config.DefaultConfig()
	if !strings.Contains(conf.Slurm.Template, "worker run --Compute slurm --taskID") {
		t.Fatal("Slurm worker command omits backend identity")
	}
	conf.Slurm.BackendParameters = map[string]string{"gres": "gpu:[A-Za-z0-9_-]+:[1-9][0-9]*"}
	backend, err := NewBackend(context.Background(), conf, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if keys := backend.BackendParameterKeys(); len(keys) != 1 || keys[0] != "gres" {
		t.Fatalf("keys=%v", keys)
	}
	for _, tc := range []struct {
		value string
		valid bool
	}{{"gpu:a100:2", true}, {"gpu:a100:2\ncommand", false}} {
		err := backend.CheckBackendParameterSupport(&tes.Task{Resources: &tes.Resources{BackendParametersStrict: true, BackendParameters: map[string]string{"gres": tc.value}}})
		if (err == nil) != tc.valid {
			t.Fatalf("gres=%q error=%v", tc.value, err)
		}
	}
	conf.Slurm.BackendParameters = map[string]string{"gres": "("}
	if _, err := NewBackend(context.Background(), conf, nil, nil, nil); err == nil {
		t.Fatal("invalid administrator pattern accepted")
	}
}
