package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path"
	"strings"
	"text/template"
	"time"

	"github.com/calypr/funnel/events"
	tes "github.com/calypr/funnel/tes"
	"github.com/calypr/funnel/util/shellwords"
)

// DockerCommand is responsible for configuring and running a docker container.
type DockerCommand struct {
	Id               string
	Name             string
	Volumes          []Volume
	Workdir          string
	RemoveContainer  bool
	Event            *events.ExecutorWriter
	DriverCommand    string
	RunCommand       string // template string
	PullCommand      string // template string
	StopCommand      string // template string
	EnableTags       bool
	Tags             map[string]string
	Resources        *tes.Resources
	EnforceResources bool
	ComputeBackend   string
	Command
}

const gpuDevicesBackendParameter = "gpu_devices"

type dockerGPURequest struct {
	count   int
	devices []string
}

func (request dockerGPURequest) requested() bool {
	return request.count > 0 || len(request.devices) > 0
}

// GPUArgs returns the Docker flags for the task's requested GPUs. AWS Batch
// must use the exact devices assigned to its worker so the nested container
// cannot select GPUs allocated to another job.
func (docker DockerCommand) GPUArgs() (string, error) {
	request, err := dockerGPURequestFromResources(docker.Resources)
	if err != nil {
		return "", err
	}

	computeBackend := strings.ToLower(strings.TrimSpace(docker.ComputeBackend))
	awsBatch := isAWSBatchComputeBackend(computeBackend)
	allocation, hasSlurmAllocation := os.LookupEnv("FUNNEL_GPU_DEVICES")
	if os.Getenv("AWS_BATCH_JOB_ID") != "" && !awsBatch && (request.requested() || hasSlurmAllocation) {
		return "", fmt.Errorf("AWS Batch job has non-AWS compute backend %q; refusing to select GPUs without the AWS allocation", docker.ComputeBackend)
	}

	if awsBatch {
		if len(request.devices) > 0 {
			return "", fmt.Errorf("%s is only supported by local Docker compute", gpuDevicesBackendParameter)
		}
		if request.count == 0 {
			allocation := os.Getenv("NVIDIA_VISIBLE_DEVICES")
			switch strings.ToLower(allocation) {
			case "", "all", "none", "void":
				return "", nil
			}
			devices, err := tes.ParseGPUDevices(allocation, 0)
			if err != nil {
				return "", fmt.Errorf("invalid AWS Batch NVIDIA_VISIBLE_DEVICES allocation: %w", err)
			}
			return gpuDeviceArgs(devices), nil
		}
		devices, err := tes.ParseGPUDevices(os.Getenv("NVIDIA_VISIBLE_DEVICES"), request.count)
		if err != nil {
			return "", fmt.Errorf("AWS Batch GPU request requires NVIDIA_VISIBLE_DEVICES allocation: %w", err)
		}
		return gpuDeviceArgs(devices), nil
	}

	if computeBackend == "slurm" {
		if len(request.devices) > 0 {
			return "", fmt.Errorf("gpu_devices is only supported by local Docker compute")
		}
		if request.count == 0 && !hasSlurmAllocation {
			return "", nil
		}
		if !hasSlurmAllocation {
			return "", fmt.Errorf("Slurm GPU request requires FUNNEL_GPU_DEVICES with assigned physical device IDs")
		}
		devices, err := tes.ParseGPUDevices(allocation, request.count)
		if err != nil {
			return "", fmt.Errorf("invalid FUNNEL_GPU_DEVICES allocation: %w", err)
		}
		return gpuDeviceArgs(devices), nil
	}

	if computeBackend == "local" {
		if hasSlurmAllocation {
			return "", fmt.Errorf("FUNNEL_GPU_DEVICES requires the Slurm compute backend")
		}
		if len(request.devices) > 0 {
			return gpuDeviceArgs(request.devices), nil
		}
		if request.count > 0 {
			return fmt.Sprintf("--gpus %d", request.count), nil
		}
		return "", nil
	}

	if request.requested() || hasSlurmAllocation {
		return "", fmt.Errorf("GPU requests are not supported by Docker compute backend %q", docker.ComputeBackend)
	}
	return "", nil
}

func dockerGPURequestFromResources(resources *tes.Resources) (dockerGPURequest, error) {
	if resources == nil {
		return dockerGPURequest{}, nil
	}
	count, err := tes.GPUCount(resources)
	if err != nil {
		return dockerGPURequest{}, err
	}
	request := dockerGPURequest{count: count}
	devices, err := tes.GPUDevices(resources, request.count)
	if err != nil {
		return dockerGPURequest{}, fmt.Errorf("invalid %s: %w", gpuDevicesBackendParameter, err)
	}
	request.devices = devices
	return request, nil
}

func gpuDeviceArgs(devices []string) string {
	return fmt.Sprintf(`--gpus '"device=%s"'`, strings.Join(devices, ","))
}

func isAWSBatchComputeBackend(backend string) bool {
	switch backend {
	case "aws-batch", "aws", "amazon":
		return true
	default:
		return false
	}
}

// MemoryMB returns the task's requested RAM as an integer megabyte value
// suitable for passing to nerdctl/docker --memory flag (e.g. "2048m").
// Returns 0 if no memory limit is set.
func (docker DockerCommand) MemoryMB() int64 {
	if docker.Resources == nil || docker.Resources.RamGb <= 0 {
		return 0
	}
	return int64(math.Round(docker.Resources.RamGb * 1024))
}

// NeedsTmpfs reports whether the container needs Funnel's executor-local /tmp
// mount. A task-provided mount at /tmp or one of its ancestors takes precedence
// so explicitly requested storage can persist between executors.
func (docker DockerCommand) NeedsTmpfs() bool {
	const tmpDir = "/tmp"
	for _, volume := range docker.Volumes {
		containerPath := path.Clean(volume.ContainerPath)
		if containerPath == "/" || containerPath == tmpDir || strings.HasPrefix(tmpDir, containerPath+"/") {
			return false
		}
	}
	return true
}

type DockerVersion struct {
	Client string
	Server string
}

// Run runs the Docker command and blocks until done.
func (docker DockerCommand) Run(ctx context.Context) error {
	gpuArgs, err := docker.GPUArgs()
	if err != nil {
		return err
	}
	if gpuArgs != "" && !hasGPUArgsPlaceholder(docker.RunCommand) {
		return fmt.Errorf("Docker run template must include {{.GPUArgs}} to forward the task's GPU request")
	}

	// Sync docker API version info.
	err = docker.SyncAPIVersion()
	if err != nil {
		_ = docker.Event.Error("failed to sync docker client API version", err) //nolint:errcheck // Diagnostic logging must not replace the Docker execution result.
	}

	err = docker.executeCommand(ctx, docker.PullCommand, false)
	if err != nil {
		_ = docker.Event.Error("failed to pull docker image using command", err, docker.PullCommand) //nolint:errcheck // Diagnostic logging must not replace the Docker execution result.
	}

	err = docker.executeCommand(ctx, docker.RunCommand, true)
	if err != nil {
		_ = docker.Event.Error("failed to run docker container", err) //nolint:errcheck // The run error is returned below; logging must not replace it.
	}

	return err
}

func hasGPUArgsPlaceholder(commandTemplate string) bool {
	for {
		open := strings.Index(commandTemplate, "{{")
		if open < 0 {
			return false
		}
		commandTemplate = commandTemplate[open+2:]
		close := strings.Index(commandTemplate, "}}")
		if close < 0 {
			return false
		}
		action := strings.TrimSpace(commandTemplate[:close])
		action = strings.TrimPrefix(action, "-")
		action = strings.TrimSuffix(action, "-")
		if strings.TrimSpace(action) == ".GPUArgs" {
			return true
		}
		commandTemplate = commandTemplate[close+2:]
	}
}

// Stop stops the container.
func (docker DockerCommand) Stop() error {
	_ = docker.Event.Info("Stopping container", "container", docker.Name) //nolint:errcheck // Stop logging must not replace the stop command result.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := docker.executeCommand(ctx, docker.StopCommand, false)
	if err != nil {
		_ = docker.Event.Error("failed to stop docker container", err) //nolint:errcheck // The stop error is returned below; logging must not replace it.
		return err
	}
	return nil
}

func (docker DockerCommand) executeCommand(ctx context.Context, commandTemplate string, enableIO bool) error {
	var usingCommand bool = false
	if strings.Contains(commandTemplate, "{{.Command}}") {
		usingCommand = true
		commandTemplate = strings.ReplaceAll(commandTemplate, "{{.Command}}", "")
	}

	tmpl, err := template.New("command").Parse(commandTemplate)
	if err != nil {
		return fmt.Errorf("failed to parse template for command: %w", err)
	}

	var cmdBuffer bytes.Buffer
	err = tmpl.Execute(&cmdBuffer, docker)
	if err != nil {
		return fmt.Errorf("failed to execute template for command: %w", err)
	}

	cmdParts, err := shellwords.Split(cmdBuffer.String())
	if err != nil {
		return fmt.Errorf("failed to parse command: %w", err)
	}

	if usingCommand {
		inspectCtx, cancel := context.WithCancel(ctx)
		inspected := make(chan struct{})
		go func() {
			defer close(inspected)
			docker.InspectContainer(inspectCtx)
		}()
		defer func() {
			cancel()
			<-inspected
		}()

		commandToExecute := docker.Command.ShellCommand
		if len(commandToExecute) == 1 {
			cmdString := commandToExecute[0]
			commandToExecute = []string{"/bin/sh", "-c", cmdString}
		}

		cmdParts = append(cmdParts, commandToExecute...)
	}

	driverCmd := strings.Fields(docker.DriverCommand)
	var cmd *exec.Cmd
	if len(driverCmd) > 1 {
		cmdArgs := append(driverCmd[1:], cmdParts...)
		cmd = exec.CommandContext(ctx, driverCmd[0], cmdArgs...)
	} else {
		cmd = exec.CommandContext(ctx, driverCmd[0], cmdParts...)
	}

	if enableIO {
		if docker.Stdin != nil {
			cmd.Stdin = docker.Stdin
		}
		if docker.Stdout != nil {
			cmd.Stdout = docker.Stdout
		}
		if docker.Stderr != nil {
			cmd.Stderr = docker.Stderr
		}
	}

	if usingCommand {
		_ = docker.Event.Info("Running command", "cmd", cmd.String()) //nolint:errcheck // Informational logging must not change command execution.
	}
	return cmd.Run()
}

func formatVolumeArg(v Volume) string {
	// `o` is structed as "HostPath:ContainerPath:Mode".
	mode := "rw"
	if v.Readonly {
		mode = "ro"
	}
	return fmt.Sprintf("%s:%s:%s", v.HostPath, v.ContainerPath, mode)
}

// shellEscape safely escapes a string for use in a POSIX shell context.
// It wraps the value in single quotes and correctly handles embedded single quotes
// by closing the quote, inserting an escaped single quote, and reopening the quote.
func shellEscape(s string) string {
	if s == "" {
		return "''"
	}
	var b strings.Builder
	b.Grow(len(s) + 2) // approximate
	b.WriteByte('\'')
	for _, r := range s {
		if r == '\'' {
			b.WriteString("'\"'\"'")
		} else {
			b.WriteRune(r)
		}
	}
	b.WriteByte('\'')
	return b.String()
}

func formatEnvVars(env map[string]string) []string {
	var result []string
	for k, v := range env {
		escapedValue := shellEscape(v)
		result = append(result, fmt.Sprintf("--env %s=%s", k, escapedValue))
	}
	return result
}

func (docker DockerCommand) GetEnvArgs() string {
	return strings.Join(formatEnvVars(docker.Env), " ")
}

func (docker DockerCommand) GetImage() string {
	return docker.Image
}

// inspectContainer inspects the docker container for metadata.
func (docker *DockerCommand) InspectContainer(ctx context.Context) DockerCommand {
	// Give the container time to start.
	select {
	case <-ctx.Done():
		return DockerCommand{}
	case <-time.After(2 * time.Second):
	}

	// Inspect the container for metadata
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for i := 0; i < 5; i++ {
		select {
		case <-ctx.Done():
			return DockerCommand{}
		case <-ticker.C:
			cmd := exec.CommandContext(ctx, "docker", "inspect", docker.Name)
			out, err := cmd.Output()
			if err == nil {

				meta := []DockerCommand{}
				err = json.Unmarshal(out, &meta)
				if err == nil && len(meta) == 1 {
					//nolint:errcheck // Metadata logging must not change container discovery.
					_ = docker.Event.Info("container metadata",
						"containerID", meta[0].Id,
						"containerName", meta[0].Name,
						"containerImageHash", meta[0].Image)
					return meta[0]
				}
			}
		}
	}

	return DockerCommand{}
}

func (docker *DockerCommand) GetStdout() io.Writer {
	return docker.Stdout
}

func (docker *DockerCommand) GetStderr() io.Writer {
	return docker.Stderr
}

// SyncDockerAPIVersion ensures that the client uses the same API version as
// the server.
func (docker *DockerCommand) SyncAPIVersion() error {
	if os.Getenv("DOCKER_API_VERSION") == "" {
		var args []string
		driverCmd := strings.Fields(docker.DriverCommand)

		if len(docker.DriverCommand) > 1 {
			// Merge driver parts and command parts
			args = append(args, driverCmd[1:]...)
		}

		args = append(args, "version", "--format", `{"Server": "{{.Server.APIVersion}}", "Client": "{{.Client.APIVersion}}"}`)
		cmd := exec.Command(driverCmd[0], args...)
		out, err := cmd.Output()
		if err != nil {
			return fmt.Errorf("docker version command failed: %v", err)
		}
		version := &DockerVersion{}
		err = json.Unmarshal(out, version)
		if err != nil {
			return fmt.Errorf("failed to unmarshal docker version: %v", err)
		}
		if version.Client != version.Server {
			err := os.Setenv("DOCKER_API_VERSION", version.Server)
			if err != nil {
				return fmt.Errorf("set Docker API version %q: %w", version.Server, err)
			}
		}
	}
	return nil
}
