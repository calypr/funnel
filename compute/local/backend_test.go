package local

import (
	"testing"

	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/logger"
	"github.com/ohsu-comp-bio/funnel/tes"
	"github.com/stretchr/testify/assert"
)

func createBackend(p map[string]string) *Backend {
	conf := config.DefaultConfig()
	log := logger.NewLogger("test", logger.DefaultConfig())
	b := &Backend{
		conf: conf,
		log:  log,
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
		assert.Equal(t, err.Error(), "backend parameters not supported")
	}
}
