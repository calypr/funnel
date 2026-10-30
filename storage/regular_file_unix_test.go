//go:build unix

package storage

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestOpenRegularFileRejectsFIFOWithoutBlocking(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "input")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	opened := make(chan error, 1)
	go func() {
		file, err := openRegularFile(fifo)
		if file != nil {
			if closeErr := file.Close(); closeErr != nil {
				err = errors.Join(err, closeErr)
			}
		}
		opened <- err
	}()
	select {
	case err := <-opened:
		if err == nil {
			t.Fatal("FIFO was accepted as a regular file")
		}
	case <-time.After(time.Second):
		t.Fatal("opening FIFO blocked")
	}
}

func TestOpenRootRegularFileAllowsInRootSymlinkAndRejectsFIFO(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "regular"), []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("regular", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(dir, "fifo"), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	closeTestResource(t, root)
	file, err := openRootRegularFile(root, "link")
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(file)
	if err != nil || string(data) != "data" {
		t.Fatalf("in-root symlink descriptor read = %q, %v", data, err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	opened := make(chan error, 1)
	go func() {
		file, err := openRootRegularFile(root, "fifo")
		if file != nil {
			if closeErr := file.Close(); closeErr != nil {
				err = errors.Join(err, closeErr)
			}
		}
		opened <- err
	}()
	select {
	case err := <-opened:
		if err == nil {
			t.Fatal("FIFO was accepted as a regular mounted input")
		}
	case <-time.After(time.Second):
		t.Fatal("opening mounted FIFO blocked")
	}
}
