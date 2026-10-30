package storage

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"
)

func TestMountpointMountID(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mounts    string
		id        string
		want      bool
		wantError bool
	}{
		{"mount", "41 20 0:12 / /mnt rw - fuse mountpoint-s3 rw", "41", true, false},
		{"bind-subpath", "42 20 0:12 /subdir /task rw shared:2 - fuse mountpoint-s3 rw", "42", true, false},
		{"different-id", "41 20 0:12 / /mnt rw - fuse mountpoint-s3 rw\n42 20 0:13 / /mnt rw - fuse other rw", "42", false, false},
		{"subtype", "41 20 0:12 / /mnt rw - fuse.mountpoint-s3 mountpoint-s3 rw", "41", false, false},
		{"source-prefix", "41 20 0:12 / /mnt rw - fuse mountpoint-s3-other rw", "41", false, false},
		{"non-fuse", "41 20 0:12 / /mnt rw - ext4 mountpoint-s3 rw", "41", false, false},
		{"missing-id", "41 20 0:12 / /mnt rw - fuse mountpoint-s3 rw", "4", false, true},
		{"malformed", "41 20 0:12 / /mnt rw", "41", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := mountpointMountID(strings.NewReader(tc.mounts), tc.id)
			if got != tc.want || (err != nil) != tc.wantError {
				t.Fatalf("mount identity = %v, %v; want %v, error %v", got, err, tc.want, tc.wantError)
			}
		})
	}
	want := errors.New("mountinfo read failed")
	if got, err := mountpointMountID(iotest.ErrReader(want), "41"); got || !errors.Is(err, want) {
		t.Fatalf("failed mountinfo read = %v, %v", got, err)
	}
}

func TestMountpointDirectoryRejectsClosedDescriptor(t *testing.T) {
	dir, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := dir.Close(); err != nil {
		t.Fatal(err)
	}
	if got, err := isMountpointDirectory(dir); got || err == nil {
		t.Fatalf("closed directory identified as Mountpoint: %v, %v", got, err)
	}
}

func TestMountpointPublicationRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	parent, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	closeTestResource(t, parent)
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "target")); err != nil {
		t.Fatal(err)
	}
	err = writeMountpointDownload(context.Background(), parent, "target", func(stage *os.File) error {
		_, err := io.WriteString(stage, "replacement")
		return err
	})
	if err == nil {
		t.Fatal("mounted publication followed destination symlink")
	}
	got, err := os.ReadFile(outside)
	if err != nil || string(got) != "original" {
		t.Fatalf("publication changed symlink target: %q, %v", got, err)
	}
}
