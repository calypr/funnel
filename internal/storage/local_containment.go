package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// openAllowed selects a lexical boundary, then uses descriptor-relative IO for
// every access. Root rejects escaping symlinks, including all absolute symlinks.
func (local *Local) openAllowed(path string) (*os.Root, string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, "", err
	}
	for _, dir := range local.allowedDirs {
		rel, err := filepath.Rel(dir, absolute)
		if err != nil || !filepath.IsLocal(rel) {
			continue
		}
		root, err := os.OpenRoot(dir)
		return root, rel, err
	}
	return nil, "", fmt.Errorf("localStorage: path is outside allowed directories: %s", path)
}

type localEndpoint struct {
	root *os.Root
	path string
}

func (e localEndpoint) open() (*os.File, error) {
	return openLocalFile(e.root, e.path, os.O_RDONLY, 0)
}
func (e localEndpoint) child(name string) localEndpoint {
	return localEndpoint{e.root, filepath.Join(e.path, name)}
}
func (e localEndpoint) mkdir() error {
	if e.root != nil {
		return e.root.MkdirAll(e.path, 0755)
	}
	return os.MkdirAll(e.path, 0755)
}
func (e localEndpoint) writable(mode os.FileMode) (*os.File, bool, error) {
	for attempts := 0; attempts < 40; attempts++ {
		file, err := openLocalFile(e.root, e.path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err == nil {
			return file, true, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, false, err
		}
		file, err = openLocalFile(e.root, e.path, os.O_WRONLY, 0)
		if !errors.Is(err, fs.ErrNotExist) {
			return file, false, err
		}
		var target string
		if e.root != nil {
			target, err = e.root.Readlink(e.path)
		} else {
			target, err = os.Readlink(e.path)
		}
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, false, err
		}
		if filepath.IsAbs(target) {
			e.path = target
		} else {
			// Keep parent symlinks and ".." for the rooted open to resolve.
			dir, _ := filepath.Split(e.path)
			e.path = dir + target
		}
	}
	return nil, false, fmt.Errorf("localStorage: destination changed too many times: %s", e.path)
}

// Copy from opened descriptors, including the same-file check before truncation.
// Cross-boundary hard links cannot be made safely through Root, so transfers copy.
func localTransfer(ctx context.Context, source, dest localEndpoint) error {
	if strings.Contains(source.path, "*") {
		var matches []string
		var err error
		if source.root != nil {
			matches, err = fs.Glob(source.root.FS(), filepath.ToSlash(source.path))
		} else {
			matches, err = filepath.Glob(source.path)
		}
		if err != nil {
			return err
		}
		for _, match := range matches {
			err = localTransferTree(ctx, localEndpoint{source.root, match}, dest.child(filepath.Base(match)), nil)
			if err != nil {
				return err
			}
		}
		return nil
	}
	return localTransferTree(ctx, source, dest, nil)
}

func localTransferTree(ctx context.Context, source, dest localEndpoint, ancestors []os.FileInfo) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	sf, err := source.open()
	if err != nil {
		return err
	}
	defer sf.Close() //nolint:errcheck // Input close is cleanup only.
	info, err := sf.Stat()
	if err != nil {
		return err
	}
	if info.IsDir() {
		for _, ancestor := range ancestors {
			if os.SameFile(info, ancestor) {
				return fmt.Errorf("localStorage: directory symlink cycle: %s", source.path)
			}
		}
		ancestors = append(ancestors, info)
		err = dest.mkdir()
		if err != nil {
			return err
		}
		entries, err := sf.ReadDir(-1)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			err = localTransferTree(ctx, source.child(entry.Name()), dest.child(entry.Name()), ancestors)
			if err != nil {
				return err
			}
		}
		return nil
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("localStorage: source is not a regular file: %s", source.path)
	}
	err = (localEndpoint{dest.root, filepath.Dir(dest.path)}).mkdir()
	if err != nil {
		return err
	}
	df, created, err := dest.writable(info.Mode().Perm())
	if err != nil {
		return err
	}
	defer df.Close() //nolint:errcheck // Explicit close below reports write failures.
	target, err := df.Stat()
	if err != nil {
		return err
	}
	if !target.Mode().IsRegular() {
		return fmt.Errorf("localStorage: destination is not a regular file: %s", dest.path)
	}
	if os.SameFile(info, target) {
		return nil
	}
	err = df.Truncate(0)
	if err != nil {
		return err
	}
	_, err = io.Copy(df, contextReader(ctx, sf))
	if err != nil {
		return err
	}
	if created {
		if err := preserveLocalFileMetadata(df, info); err != nil {
			return err
		}
	}
	return df.Close()
}

func localWalk(ctx context.Context, root *os.Root, path string) ([]Hostfile, error) {
	var files []Hostfile
	var walk func(string, string, []os.FileInfo) error
	walk = func(name, rel string, ancestors []os.FileInfo) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		file, err := (localEndpoint{root: root, path: name}).open()
		if err != nil {
			return err
		}
		defer file.Close() //nolint:errcheck // Read handle cleanup only.
		info, err := file.Stat()
		if err != nil {
			return err
		}
		if !info.IsDir() {
			if !info.Mode().IsRegular() {
				return fmt.Errorf("localStorage: listed path is not a regular file: %s", name)
			}
			files = append(files, Hostfile{Rel: rel, LastModified: info.ModTime(), Size: info.Size()})
			return nil
		}
		for _, ancestor := range ancestors {
			if os.SameFile(info, ancestor) {
				return fmt.Errorf("localStorage: directory symlink cycle: %s", name)
			}
		}
		ancestors = append(ancestors, info)
		entries, err := file.ReadDir(-1)
		if err != nil {
			return err
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, entry := range entries {
			err = walk(filepath.Join(name, entry.Name()), filepath.Join(rel, entry.Name()), ancestors)
			if err != nil {
				return err
			}
		}
		return nil
	}
	err := walk(path, "", nil)
	return files, err
}
