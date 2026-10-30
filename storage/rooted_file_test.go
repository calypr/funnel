package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteRootFileAtomicallyKeepsDestinationOnCancellation(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	closeTestResource(t, root)
	if err := os.WriteFile(filepath.Join(dir, "target"), []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	err = writeRootFileAtomically(ctx, root, "target", func(file *os.File) error {
		info, err := file.Stat()
		if err != nil {
			return err
		}
		if info.Mode().Perm()&0077 != 0 {
			return fmt.Errorf("staged file permissions = %04o, want no group or other access", info.Mode().Perm())
		}
		if _, err := io.WriteString(file, "new"); err != nil {
			return err
		}
		cancel()
		return nil
	})
	if err == nil {
		t.Fatal("canceled staged write succeeded")
	}
	data, err := os.ReadFile(filepath.Join(dir, "target"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "old" {
		t.Fatalf("destination after cancellation = %q, want %q", data, "old")
	}
	assertNoStagedFiles(t, dir)
}

func TestWriteRootFileAtomicallyRejectsTrailingSeparator(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	closeTestResource(t, root)
	if err := os.Mkdir(filepath.Join(dir, "target"), 0700); err != nil {
		t.Fatal(err)
	}
	err = writeRootFileAtomically(context.Background(), root, "target"+string(filepath.Separator), func(file *os.File) error {
		t.Error("invalid destination reached the writer")
		return nil
	})
	if err == nil {
		t.Fatal("write to a path ending in a separator succeeded")
	}
	entries, err := os.ReadDir(filepath.Join(dir, "target"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("invalid write created files: %v", entries)
	}
}

func TestWriteRootFileAtomicallyKeepsDestinationOnRenameError(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	closeTestResource(t, root)
	destination := filepath.Join(dir, "target")
	if err := os.Mkdir(destination, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "sentinel"), []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}

	err = writeRootFileAtomically(context.Background(), root, "target", func(file *os.File) error {
		_, err := io.WriteString(file, "new")
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "replace destination") {
		t.Fatalf("rename error = %v, want replace destination failure", err)
	}
	data, err := os.ReadFile(filepath.Join(destination, "sentinel"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "old" {
		t.Fatalf("destination after failed rename = %q, want %q", data, "old")
	}
	assertNoStagedFiles(t, dir)
}

func assertNoStagedFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".funnel-stage-") {
			t.Fatalf("staged file remains after failed write: %q", entry.Name())
		}
	}
}

func TestWriteRootFileAtomicallyWithModeUsesRequestedMode(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	closeTestResource(t, root)

	if err := writeRootFileAtomicallyWithMode(context.Background(), root, "target", 0600, func(file *os.File) error {
		_, err := io.WriteString(file, "new")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "target"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("destination permissions = %04o, want %04o", info.Mode().Perm(), 0600)
	}
}
