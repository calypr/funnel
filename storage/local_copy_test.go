package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCopyFileReplacesDestinationContents(t *testing.T) {
	for _, contents := range []string{"ok", ""} {
		name := "shorter file"
		if contents == "" {
			name = "empty file"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			source := filepath.Join(dir, "source")
			destination := filepath.Join(dir, "destination")
			if err := os.WriteFile(source, []byte(contents), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(destination, []byte("old-long-content"), 0600); err != nil {
				t.Fatal(err)
			}

			if err := copyFile(context.Background(), source, destination); err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{source, destination} {
				got, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if string(got) != contents {
					t.Errorf("contents of %s = %q, want %q", path, got, contents)
				}
			}
		})
	}
}
