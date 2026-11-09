package tes

import "testing"

func TestValidateRejectsRuntimeOptionsInImage(t *testing.T) {
	for _, image := range []string{"--privileged", "alpine --privileged", "alpine\n--privileged"} {
		t.Run(image, func(t *testing.T) {
			task := &Task{Executors: []*Executor{{Image: image, Command: []string{"true"}}}}
			if err := Validate(task); err == nil {
				t.Fatal("image argument was accepted as runtime options")
			}
		})
	}
}
