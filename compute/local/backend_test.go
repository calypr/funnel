package local

import (
	"strings"
	"testing"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/logger"
	"github.com/calypr/funnel/tes"
	"github.com/stretchr/testify/assert"
)

func createBackend(p map[string]string) *Backend {
	conf := config.DefaultConfig()
	log := logger.NewLogger("test", logger.DefaultConfig())
	b := &Backend{
		conf:              conf,
		log:               log,
		backendParameters: p,
	}
	return b
}

func createTask(r *tes.Resources) *tes.Task {
	task := &tes.Task{
		Id: "task1",
		Executors: []*tes.Executor{
			{
				Image:   "alpine",
				Command: []string{"echo", "hello world"},
			},
		},
		Resources: r,
	}
	return task
}

func TestDefault(t *testing.T) {
	b := createBackend(nil)
	task := createTask(nil)

	err := b.CheckBackendParameterSupport(task)
	assert.NoError(t, err)
}

func TestBackendParamaters(t *testing.T) {
	b := createBackend(nil)
	task := createTask(&tes.Resources{
		BackendParametersStrict: false,
		BackendParameters:       map[string]string{"foo": "bar"},
	})

	err := b.CheckBackendParameterSupport(task)
	assert.NoError(t, err)
}

func TestBackendParamatersStrict(t *testing.T) {
	b := createBackend(map[string]string{"foo": "bar"})
	task := createTask(&tes.Resources{
		BackendParametersStrict: true,
		BackendParameters:       map[string]string{"foo": "baz"},
	})

	err := b.CheckBackendParameterSupport(task)
	assert.NoError(t, err)
}

func TestBackendParamatersStrictFail(t *testing.T) {
	b := createBackend(nil)
	task := createTask(&tes.Resources{
		BackendParametersStrict: true,
		BackendParameters:       map[string]string{"foo": "bar"},
	})

	err := b.CheckBackendParameterSupport(task)
	if assert.Error(t, err) {
		assert.True(t, strings.Contains(err.Error(), "backend parameters not supported: foo"))
	}
}

func TestGPUBackendParameter(t *testing.T) {
	b := createBackend(nil)
	for _, tc := range []struct {
		value  string
		strict bool
		valid  bool
	}{
		{"2", true, true}, {"0", true, false}, {"-1", false, false},
	} {
		err := b.CheckBackendParameterSupport(createTask(&tes.Resources{BackendParameters: map[string]string{"gpu_count": tc.value}, BackendParametersStrict: tc.strict}))
		if (err == nil) != tc.valid {
			t.Errorf("gpu_count=%q strict=%v: %v", tc.value, tc.strict, err)
		}
	}
}

func TestGPUDeviceValidationBeforeExecution(t *testing.T) {
	b := createBackend(nil)
	for _, params := range []map[string]string{
		{"gpu_devices": "all"},
		{"gpu_devices": "0,0"},
		{"gpu_count": "2", "gpu_devices": "0"},
	} {
		if err := b.CheckBackendParameterSupport(createTask(&tes.Resources{BackendParameters: params})); err == nil {
			t.Errorf("accepted invalid GPU device request %v", params)
		}
	}
	if err := b.CheckBackendParameterSupport(createTask(&tes.Resources{BackendParametersStrict: true, BackendParameters: map[string]string{"GPU_DEVICES": "0,2"}})); err != nil {
		t.Fatal(err)
	}
}
