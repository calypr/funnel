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
		"/data/../dev":  false, // forbidden after cleaning dot segments
		"//dev/null":    false, // forbidden after cleaning repeated slashes
		"/devices/data": true,  // allowed: not a /dev segment
		"/data/dev":     true,  // allowed: forbidden prefix not at root
		"/dev/../data":  true,  // allowed after cleaning out of /dev
		"/home/inputs":  true,  // allowed
	}

	for path, valid := range cases {
		v := ValidateWithForbiddenPathPrefixes(&Task{
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
	v := ValidateWithForbiddenPathPrefixes(&Task{
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
	})
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
	if v := ValidateWithForbiddenPathPrefixes(task("/foo"), custom); len(v) == 0 {
		t.Errorf("expected /foo to be forbidden with custom deny list")
	}
	if v := ValidateWithForbiddenPathPrefixes(task("/bar/baz/data"), custom); len(v) == 0 {
		t.Errorf("expected /bar/baz/data to be forbidden with custom deny list")
	}

	// A default prefix that is not in the custom list is now allowed, since the
	// custom list replaces the defaults.
	if v := ValidateWithForbiddenPathPrefixes(task("/dev/sda"), custom); len(v) != 0 {
		t.Errorf("expected /dev/sda to be allowed when custom deny list replaces defaults, got: %v", v)
	}
	if v := ValidateWithForbiddenPathPrefixes(task("/proc/self"), custom); len(v) != 0 {
		t.Errorf("expected /proc/self to be allowed when custom deny list replaces defaults, got: %v", v)
	}
}

func TestRootForbiddenPath(t *testing.T) {
	task := &Task{
		Executors: []*Executor{{Image: "alpine", Command: []string{"echo"}}},
		Volumes:   []string{"/data"},
	}
	if v := ValidateWithForbiddenPathPrefixes(task, []string{"/"}); len(v) == 0 {
		t.Fatal("expected root prefix to forbid every absolute container path")
	}
}

func TestForbiddenPathPrefixNormalization(t *testing.T) {
	cases := []struct {
		candidate string
		prefixes  []string
		forbidden bool
	}{
		{candidate: "/dev/null", prefixes: []string{"/dev/"}, forbidden: true},
		{candidate: "/safe/../dev/null", prefixes: []string{"//dev"}, forbidden: true},
		{candidate: "/devices", prefixes: []string{"/dev/"}, forbidden: false},
		{candidate: "/dev", prefixes: []string{"dev"}, forbidden: false},
	}

	for _, tc := range cases {
		if got := isForbiddenPath(tc.candidate, tc.prefixes); got != tc.forbidden {
			t.Errorf("isForbiddenPath(%q, %v) = %t, want %t", tc.candidate, tc.prefixes, got, tc.forbidden)
		}
	}
}
