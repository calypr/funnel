package worker

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/funnel/internal/events"
	"github.com/calypr/funnel/internal/worker/workspace"
)

func TestDockerTemplatePreservesTaskArgumentBoundaries(t *testing.T) {
	for _, template := range []string{
		`run --name {{.Name}} --workdir {{.Workdir}} {{range .Volumes}}--volume {{.HostPath}}:{{.ContainerPath}}:ro {{end}}{{.GetEnvArgs}} {{.Image}}`,
		`run --name "{{.Name}}" --workdir '{{.Workdir}}' {{range .Volumes}}--volume "{{.HostPath}}:{{.ContainerPath}}:ro" {{end}}{{.GetEnvArgs}} "{{.Image}}"`,
	} {
		t.Run(template, func(t *testing.T) {
			driver, capture := dockerArgsRecorder(t)
			command := DockerCommand{
				DriverCommand: driver,
				Name:          "task --privileged",
				Workdir:       `/work 'quoted' "double" --privileged`,
				Volumes:       []workspace.Volume{{HostPath: `/host 'quoted' "double"`, ContainerPath: `/input --privileged`, Readonly: true}},
				Command:       Command{Image: "alpine", Env: map[string]string{"X --privileged --env Y": `value 'quoted' "double" $HOME`}},
			}
			if err := command.executeCommand(context.Background(), template, false); err != nil {
				t.Fatalf("executeCommand: %v", err)
			}
			raw, err := os.ReadFile(capture)
			if err != nil {
				t.Fatal(err)
			}
			got := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
			want := []string{"run", "--name", command.Name, "--workdir", command.Workdir, "--volume", command.Volumes[0].HostPath + ":" + command.Volumes[0].ContainerPath + ":ro", "--env", `X --privileged --env Y=value 'quoted' "double" $HOME`, "alpine"}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("driver argv = %#v; want %#v", got, want)
			}
		})
	}
}

func TestDockerRejectsOptionLikeImageBeforeDriver(t *testing.T) {
	for _, image := range []string{"--privileged", "alpine --privileged", "alpine\n--privileged"} {
		t.Run(image, func(t *testing.T) {
			driver, capture := dockerArgsRecorder(t)
			command := DockerCommand{DriverCommand: driver, Command: Command{Image: image}}
			if err := command.executeCommand(t.Context(), "pull {{.Image}}", false); err == nil {
				t.Fatal("accepted image that can become runtime options")
			}
			if _, err := os.Stat(capture); !os.IsNotExist(err) {
				t.Fatalf("driver was invoked: %v", err)
			}
		})
	}
}

func TestDockerCustomEnvironmentAndLabelTemplatesKeepValuesLiteral(t *testing.T) {
	driver, capture := dockerArgsRecorder(t)
	command := DockerCommand{
		DriverCommand: driver,
		Tags:          map[string]string{`tag --privileged`: `value ' " --privileged`},
		Command:       Command{Image: "alpine", Env: map[string]string{`X --privileged`: `value ' " --privileged`}},
	}
	template := `run {{range $k, $v := .Env}}--env "{{$k}}={{$v}}" {{end}}{{range $k, $v := .Tags}}--label '{{$k}}={{$v}}' {{end}}{{.GetImage}}`
	if err := command.executeCommand(t.Context(), template, false); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	want := []string{"run", "--env", `X --privileged=value ' " --privileged`, "--label", `tag --privileged=value ' " --privileged`, "alpine"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %#v; want %#v", got, want)
	}
}

func TestDockerExecutorCommandArgumentsRemainLiteral(t *testing.T) {
	driver, capture := dockerArgsRecorder(t)
	command := DockerCommand{DriverCommand: driver, Event: events.NewExecutorWriter("args", 0, 0, events.Noop{}), Command: Command{Image: "alpine", ShellCommand: []string{"printf", "%s", "--privileged two words"}}}
	if err := command.executeCommand(t.Context(), `run {{.Image}} {{.Command}}`, false); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	want := []string{"run", "alpine", "printf", "%s", "--privileged two words"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %#v; want %#v", got, want)
	}
}

func TestDockerTemplateDoesNotReinterpretPlaceholderLikeValues(t *testing.T) {
	driver, capture := dockerArgsRecorder(t)
	const literal = "__FUNNEL_ARG_616c70696e65__"
	command := DockerCommand{
		DriverCommand: driver, EnableTags: true, Tags: map[string]string{"key": literal},
		Command: Command{Image: "alpine", Env: map[string]string{"X": literal}},
	}
	if err := command.executeCommand(t.Context(), `run {{.GetEnvArgs}} {{.GetTagArgs}} {{.Image}}`, false); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	want := []string{"run", "--env", "X=" + literal, "--label", "key=" + literal, "alpine"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %#v; want %#v", got, want)
	}
}

func TestDockerNestedCommandFieldsStayLiteral(t *testing.T) {
	driver, capture := dockerArgsRecorder(t)
	command := DockerCommand{DriverCommand: driver, Command: Command{
		Image: "alpine", Workdir: `/work 'quoted' --privileged`,
		ShellCommand: []string{"printf", "%s", `value ' " --privileged`},
	}}
	template := `run --workdir {{.Command.Workdir}} {{.Command.Image}} {{range .Command.ShellCommand}}'{{.}}' {{end}}`
	if err := command.executeCommand(t.Context(), template, false); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	want := []string{"run", "--workdir", command.Command.Workdir, "alpine", "printf", "%s", `value ' " --privileged`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %#v; want %#v", got, want)
	}
}
