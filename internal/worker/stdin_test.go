package worker

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/calypr/funnel/internal/events"
	"github.com/calypr/funnel/tes"
)

func TestWorkerStdinPreservesMappedInput(t *testing.T) {
	for _, scratch := range []bool{false, true} {
		name := "workdir"
		if scratch {
			name = "scratch"
		}
		t.Run(name, func(t *testing.T) {
			mapper := NewFileMapper(t.TempDir())
			base := mapper.WorkDir
			if scratch {
				mapper.ScratchDir = t.TempDir()
				base = mapper.ScratchDir
			}
			input := filepath.Join(base, "input.txt")
			const content = "input must reach the executor intact\n"
			if err := os.WriteFile(input, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			command := &Command{}
			step := &stepWorker{Command: command, Event: events.NewExecutorWriter("stdin", 0, 0, events.Noop{})}
			if err := (&DefaultWorker{}).openStepLogs(mapper, step, &tes.Executor{Stdin: "/input.txt"}); err != nil {
				t.Fatal(err)
			}
			file := command.Stdin.(*os.File)
			defer func() {
				if err := file.Close(); err != nil {
					t.Error(err)
				}
			}()
			got, err := io.ReadAll(file)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != content {
				t.Fatalf("executor stdin = %q, want %q", got, content)
			}
			saved, err := os.ReadFile(input)
			if err != nil {
				t.Fatal(err)
			}
			if string(saved) != content {
				t.Fatalf("input was changed to %q", saved)
			}
			if _, err := file.WriteString("unexpected write"); err == nil {
				t.Fatal("stdin was opened writable")
			}
		})
	}
}

func TestWorkerStdinRejectsMissingFileAndEscapingSymlink(t *testing.T) {
	for _, symlink := range []bool{false, true} {
		name := "missing"
		if symlink {
			name = "symlink"
		}
		t.Run(name, func(t *testing.T) {
			mapper := NewFileMapper(t.TempDir())
			input := filepath.Join(mapper.WorkDir, "input.txt")
			outside := filepath.Join(t.TempDir(), "outside.txt")
			if symlink {
				if err := os.WriteFile(outside, []byte("unchanged"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, input); err != nil {
					t.Fatal(err)
				}
			}
			command := &Command{}
			step := &stepWorker{Command: command, Event: events.NewExecutorWriter("stdin", 0, 0, events.Noop{})}
			if err := (&DefaultWorker{}).openStepLogs(mapper, step, &tes.Executor{Stdin: "/input.txt"}); err == nil {
				if closeErr := command.Stdin.(*os.File).Close(); closeErr != nil {
					t.Error(closeErr)
				}
				t.Fatal("expected stdin open to fail")
			}
			if symlink {
				got, err := os.ReadFile(outside)
				if err != nil || string(got) != "unchanged" {
					t.Fatalf("outside file = %q, error %v", got, err)
				}
			} else if _, err := os.Stat(input); !os.IsNotExist(err) {
				t.Fatalf("missing stdin was created: %v", err)
			}
		})
	}
}
