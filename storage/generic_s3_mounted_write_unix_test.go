//go:build linux || darwin

package storage

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestMountedWriteRejectsFIFOWithoutBlocking(t *testing.T) {
	for _, kind := range []string{"source", "destination"} {
		t.Run(kind, func(t *testing.T) {
			mux, _, root := mountedWriterFixture(t, func(w http.ResponseWriter, r *http.Request) {
				t.Error("FIFO reached remote verification")
				w.WriteHeader(403)
			})
			source := publicationFile(t, "data")
			fifo := filepath.Join(root, "fifo")
			if err := syscall.Mkfifo(fifo, 0600); err != nil {
				t.Fatal(err)
			}
			destination := "s3://bucket/results/output"
			if kind == "source" {
				source = fifo
			} else {
				destination = "s3://bucket/results/fifo"
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			result := make(chan error, 1)
			go func() { _, err := mux.Put(ctx, destination, source); result <- err }()
			select {
			case err := <-result:
				if err == nil {
					t.Fatal("FIFO accepted")
				}
			case <-time.After(250 * time.Millisecond):
				unblock, err := os.OpenFile(fifo, os.O_RDWR|syscall.O_NONBLOCK, 0600)
				if err != nil {
					t.Fatal(err)
				}
				defer unblock.Close()
				select {
				case <-result:
				case <-time.After(time.Second):
					t.Error("FIFO operation remained blocked")
				}
				t.Fatal("FIFO open ignored timeout")
			}
		})
	}
}
