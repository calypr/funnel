package worker

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/events"
	"github.com/ohsu-comp-bio/funnel/storage"
	"github.com/ohsu-comp-bio/funnel/tes"
)

func TestUploadOutputsReplacesMountedDestinationWithoutChangingAliases(t *testing.T) {
	const size = 4 << 20
	root := t.TempDir()
	target := filepath.Join(root, "shared")
	alias := filepath.Join(root, "alias")
	if err := os.WriteFile(target, []byte("original inode"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(target, alias); err != nil {
		t.Fatal(err)
	}

	mux, _ := workerMountedOutputMux(t, root, 10*time.Second, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Errorf("unexpected S3 request %s", r.Method)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		info, err := os.Stat(target)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
		w.Header().Set("ETag", `"mounted"`)
		w.Header().Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
	}))

	sources := t.TempDir()
	outputs := make([]*tes.Output, 0, 8)
	for i := range 8 {
		source := filepath.Join(sources, fmt.Sprintf("source-%d", i))
		if err := os.WriteFile(source, bytes.Repeat([]byte{byte('A' + i)}, size), 0600); err != nil {
			t.Fatal(err)
		}
		outputs = append(outputs, &tes.Output{Path: source, Url: "s3://bucket/outputs/shared", Type: tes.File})
	}
	task := &tes.Task{Id: "task", Executors: []*tes.Executor{{Image: "alpine", Command: []string{"true"}}}, Outputs: outputs}
	if errs := tes.Validate(task); len(errs) > 0 {
		t.Fatalf("TES rejected duplicate output URLs: %v", errs)
	}
	logs, err := UploadOutputs(context.Background(), outputs, mux, events.NewTaskWriter("task", 0, events.Noop{}), 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != len(outputs) {
		t.Fatalf("successful output logs = %d, want %d", len(logs), len(outputs))
	}

	got, err := os.ReadFile(alias)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "original inode" {
		t.Fatalf("upload changed a hardlink to the old destination, now %d bytes", len(got))
	}
	got, err = os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != size {
		t.Fatalf("published size = %d, want %d", len(got), size)
	}
	if got[0] < 'A' || got[0] > 'H' || !bytes.Equal(got, bytes.Repeat(got[:1], size)) {
		t.Fatal("parallel publication left mixed destination bytes")
	}
}

func TestMountedOutputWriterPublishesAcrossProcesses(t *testing.T) {
	const size = 4 << 20
	root := t.TempDir()
	target := filepath.Join(root, "shared")
	alias := filepath.Join(root, "alias")
	if err := os.WriteFile(target, []byte("original inode"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(target, alias); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Errorf("unexpected S3 request %s", r.Method)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(size))
		w.Header().Set("ETag", `"mounted"`)
		w.Header().Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
	}))
	defer server.Close()

	commands := make([]*exec.Cmd, 0, 2)
	var commandStdout, commandStderr [2]bytes.Buffer
	for i, letter := range []byte{'A', 'B'} {
		source := filepath.Join(t.TempDir(), fmt.Sprintf("source-%d", i))
		if err := os.WriteFile(source, bytes.Repeat([]byte{letter}, size), 0600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(os.Args[0], "-test.run=^TestMountedOutputWriterProcessHelper$")
		cmd.Env = append(os.Environ(),
			"FUNNEL_MOUNTED_WRITER_HELPER=1",
			"FUNNEL_MOUNTED_WRITER_ENDPOINT="+server.URL,
			"FUNNEL_MOUNTED_WRITER_ROOT="+root,
			"FUNNEL_MOUNTED_WRITER_SOURCE="+source,
		)
		cmd.Stdout = &commandStdout[i]
		cmd.Stderr = &commandStderr[i]
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		commands = append(commands, cmd)
	}
	for i, cmd := range commands {
		if err := cmd.Wait(); err != nil {
			t.Fatalf("writer process %d: %v\n%s%s", i, err, commandStdout[i].String(), commandStderr[i].String())
		}
	}

	got, err := os.ReadFile(alias)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "original inode" {
		t.Fatalf("writer process changed a hardlink to the old destination: %q", got)
	}
	got, err = os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, bytes.Repeat([]byte{'A'}, size)) && !bytes.Equal(got, bytes.Repeat([]byte{'B'}, size)) {
		t.Fatal("writer processes left mixed destination contents")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".funnel-stage-") {
			t.Fatalf("writer process left a staging file: %q", entry.Name())
		}
	}
}

func TestMountedOutputWriterProcessHelper(t *testing.T) {
	if os.Getenv("FUNNEL_MOUNTED_WRITER_HELPER") == "" {
		return
	}
	conf := config.EmptyConfig()
	conf.AmazonS3.Disabled = true
	conf.GoogleStorage.Disabled = true
	conf.GenericS3 = []*config.GenericS3Storage{{
		Endpoint: os.Getenv("FUNNEL_MOUNTED_WRITER_ENDPOINT"),
		Bucket:   "bucket",
		Key:      "key",
		Secret:   "secret",
		Region:   "us-east-1",
	}}
	mux, err := storage.NewWorkerMux(conf, nil, storage.S3WriteMount{
		Endpoint: os.Getenv("FUNNEL_MOUNTED_WRITER_ENDPOINT"),
		Bucket:   "bucket",
		Prefix:   "outputs",
		Root:     os.Getenv("FUNNEL_MOUNTED_WRITER_ROOT"),
		Timeout:  5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mux.Put(context.Background(), "s3://bucket/outputs/shared", os.Getenv("FUNNEL_MOUNTED_WRITER_SOURCE")); err != nil {
		t.Fatal(err)
	}
}
