package tes

import "testing"

func TestValidation(t *testing.T) {
	v := Validate(&Task{})
	if len(v) == 0 {
		t.Fatal("expected validation errors")
	}
}

func TestForbiddenInputPath(t *testing.T) {
	cases := map[string]bool{
		"/dev":          false, // forbidden: exact match
		"/dev/sda":      false, // forbidden: nested
		"/proc/self":    false, // forbidden: nested
		"/sys":          false, // forbidden: exact match
		"/run/secret":   false, // forbidden: nested
		"/var/run/x":    false, // forbidden: nested
		"/devices/data": true,  // allowed: not a /dev segment
		"/data/dev":     true,  // allowed: forbidden prefix not at root
		"/home/inputs":  true,  // allowed
	}

	for path, valid := range cases {
		v := Validate(&Task{
			Executors: []*Executor{
				{Image: "alpine", Command: []string{"echo"}},
			},
			Inputs: []*Input{
				{Url: "file:///src", Path: path},
			},
		})
		if valid && len(v) != 0 {
			t.Errorf("path %q: expected no validation errors, got: %v", path, v)
		}
		if !valid && len(v) == 0 {
			t.Errorf("path %q: expected a forbidden-path validation error, got none", path)
		}
	}
}

func TestForbiddenOutputAndVolumePaths(t *testing.T) {
	v := Validate(&Task{
		Executors: []*Executor{
			{Image: "alpine", Command: []string{"echo"}, Workdir: "/proc/1"},
		},
		Outputs: []*Output{
			{Url: "file:///dst", Path: "/sys/kernel"},
		},
		Volumes: []string{"/var/run"},
	})
	// Expect one error each for Workdir, Output.Path, and Volume.
	if len(v) != 3 {
		t.Fatalf("expected 3 forbidden-path validation errors, got %d: %v", len(v), v)
	}
}

func TestEmptyTagKeyValidation(t *testing.T) {
	v := Validate(&Task{
		Tags: map[string]string{
			"": "bar",
		},
		Executors: []*Executor{
			{
				Image:   "alpine",
				Command: []string{"echo"},
			},
		},
	})
	if len(v) != 1 {
		t.Fatal("expected 1 validation error")
	}
}
