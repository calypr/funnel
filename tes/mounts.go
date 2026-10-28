package tes

import (
	"fmt"
	"path"
	"strings"
)

// ValidateMountPaths rejects task paths that would create a container mount
// at the filesystem root. Docker can instead mount declared root-level
// executor streams as individual files.
func ValidateMountPaths(task *Task, executorBackend string) error {
	executorBackend = strings.ToLower(strings.TrimSpace(executorBackend))
	if executorBackend == "k8s" {
		executorBackend = "kubernetes"
	}
	allowRootStreams := executorBackend == "docker"

	rootStreams := map[string]bool{}
	allStreams := make([]string, 0, len(task.Executors)*2)
	for i, executor := range task.Executors {
		for _, stream := range []struct {
			field string
			path  string
		}{{"Stdout", executor.Stdout}, {"Stderr", executor.Stderr}} {
			if stream.path == "" {
				continue
			}
			streamPath := path.Clean(stream.path)
			allStreams = append(allStreams, streamPath)
			if path.Dir(streamPath) != "/" {
				continue
			}
			if !allowRootStreams {
				return fmt.Errorf("Task.Executors[%d].%s: Kubernetes cannot mount root-level stream files; use a dedicated directory such as /outputs/%s", i, stream.field, path.Base(streamPath))
			}
			if streamPath == "/" {
				return fmt.Errorf("Task.Executors[%d].%s: a stream file cannot be the container root", i, stream.field)
			}
			rootStreams[streamPath] = true
		}
	}

	for i, input := range task.Inputs {
		inputPath := path.Clean(input.Path)
		if inputPath == "/" || (input.Type != FileType_DIRECTORY && path.Dir(inputPath) == "/") {
			inputName := path.Base(inputPath)
			if inputName == "/" {
				inputName = "data"
			}
			return fmt.Errorf("Task.Inputs[%d].Path: %q requires a mount at /, which is not allowed; use a dedicated input directory such as /inputs/%s", i, input.Path, inputName)
		}
	}

	for i, output := range task.Outputs {
		outputPath := path.Clean(output.Path)
		if output.Type == FileType_DIRECTORY {
			if outputPath == "/" {
				return fmt.Errorf("Task.Outputs[%d].Path: %q requires a mount at /, which is not allowed; use a dedicated output directory such as /outputs", i, output.Path)
			}
			continue
		}
		if path.Dir(outputPath) == "/" && !rootStreams[outputPath] {
			outputName := path.Base(outputPath)
			if outputName == "/" {
				outputName = "result"
			}
			return fmt.Errorf("Task.Outputs[%d].Path: %q requires a mount at /, which is not allowed; use a dedicated output directory such as /outputs/%s. Docker root-level files are supported only when the path matches an executor stdout or stderr path", i, output.Path, outputName)
		}
	}

	for i, volume := range task.Volumes {
		if path.Clean(volume) == "/" {
			return fmt.Errorf("Task.Volumes[%d]: %q requires a mount at /, which is not allowed; use a dedicated task directory such as /scratch", i, volume)
		}
	}

	for streamPath := range rootStreams {
		for i, input := range task.Inputs {
			if pathsOverlap(streamPath, input.Path) {
				return fmt.Errorf("root-level executor stream path %q conflicts with Task.Inputs[%d].Path %q", streamPath, i, input.Path)
			}
		}
		for i, output := range task.Outputs {
			outputPath := path.Clean(output.Path)
			if output.Type == FileType_FILE && outputPath == streamPath {
				continue
			}
			if pathsOverlap(streamPath, outputPath) {
				return fmt.Errorf("root-level executor stream path %q conflicts with Task.Outputs[%d].Path %q", streamPath, i, output.Path)
			}
		}
		for i, volume := range task.Volumes {
			if pathsOverlap(streamPath, volume) {
				return fmt.Errorf("root-level executor stream path %q conflicts with Task.Volumes[%d] %q", streamPath, i, volume)
			}
		}
		for i, executor := range task.Executors {
			if pathIsWithinFile(streamPath, executor.Workdir) {
				return fmt.Errorf("root-level executor stream path %q conflicts with Task.Executors[%d].Workdir %q", streamPath, i, executor.Workdir)
			}
		}
		for _, otherStream := range allStreams {
			if otherStream != streamPath && pathsOverlap(streamPath, otherStream) {
				return fmt.Errorf("root-level executor stream path %q conflicts with executor stream path %q", streamPath, otherStream)
			}
		}
	}

	return nil
}

func pathsOverlap(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	a = path.Clean(a)
	b = path.Clean(b)
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

func pathIsWithinFile(filePath, candidatePath string) bool {
	if candidatePath == "" {
		return false
	}
	filePath = path.Clean(filePath)
	candidatePath = path.Clean(candidatePath)
	return candidatePath == filePath || strings.HasPrefix(candidatePath, filePath+"/")
}
