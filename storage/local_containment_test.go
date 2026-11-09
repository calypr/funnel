package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalContainment(t *testing.T) {
	base := t.TempDir()
	allowed := filepath.Join(base, "allowed")
	outside := filepath.Join(base, "allowed-sibling")
	for _, dir := range []string{allowed, outside} {
		err := os.MkdirAll(dir, 0700)
		if err != nil {
			t.Fatal(err)
		}
	}
	marker := filepath.Join(outside, "marker")
	err := os.WriteFile(marker, []byte("outside"), 0600)
	if err != nil {
		t.Fatal(err)
	}
	err = os.Symlink(outside, filepath.Join(allowed, "escape"))
	if err != nil {
		t.Fatal(err)
	}
	l := &Local{allowedDirs: []string{allowed}}
	ctx := context.Background()
	for _, target := range []string{marker, allowed + "/../allowed-sibling/marker", filepath.Join(allowed, "escape", "marker")} {
		t.Run(target, func(t *testing.T) {
			_, err := l.Stat(ctx, target)
			if err == nil {
				t.Fatal("Stat escaped")
			}
			_, err = l.Get(ctx, target, filepath.Join(base, "copied"))
			if err == nil {
				t.Fatal("Get escaped")
			}
			_, err = l.Put(ctx, target, marker)
			if err == nil {
				t.Fatal("Put escaped")
			}
		})
	}
	if l.UnsupportedOperations(marker).Get == nil {
		t.Fatal("sibling supported")
	}
	_, err = l.Join(allowed, "../allowed-sibling/marker")
	if err == nil {
		t.Fatal("Join escaped")
	}
	_, err = l.List(ctx, allowed)
	if err == nil {
		t.Fatal("List followed escaping child")
	}
	_, err = l.Get(ctx, allowed, filepath.Join(base, "tree"))
	if err == nil {
		t.Fatal("directory Get followed escaping child")
	}
	_, err = l.Put(ctx, filepath.Join(allowed, "escape", "missing", "file"), marker)
	if err == nil {
		t.Fatal("Put created outside parents")
	}
	_, err = os.Stat(filepath.Join(outside, "missing"))
	if !os.IsNotExist(err) {
		t.Fatalf("outside parent exists: %v", err)
	}
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "outside" {
		t.Fatalf("outside changed: %q %v", data, err)
	}
}

func TestLocalContainedTransfers(t *testing.T) {
	base := t.TempDir()
	allowed := filepath.Join(base, "allowed")
	err := os.MkdirAll(filepath.Join(allowed, "source", "nested"), 0700)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(allowed, "source", "nested", "file")
	err = os.WriteFile(file, []byte("contents"), 0600)
	if err != nil {
		t.Fatal(err)
	}
	l := &Local{allowedDirs: []string{allowed}}
	ctx := context.Background()
	for _, op := range []func(context.Context, string, string) (*Object, error){l.Get, l.Put} {
		_, err = op(ctx, file, file)
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err = l.Get(ctx, filepath.Join(allowed, "source"), filepath.Join(base, "trusted"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = l.Put(ctx, filepath.Join(allowed, "destination"), filepath.Join(base, "trusted"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(allowed, "destination", "nested", "file"))
	if err != nil || string(data) != "contents" {
		t.Fatalf("copy: %q %v", data, err)
	}
	err = os.Symlink("source/nested/file", filepath.Join(allowed, "relative"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = l.Get(ctx, filepath.Join(allowed, "relative"), filepath.Join(base, "relative-copy"))
	if err != nil {
		t.Fatal(err)
	}
}
