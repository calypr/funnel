package run

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/calypr/funnel/tes"
	"github.com/calypr/funnel/util/shellwords"
)

// ErrKeyFmt describes an error in input/output/env/tag flag formatting
var ErrKeyFmt = errors.New("Arguments passed to --in, --out, --tag and --env must be of the form: KEY=VALUE")

// ErrStorageScheme describes an error in supported storage URL schemes.
var ErrStorageScheme = errors.New("File paths must be prefixed with one of:\n file://\n gs://\n s3://")

// DuplicateKeyErr returns a new error describing conflicting keys for env. vars., inputs, and outputs.
func DuplicateKeyErr(key string) error {
	return errors.New("Can't use the same KEY for multiple --in, --out, --tag, --env arguments: " + key)
}

// Parse CLI variable definitions (e.g "varname=value") into usable task values.
func valsToTask(vals flagVals) (task *tes.Task, err error) {

	// Any error occurring during parsing the variables an preparing the task
	// is a fatal error, so I'm using panic/recover to simplify error handling.
	defer func() {
		if x := recover(); x != nil {
			err = x.(error)
		}
	}()

	environ := map[string]string{}

	// Build the task message
	task = &tes.Task{
		Name:        vals.name,
		Description: vals.description,
	}

	if vals.cpu > 0 || vals.ram > 0 || vals.disk > 0 || len(vals.zones) > 0 || vals.preemptible {
		task.Resources = &tes.Resources{
			CpuCores:    int32(vals.cpu),
			RamGb:       vals.ram,
			DiskGb:      vals.disk,
			Zones:       vals.zones,
			Preemptible: vals.preemptible,
		}
	}

	for _, vol := range vals.volumes {
		if !strings.HasPrefix(vol, "/") {
			panic(fmt.Errorf("volumes must be absolute paths: %s", vol))
		}
		task.Volumes = append(task.Volumes, vol)
	}

	// Create executors
	for i, exec := range vals.execs {
		// Split command string based on shell syntax.
		cmd, _ := shellwords.Split(exec.cmd)

		// Only set the stdio paths if their respective flags were used.
		var stdin, stdout, stderr string

		if exec.stdin != "" {
			stdin = fmt.Sprintf("/inputs/stdin-%d", i)
			task.Inputs = append(task.Inputs, &tes.Input{
				Name: fmt.Sprintf("stdin-%d", i),
				Url:  resolvePath(exec.stdin),
				Path: stdin,
			})
		}

		if exec.stdout != "" {
			stdout = fmt.Sprintf("/outputs/stdout-%d", i)
			task.Outputs = append(task.Outputs, &tes.Output{
				Name: fmt.Sprintf("stdout-%d", i),
				Url:  resolvePath(exec.stdout),
				Path: stdout,
			})
		}

		if exec.stderr != "" {
			stderr = fmt.Sprintf("/outputs/stderr-%d", i)
			task.Outputs = append(task.Outputs, &tes.Output{
				Name: fmt.Sprintf("stderr-%d", i),
				Url:  resolvePath(exec.stderr),
				Path: stderr,
			})
		}

		task.Executors = append(task.Executors, &tes.Executor{
			Image:   vals.container,
			Command: cmd,
			Workdir: vals.workdir,
			Stdin:   stdin,
			Stdout:  stdout,
			Stderr:  stderr,
		})
	}

	// Helper to make sure variable keys are unique.
	setenv := func(key, val string) {
		_, exists := environ[key]
		if exists {
			panic(DuplicateKeyErr(key))
		}
		environ[key] = val
	}

	for _, raw := range vals.inputs {
		k, v := parseCliVar(raw)
		url := resolvePath(v)
		path := "/inputs/" + stripStoragePrefix(url)
		setenv(k, path)
		task.Inputs = append(task.Inputs, &tes.Input{
			Name: k,
			Url:  url,
			Path: path,
		})
	}

	for _, raw := range vals.inputDirs {
		k, v := parseCliVar(raw)
		url := resolvePath(v)
		path := "/inputs/" + stripStoragePrefix(url)
		setenv(k, path)
		task.Inputs = append(task.Inputs, &tes.Input{
			Name: k,
			Url:  url,
			Path: path,
			Type: tes.FileType_DIRECTORY,
		})
	}

	for _, raw := range vals.content {
		k, v := parseCliVar(raw)
		path := "/inputs/" + stripStoragePrefix(resolvePath(v))
		setenv(k, path)
		task.Inputs = append(task.Inputs, &tes.Input{
			Name:    k,
			Path:    path,
			Content: getContent(v),
		})
	}

	for _, raw := range vals.outputs {
		k, v := parseCliVar(raw)
		url := resolvePath(v)
		path := "/outputs/" + stripStoragePrefix(url)
		setenv(k, path)
		task.Outputs = append(task.Outputs, &tes.Output{
			Name: k,
			Url:  url,
			Path: path,
		})
	}

	for _, raw := range vals.outputDirs {
		k, v := parseCliVar(raw)
		url := resolvePath(v)
		path := "/outputs/" + stripStoragePrefix(url)
		setenv(k, path)
		task.Outputs = append(task.Outputs, &tes.Output{
			Name: k,
			Url:  url,
			Path: path,
			Type: tes.FileType_DIRECTORY,
		})
	}

	for _, raw := range vals.environ {
		k, v := parseCliVar(raw)
		setenv(k, v)
	}

	if len(environ) > 0 {
		for _, e := range task.Executors {
			e.Env = environ
		}
	}

	if len(vals.tags) > 0 {
		task.Tags = map[string]string{}
	}

	for _, raw := range vals.tags {
		k, v := parseCliVar(raw)
		task.Tags[k] = v
	}
	return
}

func getContent(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func parseCliVar(raw string) (string, string) {
	re := regexp.MustCompile("=")
	res := re.Split(raw, -1)

	if len(res) != 2 {
		panic(ErrKeyFmt)
	}

	key := res[0]
	val := res[1]
	return key, val
}

// Give a input/output URL "raw", return the path of the file
// relative to the container.
func containerPath(raw, base string) string {
	url := resolvePath(raw)
	p := stripStoragePrefix(url)
	return base + p
}

func stripStoragePrefix(url string) string {
	re := regexp.MustCompile("[a-z0-9]+://")
	if !re.MatchString(url) {
		panic(ErrStorageScheme)
	}
	path := re.ReplaceAllString(url, "")
	return strings.TrimPrefix(path, "/")
}

func resolvePath(url string) string {
	re := regexp.MustCompile("[a-z0-9]+://")
	prefixed := re.MatchString(url)
	local := strings.HasPrefix(url, "/") || strings.HasPrefix(url, ".") ||
		strings.HasPrefix(url, "~") || !prefixed

	switch {
	case local:
		path, err := filepath.Abs(url)
		if err != nil {
			panic(err)
		}
		return "file://" + path
	case prefixed:
		return url
	default:
		panic(fmt.Errorf("could not resolve filepath: %s", url))
	}
}
