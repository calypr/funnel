package tes

import "testing"

var configuredDefaultForbiddenPaths = []string{
	"/dev",
	"/proc",
	"/sys",
	"/run",
	"/var/run",
}

func TestValidation(t *testing.T) {
	v := Validate(&Task{}, nil)
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
		}, configuredDefaultForbiddenPaths)
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
	}, configuredDefaultForbiddenPaths)
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
	}, nil)
	if len(v) != 1 {
		t.Fatal("expected 1 validation error")
	}
}

// TestConfigurableForbiddenPaths verifies that a caller-supplied deny list
// replaces the configured defaults: configured prefixes are rejected, and
// paths omitted from the custom list are allowed.
func TestConfigurableForbiddenPaths(t *testing.T) {
	custom := []string{"/foo", "/bar/baz"}

	task := func(inputPath string) *Task {
		return &Task{
			Executors: []*Executor{
				{Image: "alpine", Command: []string{"echo"}},
			},
			Inputs: []*Input{
				{Url: "file:///src", Path: inputPath},
			},
		}
	}

	// Configured prefixes are forbidden.
	if v := Validate(task("/foo"), custom); len(v) == 0 {
		t.Errorf("expected /foo to be forbidden with custom deny list")
	}
	if v := Validate(task("/bar/baz/data"), custom); len(v) == 0 {
		t.Errorf("expected /bar/baz/data to be forbidden with custom deny list")
	}

	// A default prefix that is not in the custom list is now allowed, since the
	// custom list replaces the defaults.
	if v := Validate(task("/dev/sda"), custom); len(v) != 0 {
		t.Errorf("expected /dev/sda to be allowed when custom deny list replaces defaults, got: %v", v)
	}
	if v := Validate(task("/proc/self"), custom); len(v) != 0 {
		t.Errorf("expected /proc/self to be allowed when custom deny list replaces defaults, got: %v", v)
	}
}
