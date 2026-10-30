package storage

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestGenericS3DownloadReplacesDestinationAliases(t *testing.T) {
	store := publicationS3(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("list-type") {
			w.Header().Set("Content-Type", "application/xml")
			writeTestResponse(t, w, []byte(`<ListBucketResult><Name>bucket</Name><IsTruncated>false</IsTruncated></ListBucketResult>`))
			return
		}
		w.Header().Set("ETag", `"remote"`)
		w.Header().Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
		w.Header().Set("Content-Length", "6")
		if r.Method == http.MethodGet {
			writeTestResponse(t, w, []byte("remote"))
		}
	})

	for _, aliasType := range []string{"symlink", "hardlink"} {
		t.Run(aliasType, func(t *testing.T) {
			dir := t.TempDir()
			outside := filepath.Join(dir, "outside.txt")
			if err := os.WriteFile(outside, []byte("outside"), 0600); err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(dir, "download.txt")
			switch aliasType {
			case "symlink":
				if err := os.Symlink(outside, destination); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(outside, destination); err != nil {
					t.Fatal(err)
				}
			}

			if _, err := store.Get(context.Background(), "s3://bucket/source.txt", destination); err != nil {
				t.Fatalf("download to aliased destination: %v", err)
			}
			outsideBytes, err := os.ReadFile(outside)
			if err != nil {
				t.Fatal(err)
			}
			if string(outsideBytes) != "outside" {
				t.Fatalf("download changed aliased target: %q", outsideBytes)
			}
			downloaded, err := os.ReadFile(destination)
			if err != nil {
				t.Fatal(err)
			}
			if string(downloaded) != "remote" {
				t.Fatalf("downloaded bytes = %q, want %q", downloaded, "remote")
			}
			if info, err := os.Lstat(destination); err != nil || !info.Mode().IsRegular() {
				t.Fatalf("destination after download = %v, error %v", info, err)
			}
			outsideInfo, err := os.Stat(outside)
			if err != nil {
				t.Fatal(err)
			}
			destinationInfo, err := os.Stat(destination)
			if err != nil {
				t.Fatal(err)
			}
			if os.SameFile(outsideInfo, destinationInfo) {
				t.Fatal("download destination still shares the outside inode")
			}
		})
	}
}

func TestGenericS3DownloadRejectsTrailingSeparator(t *testing.T) {
	store := publicationS3(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("list-type") {
			w.Header().Set("Content-Type", "application/xml")
			writeTestResponse(t, w, []byte(`<ListBucketResult><Name>bucket</Name><IsTruncated>false</IsTruncated></ListBucketResult>`))
			return
		}
		w.Header().Set("ETag", `"remote"`)
		w.Header().Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
		w.Header().Set("Content-Length", "6")
		if r.Method == http.MethodGet {
			writeTestResponse(t, w, []byte("remote"))
		}
	})
	target := filepath.Join(t.TempDir(), "target")
	if _, err := store.Get(context.Background(), "s3://bucket/source", target+string(filepath.Separator)); err == nil {
		t.Fatal("download to a path ending in a separator succeeded")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("download created a destination directory: %v", err)
	}
}
