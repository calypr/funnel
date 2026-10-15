package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func writeFileAtomically(ctx context.Context, path string, write func(*os.File) error) error {
	if path == "" || os.IsPathSeparator(path[len(path)-1]) || filepath.Base(path) == "." || filepath.Base(path) == string(filepath.Separator) {
		return fmt.Errorf("invalid destination path %q", path)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create destination directory: %w", err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("open destination directory: %w", err)
	}
	defer root.Close()
	return writeRootFileAtomically(ctx, root, filepath.Base(path), write)
}

func writeRootFileAtomically(ctx context.Context, root *os.Root, destination string, write func(*os.File) error) error {
	return writeRootFileAtomicallyConfigured(ctx, root, destination, nil, write)
}

func writeRootFileAtomicallyWithMode(ctx context.Context, root *os.Root, destination string, mode os.FileMode, write func(*os.File) error) error {
	return writeRootFileAtomicallyConfigured(ctx, root, destination, &mode, write)
}

func writeRootFileAtomicallyConfigured(ctx context.Context, root *os.Root, destination string, mode *os.FileMode, write func(*os.File) error) (retErr error) {
	if destination == "" || os.IsPathSeparator(destination[len(destination)-1]) || filepath.IsAbs(destination) || !filepath.IsLocal(destination) {
		return fmt.Errorf("invalid root-relative destination %q", destination)
	}
	base := filepath.Base(destination)
	if base == "." || base == string(filepath.Separator) || base == "" {
		return fmt.Errorf("invalid root-relative destination %q", destination)
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	parent := root
	if dir := filepath.Dir(destination); dir != "." {
		var err error
		parent, err = root.OpenRoot(dir)
		if err != nil {
			return fmt.Errorf("open destination parent: %w", err)
		}
		defer parent.Close()
	}

	stageName, stage, createMode, err := createStagedSibling(parent)
	if err != nil {
		return err
	}
	finalMode := createMode
	if mode != nil {
		finalMode = *mode
	} else if existing, err := parent.Lstat(base); err == nil && existing.Mode().IsRegular() {
		finalMode = existing.Mode() & os.ModePerm
	}
	stageOpen := true
	committed := false
	defer func() {
		if stageOpen {
			if err := stage.Close(); err != nil {
				retErr = errors.Join(retErr, fmt.Errorf("close staged file: %w", err))
			}
		}
		if !committed {
			if err := parent.Remove(stageName); err != nil && !errors.Is(err, os.ErrNotExist) {
				retErr = errors.Join(retErr, fmt.Errorf("remove staged file: %w", err))
			}
		}
	}()

	writeErr := write(stage)
	if writeErr == nil {
		writeErr = stage.Chmod(finalMode)
	}
	closeErr := stage.Close()
	stageOpen = false
	if err := errors.Join(writeErr, closeErr, ctx.Err()); err != nil {
		return fmt.Errorf("write staged file: %w", err)
	}
	if err := parent.Rename(stageName, base); err != nil {
		return fmt.Errorf("replace destination: %w", err)
	}
	committed = true
	return nil
}

func createStagedSibling(root *os.Root) (string, *os.File, os.FileMode, error) {
	for attempt := 0; attempt < 10; attempt++ {
		random := make([]byte, 16)
		if _, err := rand.Read(random); err != nil {
			return "", nil, 0, fmt.Errorf("generate staged file name: %w", err)
		}
		name := ".funnel-stage-" + hex.EncodeToString(random)
		file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0666)
		if err == nil {
			info, err := file.Stat()
			if err != nil {
				cleanupErr := errors.Join(file.Close(), root.Remove(name))
				return "", nil, 0, fmt.Errorf("stat staged file: %w", errors.Join(err, cleanupErr))
			}
			createMode := info.Mode().Perm()
			if err := file.Chmod(0600); err != nil {
				cleanupErr := errors.Join(file.Close(), root.Remove(name))
				return "", nil, 0, fmt.Errorf("restrict staged file: %w", errors.Join(err, cleanupErr))
			}
			return name, file, createMode, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", nil, 0, fmt.Errorf("create staged file: %w", err)
		}
	}
	return "", nil, 0, errors.New("create staged file: too many name collisions")
}
