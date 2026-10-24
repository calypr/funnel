package storage

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/ohsu-comp-bio/funnel/config"
	"golang.org/x/sys/unix"
)

func TestDownloadMountpoint(t *testing.T) {
	dir := os.Getenv("FUNNEL_MOUNTPOINT_TEST_DIR")
	if dir == "" {
		t.Skip("run tests/storage/mountpoint-download.sh with a disposable Mountpoint mount and S3 endpoint")
	}
	store, err := NewGenericS3(&config.GenericS3Storage{
		Endpoint: os.Getenv("FUNNEL_MOUNTPOINT_TEST_ENDPOINT"),
		Key:      "funnel-test",
		Secret:   "funnel-test-secret",
		Region:   "us-east-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	prefix := time.Now().Format("150405.000000000")
	dir = filepath.Join(dir, prefix)
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	parent, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	if mountpoint, err := isMountpointDirectory(parent); err != nil || !mountpoint {
		t.Fatalf("test directory is not a verified Mountpoint mount: %v", err)
	}
	target := filepath.Join(dir, "download")
	for _, tc := range []struct {
		name string
		body []byte
	}{
		{"new", []byte("first downloaded object")},
		{"smaller-replacement", []byte("small")},
		{"empty-replacement", nil},
		{"multipart", bytes.Repeat([]byte("multipart-body!"), 1<<20)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := prefix + "-" + tc.name
			info, err := store.client.PutObject(ctx, "sources", source, bytes.NewReader(tc.body), int64(len(tc.body)), minio.PutObjectOptions{PartSize: 5 << 20})
			if err != nil {
				t.Fatal(err)
			}
			if tc.name == "multipart" && !strings.Contains(info.ETag, "-") {
				t.Fatalf("source was not uploaded in multiple parts: %q", info.ETag)
			}
			if _, err := store.Get(ctx, "s3://sources/"+source, target); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(target)
			if err != nil || !bytes.Equal(got, tc.body) {
				t.Fatalf("mounted content differs: got %d bytes, want %d, error %v", len(got), len(tc.body), err)
			}
			object, err := store.client.GetObject(ctx, "mounted", os.Getenv("FUNNEL_MOUNTPOINT_TEST_PREFIX")+prefix+"/download", minio.GetObjectOptions{})
			if err != nil {
				t.Fatal(err)
			}
			persisted, readErr := io.ReadAll(object)
			closeErr := object.Close()
			if readErr != nil || closeErr != nil || !bytes.Equal(persisted, tc.body) {
				t.Fatalf("S3 content differs: got %d bytes, want %d, read error %v, close error %v", len(persisted), len(tc.body), readErr, closeErr)
			}
		})
	}
	t.Run("source-failure-preserves-destination", func(t *testing.T) {
		before, err := os.ReadFile(target)
		if err != nil {
			t.Fatal(err)
		}
		if err := download(ctx, store.client, "sources", prefix+"-missing", target, ""); err == nil {
			t.Fatal("missing source download succeeded")
		}
		after, err := os.ReadFile(target)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatalf("failed download changed destination: %v", err)
		}
	})
	t.Run("partial-source-failure-preserves-destination", func(t *testing.T) {
		checkDownloadSourceFailure(t, dir)
	})
	t.Run("rejects-mounted-temporary-directory", func(t *testing.T) {
		before, err := os.ReadFile(target)
		if err != nil {
			t.Fatal(err)
		}
		t.Setenv("TMPDIR", dir)
		err = writeDownloadedFile(ctx, target, func(stage *os.File) error {
			t.Fatal("download bytes reached a stage on the mounted filesystem")
			return nil
		})
		if err == nil {
			t.Fatal("download accepted a Mountpoint temporary directory")
		}
		after, err := os.ReadFile(target)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatalf("invalid temporary directory changed destination: %v", err)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".funnel-download-") {
				t.Fatalf("local download stage remains: %q", entry.Name())
			}
		}
	})
	t.Run("rejects-overmounted-destination", func(t *testing.T) {
		target := filepath.Join(dir, "overmounted")
		if err := os.WriteFile(target, []byte("mounted"), 0600); err != nil {
			t.Fatal(err)
		}
		outside := filepath.Join(t.TempDir(), "outside")
		if err := os.WriteFile(outside, []byte("outside"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := unix.Mount(outside, target, "", unix.MS_BIND, ""); err != nil {
			t.Fatal(err)
		}
		defer unix.Unmount(target, 0)
		err := writeDownloadedFile(ctx, target, func(stage *os.File) error {
			_, err := io.WriteString(stage, "replacement")
			return err
		})
		if err == nil {
			t.Fatal("download crossed a destination mount")
		}
		got, err := os.ReadFile(outside)
		if err != nil || string(got) != "outside" {
			t.Fatalf("download changed overmounted file: %q, %v", got, err)
		}
	})
}
