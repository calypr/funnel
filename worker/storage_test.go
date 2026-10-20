package worker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/storage"
	"github.com/calypr/funnel/tes"
	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/proto"
)

// protoSlicesEqual reports whether two slices of protobuf messages are equal
// (proto.Equal per element). testify's DeepEqual trips over protobuf
// implementation internals, so protos must be compared with proto.Equal.
func protoSlicesEqual[T proto.Message](want, got []T) bool {
	if len(want) != len(got) {
		return false
	}
	for i := range want {
		if !proto.Equal(want[i], got[i]) {
			return false
		}
	}
	return true
}

type fakelist struct {
	list []*storage.Object
	storage.Fake
}

func (f fakelist) List(context.Context, string) ([]*storage.Object, error) {
	return f.list, nil
}

func TestFlattenInputs(t *testing.T) {
	ev := events.NewTaskWriter("task-1", 0, events.Noop{})
	fake := fakelist{
		list: []*storage.Object{
			{URL: "s3://bkt/path/to/one.txt", Name: "path/to/one.txt"},
			{URL: "s3://bkt/path/to/two.txt", Name: "path/to/two.txt"},
		},
	}

	in := []*tes.Input{
		{Url: "s3://bkt/path", Path: "/inputs/foo", Type: tes.Directory},
		{Url: "s3://bkt/path", Path: "/inputs/foo/", Type: tes.Directory},
		{Url: "s3://bkt/path/", Path: "/inputs/foo", Type: tes.Directory},
		{Url: "s3://bkt/path/", Path: "/inputs/foo/", Type: tes.Directory},
	}

	bg := context.Background()
	flat, err := FlattenInputs(bg, in, fake, ev)
	if err != nil {
		t.Fatal(err)
	}

	expected := []*tes.Input{
		{Url: "s3://bkt/path/to/one.txt", Path: "/inputs/foo/to/one.txt"},
		{Url: "s3://bkt/path/to/two.txt", Path: "/inputs/foo/to/two.txt"},
		{Url: "s3://bkt/path/to/one.txt", Path: "/inputs/foo/to/one.txt"},
		{Url: "s3://bkt/path/to/two.txt", Path: "/inputs/foo/to/two.txt"},
		{Url: "s3://bkt/path/to/one.txt", Path: "/inputs/foo/to/one.txt"},
		{Url: "s3://bkt/path/to/two.txt", Path: "/inputs/foo/to/two.txt"},
		{Url: "s3://bkt/path/to/one.txt", Path: "/inputs/foo/to/one.txt"},
		{Url: "s3://bkt/path/to/two.txt", Path: "/inputs/foo/to/two.txt"},
	}

	assert.True(t, protoSlicesEqual(expected, flat))
}

func TestUploadOutputsFileTypedDirectory(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"nested/one.txt": "first file\n",
		"nested/two.txt": "second file\n",
	}
	for rel, content := range files {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	output := &tes.Output{Url: "s3://bucket/outputs", Path: root, Type: tes.File}
	store := &capturingUploadStorage{uploaded: make(map[string]string)}
	logs, err := UploadOutputs(context.Background(), []*tes.Output{output}, store,
		events.NewTaskWriter("task-1", 0, events.Noop{}), 1)
	if err != nil {
		t.Fatal(err)
	}

	wantUploads := map[string]string{
		"s3://bucket/outputs/nested/one.txt": "first file\n",
		"s3://bucket/outputs/nested/two.txt": "second file\n",
	}
	if diff := deep.Equal(store.uploaded, wantUploads); diff != nil {
		t.Fatalf("uploaded files differ: %v", diff)
	}

	wantLogs := []*tes.OutputFileLog{
		{Url: "s3://bucket/outputs/nested/one.txt", Path: filepath.Join(root, "nested/one.txt"), SizeBytes: "11"},
		{Url: "s3://bucket/outputs/nested/two.txt", Path: filepath.Join(root, "nested/two.txt"), SizeBytes: "12"},
	}
	if diff := deep.Equal(logs, wantLogs); diff != nil {
		t.Fatalf("output logs differ: %v", diff)
	}
	if output.Url != "s3://bucket/outputs" || output.Path != root || output.Type != tes.File {
		t.Fatalf("declared output was mutated: %+v", output)
	}
}

func TestUploadOutputsMissingFileStillReachesStorage(t *testing.T) {
	url := "s3://bucket/missing.txt"
	store := &capturingUploadStorage{uploaded: make(map[string]string)}
	logs, err := UploadOutputs(context.Background(), []*tes.Output{
		{Url: url, Path: filepath.Join(t.TempDir(), "missing.txt"), Type: tes.File},
	}, store, events.NewTaskWriter("task-1", 0, events.Noop{}), 1)
	if err == nil {
		t.Fatal("expected missing source file to fail during upload")
	}
	if len(store.attempted) != 1 || store.attempted[0] != url {
		t.Fatalf("storage upload attempts = %v, want [%s]", store.attempted, url)
	}
	if len(logs) != 0 {
		t.Fatalf("output logs = %v, want none", logs)
	}
}

func TestFlattenOutputsExplicitDirectoryStillRequiresDirectory(t *testing.T) {
	_, err := FlattenOutputs(context.Background(), []*tes.Output{
		{Url: "s3://bucket/output", Path: filepath.Join(t.TempDir(), "missing"), Type: tes.Directory},
	}, storage.Fake{}, events.NewTaskWriter("task-1", 0, events.Noop{}))
	if err == nil {
		t.Fatal("expected missing declared directory to fail")
	}
}

func TestFlattenOutputsUnknownTypeIsSkipped(t *testing.T) {
	flat, err := FlattenOutputs(context.Background(), []*tes.Output{
		{Url: "s3://bucket/output", Path: filepath.Join(t.TempDir(), "missing"), Type: tes.FileType(1000)},
	}, storage.Fake{}, events.NewTaskWriter("task-1", 0, events.Noop{}))
	if err != nil {
		t.Fatal(err)
	}
	if len(flat) != 0 {
		t.Fatalf("flattened outputs = %v, want none", flat)
	}
}

type capturingUploadStorage struct {
	storage.Fake
	uploaded  map[string]string
	attempted []string
}

func (s *capturingUploadStorage) Join(url, suffix string) (string, error) {
	return strings.TrimRight(url, "/") + "/" + filepath.ToSlash(suffix), nil
}

func (s *capturingUploadStorage) Put(_ context.Context, url, sourcePath string) (*storage.Object, error) {
	s.attempted = append(s.attempted, url)
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return nil, err
	}
	s.uploaded[url] = string(data)
	return &storage.Object{URL: url, Size: int64(len(data))}, nil
}

// Wildcard matches are joined to the output URL by the storage backend, as
// DIRECTORY outputs are, so each backend encodes file names its own way.
func TestResolveWildcardsJoinsWithStorage(t *testing.T) {
	workDir := t.TempDir()
	localDir := t.TempDir()
	for _, dir := range []string{"ftp", "http", "local"} {
		if err := os.MkdirAll(filepath.Join(workDir, "outputs", dir), 0755); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"result#1.txt", "50%25.txt", "100%.txt", "a b.txt", "a?b.txt"} {
			if err := os.WriteFile(filepath.Join(workDir, "outputs", dir, name), nil, 0644); err != nil {
				t.Fatal(err)
			}
		}
	}

	httpStore, err := storage.NewHTTP(&config.HTTPStorage{})
	if err != nil {
		t.Fatal(err)
	}
	ftpStore, err := storage.NewFTP(&config.FTPStorage{})
	if err != nil {
		t.Fatal(err)
	}
	localStore, err := storage.NewLocal(&config.LocalStorage{AllowedDirs: []string{localDir}})
	if err != nil {
		t.Fatal(err)
	}
	store := &storage.Mux{Backends: []storage.Storage{ftpStore, httpStore, localStore}}

	ftpDir := filepath.Join(workDir, "outputs", "ftp")
	httpDir := filepath.Join(workDir, "outputs", "http")
	localOutDir := filepath.Join(workDir, "outputs", "local")
	mapper := &FileMapper{
		WorkDir: workDir,
		Outputs: []*tes.Output{
			{Url: "ftp://user:pass@example.com/upload/glob?token=abc", Path: ftpDir + "/*.txt", PathPrefix: "/outputs/ftp"},
			{Url: "https://example.com/upload/glob?token=abc", Path: httpDir + "/*.txt", PathPrefix: "/outputs/http"},
			{Url: "file://" + localDir + "/glob", Path: localOutDir + "/*.txt", PathPrefix: "/outputs/local"},
		},
	}
	if err := resolveWildcards(mapper, store); err != nil {
		t.Fatal(err)
	}

	// FTP and HTTP escape each name and keep the query; local storage keeps the
	// names as they are. Glob sorts the matches.
	expected := []*tes.Output{
		{Url: "ftp://user:pass@example.com/upload/glob/100%25.txt?token=abc", Path: ftpDir + "/100%.txt", PathPrefix: "/outputs/ftp"},
		{Url: "ftp://user:pass@example.com/upload/glob/50%2525.txt?token=abc", Path: ftpDir + "/50%25.txt", PathPrefix: "/outputs/ftp"},
		{Url: "ftp://user:pass@example.com/upload/glob/a%20b.txt?token=abc", Path: ftpDir + "/a b.txt", PathPrefix: "/outputs/ftp"},
		{Url: "ftp://user:pass@example.com/upload/glob/a%3Fb.txt?token=abc", Path: ftpDir + "/a?b.txt", PathPrefix: "/outputs/ftp"},
		{Url: "ftp://user:pass@example.com/upload/glob/result%231.txt?token=abc", Path: ftpDir + "/result#1.txt", PathPrefix: "/outputs/ftp"},
		{Url: "https://example.com/upload/glob/100%25.txt?token=abc", Path: httpDir + "/100%.txt", PathPrefix: "/outputs/http"},
		{Url: "https://example.com/upload/glob/50%2525.txt?token=abc", Path: httpDir + "/50%25.txt", PathPrefix: "/outputs/http"},
		{Url: "https://example.com/upload/glob/a%20b.txt?token=abc", Path: httpDir + "/a b.txt", PathPrefix: "/outputs/http"},
		{Url: "https://example.com/upload/glob/a%3Fb.txt?token=abc", Path: httpDir + "/a?b.txt", PathPrefix: "/outputs/http"},
		{Url: "https://example.com/upload/glob/result%231.txt?token=abc", Path: httpDir + "/result#1.txt", PathPrefix: "/outputs/http"},
		{Url: "file://" + localDir + "/glob/100%.txt", Path: localOutDir + "/100%.txt", PathPrefix: "/outputs/local"},
		{Url: "file://" + localDir + "/glob/50%25.txt", Path: localOutDir + "/50%25.txt", PathPrefix: "/outputs/local"},
		{Url: "file://" + localDir + "/glob/a b.txt", Path: localOutDir + "/a b.txt", PathPrefix: "/outputs/local"},
		{Url: "file://" + localDir + "/glob/a?b.txt", Path: localOutDir + "/a?b.txt", PathPrefix: "/outputs/local"},
		{Url: "file://" + localDir + "/glob/result#1.txt", Path: localOutDir + "/result#1.txt", PathPrefix: "/outputs/local"},
	}
	assert.True(t, protoSlicesEqual(expected, mapper.Outputs))
}

func TestResolveWildcardsReportsJoinError(t *testing.T) {
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "out.txt"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	mapper := &FileMapper{
		WorkDir: workDir,
		// No storage backend handles this URL, so Join fails.
		Outputs: []*tes.Output{{Url: "gs://bucket/glob", Path: workDir + "/*.txt"}},
	}
	if err := resolveWildcards(mapper, &storage.Mux{}); err == nil {
		t.Error("expected an error for an output URL that no storage backend supports")
	}
}
