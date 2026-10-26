package storage

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteDownloadedFileKeepsDestinationOnSourceFailure(t *testing.T) {
	checkDownloadSourceFailure(t, t.TempDir())
}

func checkDownloadSourceFailure(t *testing.T, dir string) {
	t.Helper()
	for _, failure := range []string{"read-error", "cancelled"} {
		t.Run(failure, func(t *testing.T) {
			temporary := t.TempDir()
			t.Setenv("TMPDIR", temporary)
			target := filepath.Join(dir, "preserve-"+failure)
			if err := os.WriteFile(target, []byte("original"), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			sourceErr := errors.New("source read failed")
			want := sourceErr
			if failure == "cancelled" {
				want = context.Canceled
			}
			err := writeDownloadedFile(ctx, target, func(stage *os.File) error {
				info, err := stage.Stat()
				if err != nil {
					t.Fatal(err)
				}
				if info.Mode().Perm() != 0600 {
					t.Fatalf("stage permissions = %04o, want 0600", info.Mode().Perm())
				}
				if _, err := io.WriteString(stage, "partial"); err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(target)
				if err != nil || string(data) != "original" {
					t.Fatalf("destination changed before staging finished: %q, %v", data, err)
				}
				if failure == "cancelled" {
					cancel()
					return nil
				}
				return sourceErr
			})
			if !errors.Is(err, want) {
				t.Fatalf("download error = %v, want %v", err, want)
			}
			data, err := os.ReadFile(target)
			if err != nil || string(data) != "original" {
				t.Fatalf("destination changed after failed staging: %q, %v", data, err)
			}
			stages, err := os.ReadDir(temporary)
			if err != nil {
				t.Fatal(err)
			}
			if len(stages) != 0 {
				t.Fatalf("local download stages remain: %v", stages)
			}
			assertNoStagedFiles(t, dir)
		})
	}
}

func TestWriteRootDownloadedFileRejectsNonBasename(t *testing.T) {
	parent, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	for _, name := range []string{"", ".", "..", "../outside", "nested/file", "file/"} {
		t.Run(name, func(t *testing.T) {
			err := writeRootDownloadedFile(context.Background(), parent, name, func(*os.File) error {
				t.Fatal("invalid destination reached writer")
				return nil
			})
			if err == nil {
				t.Fatal("invalid basename accepted")
			}
		})
	}
}

func TestWriteRootDownloadedFilePinsParent(t *testing.T) {
	dir := t.TempDir()
	original := filepath.Join(dir, "parent")
	moved := filepath.Join(dir, "moved")
	if err := os.Mkdir(original, 0700); err != nil {
		t.Fatal(err)
	}
	parent, err := os.OpenRoot(original)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	err = writeRootDownloadedFile(context.Background(), parent, "target", func(stage *os.File) error {
		if err := os.Rename(original, moved); err != nil {
			return err
		}
		if err := os.Mkdir(original, 0700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(original, "target"), []byte("outside"), 0600); err != nil {
			return err
		}
		_, err := io.WriteString(stage, "downloaded")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{
		filepath.Join(original, "target"): "outside",
		filepath.Join(moved, "target"):    "downloaded",
	} {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Fatalf("contents at %q = %q, want %q, error %v", path, got, want, err)
		}
	}
}
