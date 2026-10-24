package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

func writeDownloadedFile(ctx context.Context, path string, write func(*os.File) error) error {
	if path == "" || os.IsPathSeparator(path[len(path)-1]) || filepath.Base(path) == "." || filepath.Base(path) == string(filepath.Separator) {
		return fmt.Errorf("invalid destination path %q", path)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create destination directory: %w", err)
	}
	parent, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("open destination directory: %w", err)
	}
	defer parent.Close()
	return writeRootDownloadedFile(ctx, parent, filepath.Base(path), write)
}

func writeRootDownloadedFile(ctx context.Context, parent *os.Root, name string, write func(*os.File) error) error {
	if name == "." || !filepath.IsLocal(name) || filepath.Base(name) != name {
		return fmt.Errorf("invalid destination basename %q", name)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return writeRootDownloadedFilePlatform(ctx, parent, name, write)
}
