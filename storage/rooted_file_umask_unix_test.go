//go:build unix

package storage

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestWriteRootFileAtomicallyHonorsUmask(t *testing.T) {
	const childKey = "FUNNEL_ROOTED_FILE_UMASK_CHILD"
	const dirKey = "FUNNEL_ROOTED_FILE_UMASK_DIR"
	if os.Getenv(childKey) == "1" {
		syscall.Umask(0077)
		dir := os.Getenv(dirKey)
		root, err := os.OpenRoot(dir)
		if err != nil {
			t.Fatal(err)
		}
		closeTestResource(t, root)
		if err := writeRootFileAtomically(context.Background(), root, "created", func(file *os.File) error {
			_, err := io.WriteString(file, "new")
			return err
		}); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(filepath.Join(dir, "created"))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatalf("created file permissions = %04o, want %04o", info.Mode().Perm(), 0600)
		}
		return
	}

	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestWriteRootFileAtomicallyHonorsUmask$")
	cmd.Env = append(os.Environ(), childKey+"=1", dirKey+"="+dir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("umask child failed: %v\n%s", err, output)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "created" {
		t.Fatalf("umask child left files %v", strings.Join(entryNames(entries), ", "))
	}
}

func entryNames(entries []os.DirEntry) []string {
	names := make([]string, len(entries))
	for i, entry := range entries {
		names[i] = entry.Name()
	}
	return names
}
