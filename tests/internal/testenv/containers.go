package testenv

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	docker "github.com/moby/moby/client"

	appconfig "github.com/calypr/funnel/internal/config"
)

// WaitForDockerDestroy waits until Docker no longer has the container.
func (f *Funnel) WaitForDockerDestroy(id string) {
	ctx, cancel := context.WithTimeout(context.Background(), dockerDestroyWaitTimeout)
	defer cancel()
	ticker := time.NewTicker(f.rate)
	defer ticker.Stop()

	for {
		_, err := f.Docker.ContainerInspect(ctx, id, docker.ContainerInspectOptions{})
		if cerrdefs.IsNotFound(err) {
			return
		}
		if err != nil {
			panic(fmt.Errorf("failed to inspect Docker container %s while waiting for deletion: %w", id, err))
		}

		select {
		case <-ctx.Done():
			panic(fmt.Errorf("timed out waiting for Docker container %s to be deleted: %w", id, ctx.Err()))
		case <-ticker.C:
		}
	}
}

// StartServerInDocker starts a funnel server in a docker image
func (f *Funnel) StartServerInDocker(containerName, imageName string, extraArgs []string) {
	// Docker publishes the ports itself and cannot inherit host listeners.
	if err := f.closeListeners(); err != nil {
		panic(err)
	}
	var funnelBinary string
	var err error
	gopath := os.Getenv("GOPATH")

	if gopath == "" {
		funnelBinary, err = repositoryBinary()
		if err != nil {
			log.Error("Error finding funnel binary", err)
			panic(err)
		}
	} else {
		if runtime.GOOS == "linux" {
			funnelBinary, err = filepath.Abs(filepath.Join(
				gopath, "bin/", "funnel",
			))
		} else {
			funnelBinary, err = filepath.Abs(filepath.Join(
				gopath, "src/github.com/calypr/funnel/build/release/linux_amd64/funnel",
			))
		}
		if err != nil {
			log.Error("Error finding funnel binary", err)
			panic(err)
		}
	}

	// write config file
	configPath, err := filepath.Abs(filepath.Join(f.Conf.Worker.WorkDir, "config.yml"))
	if err != nil {
		panic(fmt.Errorf("resolve test server config path: %w", err))
	}
	err = appconfig.ToYamlFile(f.Conf, configPath)
	if err != nil {
		panic(fmt.Errorf("write test server config: %w", err))
	}
	err = os.Chmod(configPath, 0644)
	if err != nil {
		panic(fmt.Errorf("set test server config permissions: %w", err))
	}

	httpPort, err := strconv.ParseInt(f.Conf.Server.HTTPPort, 0, 32)
	if err != nil {
		panic(fmt.Errorf("parse test HTTP port: %w", err))
	}
	rpcPort, err := strconv.ParseInt(f.Conf.Server.RPCPort, 0, 32)
	if err != nil {
		panic(fmt.Errorf("parse test RPC port: %w", err))
	}

	// detect gid of /var/run/docker.sock
	fi, err := os.Stat("/var/run/docker.sock")
	if err != nil {
		panic(err)
	}
	gid := fi.Sys().(*syscall.Stat_t).Gid

	// setup docker run cmd
	args := []string{
		"run", "-i", "--rm",
		"--group-add", fmt.Sprintf("%d", gid),
		"--name", containerName,
		"-p", fmt.Sprintf("%d:%d", httpPort, httpPort),
		"-p", fmt.Sprintf("%d:%d", rpcPort, rpcPort),
		"-v", "/var/run/docker.sock:/var/run/docker.sock",
		"-v", fmt.Sprintf("%s:/bin/funnel", funnelBinary),
		"-v", fmt.Sprintf("%s:%s", configPath, "/opt/funnel_config.yml"),
	}
	args = append(args, extraArgs...)
	args = append(args, imageName, "funnel", "server", "run", "--config", "/opt/funnel_config.yml")

	cmd := exec.Command("docker", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// start server
	done := make(chan error, 1)
	go func() {
		done <- cmd.Run()
	}()

	ready := make(chan struct{})
	go func() {
		err := f.PollForServerStart()
		if err != nil {
			log.Error("failed to start funnel server", err)
			panic(err)
		}

		f.addRPCClient()
		close(ready)
	}()

	select {
	case err := <-done:
		log.Error("Error starting funnel server in container", err)
		panic(err)
	case <-ready:
		break
	}
	return
}

func (f *Funnel) killTestServerContainers(ids []string) {
	for _, n := range ids {
		_, err := f.Docker.ContainerStop(context.Background(), strings.TrimPrefix(n, "/"), docker.ContainerStopOptions{})
		if cerrdefs.IsNotFound(err) {
			continue
		}
		if err != nil {
			panic(err)
		}
		f.WaitForDockerDestroy(strings.TrimPrefix(n, "/"))
	}
}

// CleanupTestServerContainer stops the docker container running the test funnel server
func (f *Funnel) CleanupTestServerContainer(containerName string) {
	f.killTestServerContainers([]string{containerName})
	f.Cleanup()
	return
}
