package worker

import (
	"bytes"
	"context"
	"math/rand"
	"strings"
	"testing"
	"text/template"
	"time"

	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/events"
	"github.com/ohsu-comp-bio/funnel/logger"
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

func TestDockerGetImage(t *testing.T) {
	expected := "alpine"
	result := docker.GetImage()
	if result != expected {
		t.Errorf("Expected %s, but got %s", expected, result)
	}
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
