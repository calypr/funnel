package worker

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/calypr/funnel/internal/worker/workspace"
)

func TestCreateHostFileRejectsEscapingSymlink(t *testing.T) {
	for _, scratch := range []bool{false, true} {
		for _, parent := range []bool{false, true} {
			t.Run(logConfinementCaseName(scratch, parent), func(t *testing.T) {
				mapper := workspace.NewFileMapper(t.TempDir())
				base := mapper.WorkDir
				if scratch {
					mapper.ScratchDir = t.TempDir()
					base = mapper.ScratchDir
				}
				outside := t.TempDir()
				target := filepath.Join(outside, "stdout")
				if err := os.WriteFile(target, []byte("unchanged"), 0600); err != nil {
					t.Fatal(err)
				}
				link := filepath.Join(base, "stdout")
				dest := target
				src := "/stdout"
				if parent {
					link = filepath.Join(base, "logs")
					dest = outside
					src = "/logs/stdout"
				}
				if err := os.Symlink(dest, link); err != nil {
					t.Fatal(err)
				}
				f, err := mapper.CreateHostFile(src)
				if err == nil {
					if err := f.Close(); err != nil {
						t.Error(err)
					}
					t.Error("escaping output log symlink accepted")
				}
				got, err := os.ReadFile(target)
				if err != nil || string(got) != "unchanged" {
					t.Fatalf("outside file = %q, error %v", got, err)
				}
			})
		}
	}
}

func logConfinementCaseName(scratch, parent bool) string {
	name := "workdir"
	if scratch {
		name = "scratch"
	}
	if parent {
		name += "/parent"
	} else {
		name += "/file"
	}
	return name
}
