//go:build linux || darwin

package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

const localFIFOOperationBound = time.Second

func TestLocalRejectsFIFOInputsPromptly(t *testing.T) {
	tests := []struct {
		operation string
		layout    string
	}{
		{operation: "List", layout: "direct"},
		{operation: "List", layout: "nested"},
		{operation: "List", layout: "symlink"},
		{operation: "Get", layout: "direct"},
		{operation: "Get", layout: "nested"},
		{operation: "Get", layout: "symlink"},
		{operation: "Put", layout: "direct"},
		{operation: "Put", layout: "nested"},
		{operation: "Put", layout: "symlink"},
	}
	for _, test := range tests {
		t.Run(test.operation+" "+test.layout, func(t *testing.T) {
			base := t.TempDir()
			allowed := filepath.Join(base, "allowed")
			hostSource := filepath.Join(base, "host-source")
			if err := os.MkdirAll(allowed, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(hostSource, 0755); err != nil {
				t.Fatal(err)
			}
			sourceRoot := allowed
			if test.operation == "Put" {
				sourceRoot = hostSource
			}
			fifo, sourcePath, err := localFIFOLayout(sourceRoot, test.layout)
			if err != nil {
				t.Fatal(err)
			}
			local := &Local{allowedDirs: []string{allowed}}
			destination := filepath.Join(allowed, "uploaded")
			if test.operation == "Get" {
				destination = filepath.Join(base, "downloaded")
			}
			operation := func() error {
				ctx := context.Background()
				if test.operation == "List" {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, 250*time.Millisecond)
					defer cancel()
					_, err := local.List(ctx, allowed)
					return err
				}
				if test.operation == "Get" {
					_, err := local.Get(ctx, sourcePath, destination)
					return err
				}
				_, err := local.Put(ctx, destination, sourcePath)
				return err
			}
			assertLocalFIFOPositiveControl(t, local, test.operation, test.layout, sourcePath, destination, allowed)
			if err := os.Remove(fifo); err != nil {
				t.Fatal(err)
			}
			if err := syscall.Mkfifo(fifo, 0600); err != nil {
				t.Fatal(err)
			}
			assertLocalFIFOErrorPromptly(t, operation, func() (*os.File, error) {
				return openLocalFIFOPeer(fifo, os.O_WRONLY)
			})
		})
	}
}

func TestLocalRejectsFIFODestinationsPromptly(t *testing.T) {
	for _, test := range []struct {
		operation string
		reader    bool
	}{
		{operation: "Get"},
		{operation: "Get", reader: true},
		{operation: "Put"},
		{operation: "Put", reader: true},
	} {
		name := test.operation + " destination without reader"
		if test.reader {
			name = test.operation + " destination with reader"
		}
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			allowed := filepath.Join(base, "allowed")
			hostSource := filepath.Join(base, "host-source")
			if err := os.MkdirAll(allowed, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(hostSource, 0755); err != nil {
				t.Fatal(err)
			}
			local := &Local{allowedDirs: []string{allowed}}
			sourcePath, destination := filepath.Join(allowed, "source"), filepath.Join(base, "destination")
			if test.operation == "Put" {
				sourcePath, destination = filepath.Join(hostSource, "source"), filepath.Join(allowed, "destination")
			}
			if err := os.WriteFile(sourcePath, []byte("payload"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := runLocalStorageTransfer(local, test.operation, sourcePath, destination); err != nil {
				t.Fatalf("regular-file control: %v", err)
			}
			if got, err := os.ReadFile(destination); err != nil || string(got) != "payload" {
				t.Fatalf("regular-file control produced %q, %v", got, err)
			}
			if err := os.Remove(destination); err != nil {
				t.Fatal(err)
			}
			if err := syscall.Mkfifo(destination, 0600); err != nil {
				t.Fatal(err)
			}
			var reader *os.File
			if test.reader {
				var err error
				reader, err = os.OpenFile(destination, os.O_RDONLY|syscall.O_NONBLOCK, 0)
				if err != nil {
					t.Fatal(err)
				}
				defer closeLocalFIFO(t, reader)
			}
			operation := func() error {
				_, err := runLocalStorageTransfer(local, test.operation, sourcePath, destination)
				return err
			}
			assertLocalFIFOErrorPromptly(t, operation, func() (*os.File, error) {
				if reader != nil {
					return nil, nil
				}
				return os.OpenFile(destination, os.O_RDONLY|syscall.O_NONBLOCK, 0)
			})
		})
	}
}

func localFIFOLayout(root, layout string) (fifo, operationPath string, err error) {
	switch layout {
	case "direct":
		fifo, operationPath = filepath.Join(root, "input"), filepath.Join(root, "input")
	case "nested":
		fifo, operationPath = filepath.Join(root, "nested", "input"), root
		if err = os.MkdirAll(filepath.Dir(fifo), 0755); err != nil {
			return
		}
	case "symlink":
		fifo, operationPath = filepath.Join(root, "pipe"), filepath.Join(root, "link")
	default:
		return "", "", fmt.Errorf("unknown FIFO layout %q", layout)
	}
	err = os.WriteFile(fifo, []byte("payload"), 0600)
	if err == nil && layout == "symlink" {
		err = os.Symlink(filepath.Base(fifo), operationPath)
	}
	return
}

func assertLocalFIFOPositiveControl(t *testing.T, local *Local, operation, layout, sourcePath, destination, allowed string) {
	t.Helper()
	if operation == "List" {
		objects, err := local.List(context.Background(), allowed)
		if err != nil || len(objects) == 0 {
			t.Fatalf("regular-file List control returned %d objects, %v", len(objects), err)
		}
		return
	}
	if _, err := runLocalStorageTransfer(local, operation, sourcePath, destination); err != nil {
		t.Fatalf("regular-file %s control: %v", operation, err)
	}
	output := destination
	if layout == "nested" {
		output = filepath.Join(destination, "nested", "input")
	}
	if got, err := os.ReadFile(output); err != nil || string(got) != "payload" {
		t.Fatalf("regular-file %s control produced %q, %v", operation, got, err)
	}
}

func runLocalStorageTransfer(local *Local, operation, sourcePath, destination string) (*Object, error) {
	if operation == "Get" {
		return local.Get(context.Background(), sourcePath, destination)
	}
	return local.Put(context.Background(), destination, sourcePath)
}

func assertLocalFIFOErrorPromptly(t *testing.T, operation func() error, unblock func() (*os.File, error)) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- operation() }()
	timer := time.NewTimer(localFIFOOperationBound)
	defer timer.Stop()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("local storage accepted a FIFO")
		}
	case <-timer.C:
		peer, err := unblock()
		if err != nil {
			t.Fatalf("timed out and could not open the FIFO peer: %v", err)
		}
		if peer != nil {
			defer closeLocalFIFO(t, peer)
		}
		cleanup := time.NewTimer(localFIFOOperationBound)
		defer cleanup.Stop()
		select {
		case err := <-done:
			t.Fatalf("local storage blocked on a FIFO before returning %v", err)
		case <-cleanup.C:
			t.Fatal("local storage did not exit after the FIFO peer opened")
		}
	}
}

func openLocalFIFOPeer(path string, mode int) (*os.File, error) {
	deadline := time.Now().Add(localFIFOOperationBound)
	for {
		file, err := os.OpenFile(path, mode|syscall.O_NONBLOCK, 0)
		if err == nil {
			return file, nil
		}
		if !errors.Is(err, syscall.ENXIO) && !errors.Is(err, syscall.EAGAIN) {
			return nil, err
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("opening FIFO peer timed out: %w", err)
		}
		time.Sleep(time.Millisecond)
	}
}

func closeLocalFIFO(t *testing.T, file *os.File) {
	t.Helper()
	if err := file.Close(); err != nil {
		t.Error(err)
	}
}
