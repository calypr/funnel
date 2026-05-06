package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"text/template"
	"time"

	"github.com/google/shlex"
	"github.com/ohsu-comp-bio/funnel/events"
)

// DockerCommand is responsible for configuring and running a docker container.
type DockerCommand struct {
	Id              string
	Name            string
	Volumes         []Volume
	Workdir         string
	RemoveContainer bool
	Event           *events.ExecutorWriter
	DriverCommand   string
	RunCommand      string // template string
	PullCommand     string // template string
	StopCommand     string // template string
	EnableTags      bool
	Tags            map[string]string
	Command
}

type DockerVersion struct {
	Client string
	Server string
}

// Run runs the Docker command and blocks until done.
func (docker DockerCommand) Run(ctx context.Context) error {
	// Sync docker API version info.
	err := docker.SyncAPIVersion()
	if err != nil {
		docker.Event.Error("failed to sync docker client API version", err)
	}

	err = docker.executeCommand(ctx, docker.PullCommand, false)
	if err != nil {
		docker.Event.Error("failed to pull docker image using command", err, docker.PullCommand)
	}

	err = docker.executeCommand(ctx, docker.RunCommand, true)
	if err != nil {
		docker.Event.Error("failed to run docker container", err)
	}

	return err
}

// Stop stops the container.
func (docker DockerCommand) Stop() error {
	docker.Event.Info("Stopping container", "container", docker.Name)
	err := docker.executeCommand(context.Background(), docker.StopCommand, false)
	if err != nil {
		docker.Event.Error("failed to stop docker container", err)
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

	cmdParts, err := shlex.Split(cmdBuffer.String())
	if err != nil {
		return fmt.Errorf("failed to parse command: %w", err)
	}

	if usingCommand {
		go docker.InspectContainer(ctx)

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
		docker.Event.Info("Running command", "cmd", cmd.String())
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
	time.Sleep(2 * time.Second)

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
					docker.Event.Info("container metadata",
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
			os.Setenv("DOCKER_API_VERSION", version.Server)
		}
	}
	return nil
}
