package taskmodel

import (
	"testing"

	"github.com/calypr/funnel/tes"
)

func TestValidateRejectsRuntimeOptionsInImage(t *testing.T) {
	for _, image := range []string{"--privileged", "alpine --privileged", "alpine\n--privileged"} {
		t.Run(image, func(t *testing.T) {
			task := &tes.Task{Executors: []*tes.Executor{{Image: image, Command: []string{"true"}}}}
			if err := Validate(task); err == nil {
				t.Fatal("image argument was accepted as runtime options")
			}
		})
	}
}
