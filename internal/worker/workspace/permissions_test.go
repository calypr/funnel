//go:build unix

package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/calypr/funnel/api/tes"
)

func TestMapTaskPreservesUmask(t *testing.T) {
	if os.Getenv("FUNNEL_TEST_UMASK_CHILD") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestMapTaskPreservesUmask$")
		cmd.Env = append(os.Environ(), "FUNNEL_TEST_UMASK_CHILD=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated permission check failed: %v\n%s", err, output)
		}
		return
	}

	syscall.Umask(0077)
	root := t.TempDir()
	dir := filepath.Join(root, "nested", "directory")
	if err := NewFileMapper(dir).MapTask(&tes.Task{}, "docker"); err != nil {
		t.Fatal(err)
	}
	if err := NewFileMapper(dir).MapTask(&tes.Task{}, "docker"); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "unrelated-file")
	if err := os.WriteFile(file, []byte("test"), 0666); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]os.FileMode{dir: 0700, file: 0600} {
		info, err := os.Stat(name)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s permissions = %04o, want %04o", name, got, want)
		}
	}
}
