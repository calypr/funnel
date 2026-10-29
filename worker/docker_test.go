package worker

import (
	"bytes"
	"context"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"text/template"
	"time"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/logger"
	"github.com/calypr/funnel/tes"
)

var command = Command{
	Image:        "alpine",
	ShellCommand: []string{"sh", "-c", "echo Hello, World!"},
}

var docker = DockerCommand{
	Id:              "123",
	Name:            "funnel-test-" + RandomString(6),
	Command:         command,
	DriverCommand:   "docker",
	RunCommand:      "run --name {{.Name}} {{.Image}} {{.Command}}",
	PullCommand:     "pull {{.Image}}",
	RemoveContainer: true,
	Event: events.NewExecutorWriter("123", 1, 1, &events.Logger{
		Log: logger.NewLogger("test", logger.DefaultConfig()),
	}),
}

func TestDockerRun(t *testing.T) {
	err := docker.Run(context.Background())
	if err != nil {
		t.Errorf("Expected no error, but got: %v", err)
	}
}

func TestDockerExecuteCommand(t *testing.T) {
	err := docker.executeCommand(context.Background(), "run --rm alpine echo Hello, World!", true)
	if err != nil {
		t.Errorf("Expected no error, but got: %v", err)
	}
}

func TestDockerStop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	// Run the container command in a separate goroutine
	go func() {
		err := docker.executeCommand(ctx, "run --rm alpine sleep 30", true)
		if err != nil && ctx.Err() == nil {
			t.Errorf("Expected no error, but got: %v", err)
		}
	}()

	// Give the container some time to start
	time.Sleep(2 * time.Second)

	// Stop the container
	err := docker.Stop()
	if err != nil {
		t.Errorf("Expected no error, but got: %v", err)
	}

	// Cancel the context to stop the goroutine if it is still running
	cancel()
}

func TestFormatVolumeArg(t *testing.T) {
	volume := Volume{
		HostPath:      "/path/to/source",
		ContainerPath: "/path/to/destination",
		Readonly:      true,
	}
	expected := "/path/to/source:/path/to/destination:ro"
	result := formatVolumeArg(volume)
	if result != expected {
		t.Errorf("Expected %s, but got %s", expected, result)
	}
}

func TestDockerNeedsTmpfs(t *testing.T) {
	cases := []struct {
		name    string
		volumes []Volume
		want    bool
	}{
		{name: "no volumes", want: true},
		{name: "explicit tmp", volumes: []Volume{{ContainerPath: "/tmp"}}, want: false},
		{name: "normalized tmp", volumes: []Volume{{ContainerPath: "/var/../tmp"}}, want: false},
		{name: "ancestor", volumes: []Volume{{ContainerPath: "/"}}, want: false},
		{name: "tmp child", volumes: []Volume{{ContainerPath: "/tmp/data"}}, want: true},
		{name: "segment lookalike", volumes: []Volume{{ContainerPath: "/tmp-files"}}, want: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := (DockerCommand{Volumes: tc.volumes}).NeedsTmpfs()
			if got != tc.want {
				t.Fatalf("NeedsTmpfs() = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestDefaultDockerRunCommandTmpfs(t *testing.T) {
	runCommand := config.DefaultConfig().Worker.Container.RunCommand
	tmpl, err := template.New("run-command").Parse(runCommand)
	if err != nil {
		t.Fatal("parsing default run command:", err)
	}

	render := func(command DockerCommand) string {
		t.Helper()
		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, command); err != nil {
			t.Fatal("rendering default run command:", err)
		}
		return buf.String()
	}

	if got := render(DockerCommand{}); !strings.Contains(got, "--tmpfs /tmp") {
		t.Fatalf("default run command does not provide /tmp tmpfs: %q", got)
	}

	explicitTmp := DockerCommand{Volumes: []Volume{{HostPath: "/host/tmp", ContainerPath: "/tmp"}}}
	if got := render(explicitTmp); strings.Contains(got, "--tmpfs /tmp") {
		t.Fatalf("explicit /tmp volume should suppress tmpfs: %q", got)
	}
}

func TestDefaultDockerRunCommandResourceLimits(t *testing.T) {
	tmpl, err := template.New("run-command").Parse(config.DefaultConfig().Worker.Container.RunCommand)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		command DockerCommand
		want    []string
		absent  []string
	}{
		{name: "disabled", command: DockerCommand{Resources: &tes.Resources{CpuCores: 2, RamGb: 1.5}}, absent: []string{"--cpus", "--memory"}},
		{name: "enabled", command: DockerCommand{EnforceResources: true, Resources: &tes.Resources{CpuCores: 2, RamGb: 1.5}}, want: []string{"--cpus 2", "--memory 1536m", "--memory-swap 1536m"}},
		{name: "cpu only", command: DockerCommand{EnforceResources: true, Resources: &tes.Resources{CpuCores: 1}}, want: []string{"--cpus 1"}, absent: []string{"--memory"}},
		{name: "memory only", command: DockerCommand{EnforceResources: true, Resources: &tes.Resources{RamGb: 1}}, want: []string{"--memory 1024m", "--memory-swap 1024m"}, absent: []string{"--cpus"}},
		{name: "no resources", command: DockerCommand{EnforceResources: true}, absent: []string{"--cpus", "--memory"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b bytes.Buffer
			if err := tmpl.Execute(&b, tt.command); err != nil {
				t.Fatal(err)
			}
			for _, flag := range tt.want {
				if !strings.Contains(b.String(), flag) {
					t.Errorf("missing %q in %q", flag, b.String())
				}
			}
			for _, flag := range tt.absent {
				if strings.Contains(b.String(), flag) {
					t.Errorf("unexpected %q in %q", flag, b.String())
				}
			}
			if strings.Contains(b.String(), "--gpus") {
				t.Errorf("unexpected GPU flag without a GPU request in %q", b.String())
			}
		})
	}
}

func TestDockerResourceLimitConfig(t *testing.T) {
	conf := config.DefaultConfig()
	if conf.Worker.Container.EnforceResources {
		t.Fatal("resource enforcement must be disabled by default")
	}
	if err := config.Parse([]byte("Worker:\n  Container:\n    EnforceResources: true\n"), conf); err != nil {
		t.Fatal(err)
	}
	if !conf.Worker.Container.EnforceResources {
		t.Fatal("resource enforcement was not enabled")
	}
}

func TestDockerGetImage(t *testing.T) {
	expected := "alpine"
	result := docker.GetImage()
	if result != expected {
		t.Errorf("Expected %s, but got %s", expected, result)
	}
}

func TestDockerGPUArgs(t *testing.T) {
	tests := []struct {
		name           string
		resources      *tes.Resources
		computeBackend string
		batchJobID     string
		visibleDevices string
		want           string
		wantErr        bool
	}{
		{name: "no GPU request"},
		{name: "local request", resources: gpuResources("2"), computeBackend: "local", want: "--gpus 2"},
		{name: "case insensitive count key", resources: &tes.Resources{BackendParameters: map[string]string{"GPU_COUNT": "2"}}, computeBackend: "local", want: "--gpus 2"},
		{name: "count key collision", resources: &tes.Resources{BackendParameters: map[string]string{"gpu_count": "1", "GPU_COUNT": "2"}}, computeBackend: "local", wantErr: true},
		{name: "local explicit devices", resources: gpuDeviceResources("2", "1,GPU-0123456789abcdef0123456789abcdef"), computeBackend: "local", want: `--gpus '"device=1,GPU-0123456789abcdef0123456789abcdef"'`},
		{name: "local devices infer count", resources: gpuDeviceResources("", "1,3"), computeBackend: "local", want: `--gpus '"device=1,3"'`},
		{name: "AWS Batch assigned device", resources: gpuResources("1"), computeBackend: "aws-batch", batchJobID: "batch-1", visibleDevices: "3", want: `--gpus '"device=3"'`},
		{name: "AWS Batch assigned devices", resources: gpuResources("2"), computeBackend: "aws-batch", batchJobID: "batch-1", visibleDevices: "3,0", want: `--gpus '"device=3,0"'`},
		{name: "AWS job definition allocated devices without count", computeBackend: "aws-batch", batchJobID: "batch-1", visibleDevices: "3,0", want: `--gpus '"device=3,0"'`},
		{name: "AWS no-GPU allocation sentinel", computeBackend: "aws-batch", batchJobID: "batch-1", visibleDevices: "all", want: ""},
		{name: "AWS missing allocation without count", computeBackend: "aws-batch", batchJobID: "batch-1", want: ""},
		{name: "AWS environment with wrong compute backend", resources: gpuResources("1"), computeBackend: "local", batchJobID: "batch-1", visibleDevices: "0", wantErr: true},
		{name: "unsupported compute backend", resources: gpuResources("1"), computeBackend: "manual", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AWS_BATCH_JOB_ID", tt.batchJobID)
			t.Setenv("NVIDIA_VISIBLE_DEVICES", tt.visibleDevices)
			command := DockerCommand{Resources: tt.resources, ComputeBackend: tt.computeBackend}
			got, err := command.GPUArgs()
			if (err != nil) != tt.wantErr {
				t.Fatalf("GPUArgs() error = %v, wantErr %t", err, tt.wantErr)
			}
			if err == nil && got != tt.want {
				t.Fatalf("GPUArgs() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDockerGPUArgsRejectsInvalidCounts(t *testing.T) {
	for _, count := range []string{"", "0", "-1", "+1", "1.5", "1e2", "18446744073709551616"} {
		t.Run(count, func(t *testing.T) {
			command := DockerCommand{Resources: gpuResources(count), ComputeBackend: "local"}
			if _, err := command.GPUArgs(); err == nil {
				t.Fatalf("GPUArgs() accepted invalid gpu_count %q", count)
			}
		})
	}
}

func TestDockerGPUArgsRejectsInvalidAWSAllocations(t *testing.T) {
	tests := []struct {
		name    string
		count   string
		devices string
	}{
		{name: "missing", count: "1"},
		{name: "all", count: "1", devices: "all"},
		{name: "none", count: "1", devices: "none"},
		{name: "void", count: "1", devices: "void"},
		{name: "empty entry", count: "2", devices: "0,"},
		{name: "whitespace", count: "2", devices: "0, 1"},
		{name: "duplicate", count: "2", devices: "0,0"},
		{name: "invalid identifier", count: "1", devices: "GPU-xyz"},
		{name: "too few", count: "2", devices: "0"},
		{name: "too many", count: "1", devices: "0,1"},
		{name: "malformed UUID", count: "1", devices: "GPU----"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AWS_BATCH_JOB_ID", "batch-1")
			t.Setenv("NVIDIA_VISIBLE_DEVICES", tt.devices)
			command := DockerCommand{Resources: gpuResources(tt.count), ComputeBackend: "aws-batch"}
			if _, err := command.GPUArgs(); err == nil {
				t.Fatalf("GPUArgs() accepted invalid NVIDIA_VISIBLE_DEVICES %q", tt.devices)
			}
		})
	}
}

func TestDockerExecuteCommandForwardsAllocatedGPUArguments(t *testing.T) {
	for _, tc := range []struct {
		name           string
		computeBackend string
		batchJobID     string
		visibleDevices string
		gpuDevices     string
		count          string
		wantGPUArg     string
	}{
		{name: "local count", computeBackend: "local", count: "2", wantGPUArg: "2"},
		{name: "local explicit devices", computeBackend: "local", count: "2", gpuDevices: "2,0", wantGPUArg: `"device=2,0"`},
		{name: "AWS allocated devices", computeBackend: "aws-batch", batchJobID: "batch-1", visibleDevices: "4,1", count: "2", wantGPUArg: `"device=4,1"`},
		{name: "AWS job definition allocation without count", computeBackend: "aws-batch", batchJobID: "batch-1", visibleDevices: "4,1", wantGPUArg: `"device=4,1"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AWS_BATCH_JOB_ID", tc.batchJobID)
			t.Setenv("NVIDIA_VISIBLE_DEVICES", tc.visibleDevices)
			t.Setenv("CUDA_VISIBLE_DEVICES", "")
			var resources *tes.Resources
			if tc.count != "" {
				resources = gpuResources(tc.count)
			}
			if tc.gpuDevices != "" {
				resources = gpuDeviceResources(tc.count, tc.gpuDevices)
			}
			driver, argsFile := dockerArgsRecorder(t)
			command := DockerCommand{
				DriverCommand:  driver,
				RunCommand:     "run {{.GPUArgs}} alpine",
				Resources:      resources,
				ComputeBackend: tc.computeBackend,
			}
			if err := command.executeCommand(context.Background(), command.RunCommand, false); err != nil {
				t.Fatalf("executeCommand() error = %v", err)
			}
			got, err := os.ReadFile(argsFile)
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"run", "--gpus", tc.wantGPUArg, "alpine"}
			if gotArgs := strings.Split(strings.TrimSuffix(string(got), "\n"), "\n"); !equalStrings(gotArgs, want) {
				t.Fatalf("driver args = %#v, want %#v", gotArgs, want)
			}
		})
	}
}

func TestDockerRunRejectsUnallocatedGPURequestBeforeDriver(t *testing.T) {
	for _, computeBackend := range []string{"aws-batch", "slurm", "pbs", "gridengine", "htcondor", "manual"} {
		t.Run(computeBackend, func(t *testing.T) {
			t.Setenv("AWS_BATCH_JOB_ID", "")
			t.Setenv("NVIDIA_VISIBLE_DEVICES", "")
			driver, argsFile := dockerArgsRecorder(t)
			command := DockerCommand{
				DriverCommand:  driver,
				PullCommand:    "pull alpine",
				RunCommand:     "run {{.GPUArgs}} alpine",
				Resources:      gpuResources("1"),
				ComputeBackend: computeBackend,
			}
			if err := command.Run(context.Background()); err == nil {
				t.Fatal("Run() accepted a GPU request without an allocated device")
			}
			if _, err := os.Stat(argsFile); !os.IsNotExist(err) {
				t.Fatalf("driver was invoked before allocation validation: stat error = %v", err)
			}
		})
	}
}

func TestDockerRunRejectsGPURequestWithoutTemplatePlaceholder(t *testing.T) {
	driver, argsFile := dockerArgsRecorder(t)
	command := DockerCommand{
		DriverCommand:  driver,
		PullCommand:    "pull alpine",
		RunCommand:     "run alpine",
		Resources:      gpuResources("1"),
		ComputeBackend: "local",
	}
	if err := command.Run(context.Background()); err == nil {
		t.Fatal("Run() accepted a GPU request without {{.GPUArgs}} in the run template")
	}
	if _, err := os.Stat(argsFile); !os.IsNotExist(err) {
		t.Fatalf("driver was invoked before rejecting the custom template: stat error = %v", err)
	}
}

func gpuResources(count string) *tes.Resources {
	return &tes.Resources{BackendParameters: map[string]string{"gpu_count": count}}
}

func gpuDeviceResources(count, devices string) *tes.Resources {
	parameters := map[string]string{"gpu_devices": devices}
	if count != "" {
		parameters["gpu_count"] = count
	}
	return &tes.Resources{BackendParameters: parameters}
}

func dockerArgsRecorder(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	driver := filepath.Join(dir, "driver")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + shellEscape(argsFile) + "\n"
	if err := os.WriteFile(driver, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return driver, argsFile
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestDockerInspectContainer(t *testing.T) {
	config := docker.InspectContainer(context.Background())
	if config.Id == "" {
		t.Errorf("Expected non-nil container config")
	}
}

func TestDockerSyncAPIVersion(t *testing.T) {
	err := docker.SyncAPIVersion()
	if err != nil {
		t.Errorf("Expected no error, but got: %v", err)
	}
}

// RandomString generates a random string of length n
func RandomString(n int) string {
	var letterRunes = []rune("abcdefghijklmnopqrstuvwxyz0123456789")
	b := make([]rune, n)
	for i := range b {
		b[i] = letterRunes[rand.Intn(len(letterRunes))]
	}
	return string(b)
}

func TestSlurmGPUAllocationReachesDocker(t *testing.T) {
	t.Setenv("AWS_BATCH_JOB_ID", "")
	t.Setenv("FUNNEL_GPU_DEVICES", "7,2")
	driver, argsFile := dockerArgsRecorder(t)
	command := DockerCommand{DriverCommand: driver, RunCommand: "run {{.GPUArgs}} alpine", ComputeBackend: "slurm"}
	if err := command.executeCommand(context.Background(), command.RunCommand, false); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"run", "--gpus", `"device=7,2"`, "alpine"}
	if gotArgs := strings.Split(strings.TrimSuffix(string(got), "\n"), "\n"); !equalStrings(gotArgs, want) {
		t.Fatalf("args=%v want=%v", gotArgs, want)
	}
}

func TestSlurmGPUAllocationRequiresSlurmWorker(t *testing.T) {
	t.Setenv("FUNNEL_GPU_DEVICES", "7,2")
	t.Setenv("AWS_BATCH_JOB_ID", "")
	if _, err := (DockerCommand{ComputeBackend: "local"}).GPUArgs(); err == nil {
		t.Fatal("local worker ignored Slurm allocation")
	}
	t.Setenv("AWS_BATCH_JOB_ID", "batch-job")
	if _, err := (DockerCommand{ComputeBackend: "slurm"}).GPUArgs(); err == nil {
		t.Fatal("AWS worker accepted Slurm allocation")
	}
}
