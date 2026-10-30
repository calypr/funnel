package worker

import (
	"errors"
	"strings"
	"testing"
)

func TestHandlePanicDiagnostics(t *testing.T) {
	for _, value := range []any{"broken executor", errors.New("broken executor")} {
		var caught error
		func() {
			defer handlePanic(func(err error) { caught = err })
			panic(value)
		}()
		if caught == nil {
			t.Fatal("panic was not reported")
		}
		for _, want := range []string{"broken executor", "TestHandlePanicDiagnostics", "version="} {
			if !strings.Contains(caught.Error(), want) {
				t.Errorf("diagnostic missing %q: %s", want, caught)
			}
		}
	}
}
