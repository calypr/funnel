package tes

import (
	"fmt"
	"path"
	"strings"
)

// isForbiddenPath reports whether path is, or is nested under, any of the
// given forbidden path prefixes. The comparison is exact-segment based so that
// "/devices" is not treated as being under "/dev".
func isForbiddenPath(candidate string, prefixes []string) bool {
	if candidate == "" {
		return false
	}

	candidate = path.Clean(candidate)
	for _, prefix := range prefixes {
		if prefix == "" {
			continue
		}

		prefix = path.Clean(prefix)
		if !path.IsAbs(prefix) {
			continue
		}

		if prefix == "/" || candidate == prefix || strings.HasPrefix(candidate, prefix+"/") {
			return true
		}
	}
	return false
}

// ValidationError contains task validation errors.
type ValidationError []error

func (v *ValidationError) add(s string, a ...interface{}) {
	*v = append(*v, fmt.Errorf(s, a...))
}
func (v ValidationError) Error() string {
	s := ""
	for _, e := range v {
		s += fmt.Sprintln(e.Error())
	}
	return s
}

// Validate validates the given task and returns ValidationError,
// or nil if the task is valid.
func Validate(t *Task) ValidationError {
	return ValidateWithForbiddenPathPrefixes(t, nil)
}

// ValidateWithForbiddenPathPrefixes validates the given task and rejects
// inputs, outputs, volumes, and working directories at or beneath any of the
// configured container path prefixes.
func ValidateWithForbiddenPathPrefixes(t *Task, forbiddenPathPrefixes []string) ValidationError {
	var errs ValidationError

	if len(t.Executors) == 0 {
		errs.add("Task.Executors: at least one executor is required")
	}

	for i, exec := range t.Executors {
		if exec.Image == "" {
			errs.add("Task.Executors[%d].Image: required, but empty", i)
		}

		if len(exec.Command) == 0 {
			errs.add("Task.Executors[%d].Command: required, but empty", i)
		}

		if exec.Workdir != "" && !strings.HasPrefix(exec.Workdir, "/") {
			errs.add("Task.Executors[%d].Workdir: must be an absolute path", i)
		}

		if isForbiddenPath(exec.Workdir, forbiddenPathPrefixes) {
			errs.add("Task.Executors[%d].Workdir: %q is not allowed as a working directory", i, exec.Workdir)
		}

		if exec.Stdin != "" && !strings.HasPrefix(exec.Stdin, "/") {
			errs.add("Task.Executors[%d].Stdin: must be an absolute path", i)
		}

		if exec.Stdout != "" && !strings.HasPrefix(exec.Stdout, "/") {
			errs.add("Task.Executors[%d].Stdout: must be an absolute path", i)
		}

		if exec.Stderr != "" && !strings.HasPrefix(exec.Stderr, "/") {
			errs.add("Task.Executors[%d].Stderr: must be an absolute path", i)
		}
	}

	for i, input := range t.Inputs {
		if input.Content != "" && input.Url != "" {
			errs.add("Task.Inputs[%d].Content: Url is non-empty", i)
		} else if input.Url == "" && input.Content == "" {
			errs.add("Task.Inputs[%d].Url: required, but empty", i)
		}

		if input.Path == "" {
			errs.add("Task.Inputs[%d].Path: required, but empty", i)
		}

		if input.Path != "" && !strings.HasPrefix(input.Path, "/") {
			errs.add("task.Inputs[%d].Path: must be an absolute path", i)
		}

		if isForbiddenPath(input.Path, forbiddenPathPrefixes) {
			errs.add("Task.Inputs[%d].Path: %q is not allowed as an input path", i, input.Path)
		}
	}

	for i, output := range t.Outputs {
		if output.Url == "" {
			errs.add("Task.Outputs[%d].Url: required, but empty", i)
		}

		if output.Path == "" {
			errs.add("Task.Outputs[%d].Path: required, but empty", i)
		}

		if output.Path != "" && !strings.HasPrefix(output.Path, "/") {
			errs.add("task.Outputs[%d].Path: must be an absolute path", i)
		}

		if isForbiddenPath(output.Path, forbiddenPathPrefixes) {
			errs.add("Task.Outputs[%d].Path: %q is not allowed as an output path", i, output.Path)
		}
	}

	for i, vol := range t.Volumes {
		if !strings.HasPrefix(vol, "/") {
			errs.add("Task.Volumes[%d]: must be an absolute path", i)
		}

		if isForbiddenPath(vol, forbiddenPathPrefixes) {
			errs.add("Task.Volumes[%d]: %q is not allowed as a volume path", i, vol)
		}
	}

	for k, v := range t.Tags {
		if k == "" {
			errs.add(`Task.Tags[""]=%s: empty key`, v)
		}
	}

	return errs
}
