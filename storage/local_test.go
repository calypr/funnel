package storage

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLocalSupports(t *testing.T) {
	l := Local{allowedDirs: []string{"/path"}}

	err := l.UnsupportedOperations("file:///path/to/foo.txt").Get
	if err != nil {
		t.Fatal("Expected file:// URL to be supported", err)
	}

	err = l.UnsupportedOperations("/path/to/foo.txt").Get
	if err != nil {
		t.Fatal("Expected normal file path to be supported", err)
	}
}

// Tests Get on a "file://" URL, e.g. "file:///path/to/foo.txt"
func TestLocalGet(t *testing.T) {
	ctx := context.Background()
	tmp, err := os.MkdirTemp("", "funnel-test-local-storage")
	if err != nil {
		t.Fatal(err)
	}
	l := &Local{allowedDirs: []string{tmp}}

	// File test
	ip := path.Join(tmp, "input.txt")
	cp := path.Join(tmp, "container.txt")
	if err := os.WriteFile(ip, []byte("foo"), os.ModePerm); err != nil {
		t.Fatal(err)
	}

	_, gerr := l.Get(ctx, "file://"+ip, cp)
	if gerr != nil {
		t.Fatal(gerr)
	}

	b, rerr := os.ReadFile(cp)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if string(b) != "foo" {
		t.Fatal("Unexpected content")
	}
}

// Tests List on a local directory.
func TestLocalListDirectory(t *testing.T) {
	ctx := context.Background()
	d, err := filepath.Abs("../tests/storage/testdata/")
	if err != nil {
		t.Fatal(err)
	}

	l := &Local{
		allowedDirs: []string{d},
	}

	stat1, err := os.Stat("../tests/storage/testdata/test_dir/subdir/test_dir_file1")
	if err != nil {
		t.Fatal(err)
	}
	stat2, err := os.Stat("../tests/storage/testdata/test_dir/test_dir_file2")
	if err != nil {
		t.Fatal(err)
	}
	lm1 := stat1.ModTime()
	lm2 := stat2.ModTime()

	list, gerr := l.List(ctx, "file://"+d+"/test_dir")
	if gerr != nil {
		t.Fatal(gerr)
	}

	expected1 := []*Object{
		{
			URL:          "file://" + d + "/test_dir/subdir/test_dir_file1",
			Name:         "subdir/test_dir_file1",
			LastModified: lm1,
			Size:         14,
		},
		{
			URL:          "file://" + d + "/test_dir/test_dir_file2",
			Name:         "test_dir_file2",
			LastModified: lm2,
			Size:         14,
		},
	}

	assert.Equal(t, expected1, list)

	list, gerr = l.List(ctx, d+"/test_dir")
	if gerr != nil {
		t.Fatal(gerr)
	}

	expected2 := []*Object{
		{
			URL:          d + "/test_dir/subdir/test_dir_file1",
			Name:         "subdir/test_dir_file1",
			LastModified: lm1,
			Size:         14,
		},
		{
			URL:          d + "/test_dir/test_dir_file2",
			Name:         "test_dir_file2",
			LastModified: lm2,
			Size:         14,
		},
	}

	assert.Equal(t, expected2, list)

	list, gerr = l.List(ctx, "../tests/storage/testdata/test_dir")
	if gerr != nil {
		t.Fatal(gerr)
	}

	expected3 := []*Object{
		{
			URL:          "../tests/storage/testdata/test_dir/subdir/test_dir_file1",
			Name:         "subdir/test_dir_file1",
			LastModified: lm1,
			Size:         14,
		},
		{
			URL:          "../tests/storage/testdata/test_dir/test_dir_file2",
			Name:         "test_dir_file2",
			LastModified: lm2,
			Size:         14,
		},
	}

	assert.Equal(t, expected3, list)
}

func TestLocalJoin(t *testing.T) {
	l := &Local{}
	j1, err := l.Join("file:///foo/bar", "baz")
	if err != nil {
		t.Fatal(err)
	}
	e1 := "file:///foo/bar/baz"
	if j1 != e1 {
		t.Errorf("failed local join: %s != %s", j1, e1)
	}
}

// Tests Get on a URL that is a path, e.g. "/path/to/foo.txt"
func TestLocalGetPath(t *testing.T) {
	ctx := context.Background()
	tmp, err := os.MkdirTemp("", "funnel-test-local-storage")
	if err != nil {
		t.Fatal(err)
	}
	l := &Local{allowedDirs: []string{tmp}}

	ip := path.Join(tmp, "input.txt")
	cp := path.Join(tmp, "container.txt")
	if err := os.WriteFile(ip, []byte("foo"), os.ModePerm); err != nil {
		t.Fatal(err)
	}

	_, gerr := l.Get(ctx, ip, cp)
	if gerr != nil {
		t.Fatal(gerr)
	}

	b, rerr := os.ReadFile(cp)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if string(b) != "foo" {
		t.Fatal("Unexpected content")
	}
}

// Tests Put on a "file://" URL, e.g. "file:///path/to/foo.txt"
func TestLocalPut(t *testing.T) {
	ctx := context.Background()
	tmp, err := os.MkdirTemp("", "funnel-test-local-storage")
	if err != nil {
		t.Fatal(err)
	}
	l := &Local{allowedDirs: []string{tmp}}

	// File test
	cp := path.Join(tmp, "container.txt")
	op := path.Join(tmp, "output.txt")
	if err := os.WriteFile(cp, []byte("foo"), os.ModePerm); err != nil {
		t.Fatal(err)
	}

	_, gerr := l.Put(ctx, "file://"+op, cp)
	if gerr != nil {
		t.Fatal(gerr)
	}

	b, rerr := os.ReadFile(op)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if string(b) != "foo" {
		t.Fatal("Unexpected content")
	}
}

// Tests Put on a URL that is a path, e.g. "/path/to/foo.txt"
func TestLocalPutPath(t *testing.T) {
	ctx := context.Background()
	tmp, err := os.MkdirTemp("", "funnel-test-local-storage")
	if err != nil {
		t.Fatal(err)
	}
	l := &Local{allowedDirs: []string{tmp}}

	cp := path.Join(tmp, "container.txt")
	op := path.Join(tmp, "output.txt")
	if err := os.WriteFile(cp, []byte("foo"), os.ModePerm); err != nil {
		t.Fatal(err)
	}

	_, gerr := l.Put(ctx, op, cp)
	if gerr != nil {
		t.Fatal(gerr)
	}

	b, rerr := os.ReadFile(op)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if string(b) != "foo" {
		t.Fatal("Unexpected content")
	}
}

func TestProcessFile(t *testing.T) {
	ctx := context.Background()
	tmp, err := os.MkdirTemp("", "funnel-test-local-storage")
	if err != nil {
		t.Fatal(err)
	}

	cp := path.Join(tmp, "input.txt")
	op := path.Join(tmp, "output.txt")
	if err := os.WriteFile(cp, []byte("foo"), os.ModePerm); err != nil {
		t.Fatal(err)
	}

	err = processFile(ctx, cp, op)
	if err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(op)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "foo" {
		t.Fatal("Unexpected content")
	}
}

// Tests Put when source and dest reference the same file (inode)
// Since Local storage hard-links files when possible we need to protect
// against the case where the same path is 'Put' twice
func TestSameFile(t *testing.T) {
	ctx := context.Background()
	tmp, err := os.MkdirTemp("", "funnel-test-local-storage")
	if err != nil {
		t.Fatal(err)
	}
	tmpOut, err := os.MkdirTemp("", "funnel-test-local-storage")
	if err != nil {
		t.Fatal(err)
	}

	// Write the test files
	cp := path.Join(tmp, "output.txt")
	cp2 := path.Join(tmp, "output2.txt")
	op := path.Join(tmpOut, "output.txt")
	if err := os.WriteFile(cp, []byte("foo"), os.ModePerm); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cp2, []byte("bar"), os.ModePerm); err != nil {
		t.Fatal(err)
	}

	err = linkFile(ctx, cp, op)
	if err != nil {
		t.Fatal(err)
	}

	// since the file in this dir were already 'Put' in the previous step
	// nothing should happen
	err = copyFile(ctx, cp, op)
	if err != nil {
		t.Fatal(err)
	}

	// Check the resulting content
	b, err := os.ReadFile(op)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "foo" {
		t.Fatal("Unexpected content")
	}

	// same file output url; new src contents
	err = copyFile(ctx, cp2, op)
	if err != nil {
		t.Fatal(err)
	}

	// Check the resulting content
	b, err = os.ReadFile(op)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "bar" {
		t.Fatal("Unexpected content")
	}
}

func TestGetPath(t *testing.T) {
	x := "file:///foo/bar/file with spaces.txt"
	e := getPath(x)
	if e != "/foo/bar/file with spaces.txt" {
		t.Fatal("Unexpected URL encoding")
	}

	x = "/foo/bar/escaped%20with%20.txt"
	e = getPath(x)
	if e != "/foo/bar/escaped%20with%20.txt" {
		t.Fatal("Unexpected URL encoding")
	}
}
