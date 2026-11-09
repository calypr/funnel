package worker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/calypr/funnel/api/tes"
	"github.com/calypr/funnel/internal/events"
	"github.com/calypr/funnel/internal/storage"
	"github.com/calypr/funnel/internal/worker/workspace"
	"google.golang.org/protobuf/proto"
)

// snapshotTaskOutputs copies through confined descriptors into a private sibling
// directory. Uploaders can reopen these paths without racing task-owned links.
// Per-output failures accompany the successfully prepared outputs. A nil mapper
// means snapshot preparation could not continue.
func snapshotTaskOutputs(ctx context.Context, mapper *workspace.FileMapper, event *events.TaskWriter) (prepared *workspace.FileMapper, cleanup func(), err error) {
	if len(mapper.Outputs) == 0 {
		return mapper, func() {}, nil
	}
	base := mapper.WorkDir
	if mapper.ScratchDir != "" {
		base = mapper.ScratchDir
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return nil, nil, err
	}
	defer root.Close() //nolint:errcheck // The snapshot no longer needs its source directory handle.
	stage, err := os.MkdirTemp(filepath.Dir(base), ".funnel-outputs-")
	if err != nil {
		return nil, nil, err
	}
	remove := func() { _ = os.RemoveAll(stage) } //nolint:errcheck // Snapshot cleanup must not replace the publication result.
	cleanup = remove
	defer func() {
		if prepared == nil {
			remove()
		}
	}()
	copyMapper := *mapper
	copyMapper.WorkDir, copyMapper.ScratchDir, copyMapper.Outputs = stage, "", nil
	var preparationErrors []error
	for _, output := range mapper.Outputs {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		rel, err := filepath.Rel(mapper.WorkDir, output.Path)
		if err != nil || !filepath.IsLocal(rel) {
			return nil, nil, fmt.Errorf("output is outside task workspace")
		}
		if err := snapshotOutput(ctx, root, rel, stage, base, mapper.WorkDir); err != nil {
			wrapped := fmt.Errorf("prepare output %s: %w", output.Path, err)
			_ = event.Error("upload failed", "path", mapper.ContainerPath(output.Path), "url", output.Url, "error", wrapped) //nolint:errcheck // The preparation error is returned to the worker.
			if errors.Is(err, path.ErrBadPattern) {
				return nil, nil, wrapped
			}
			preparationErrors = append(preparationErrors, wrapped)
			continue
		}

		cloned := proto.Clone(output).(*tes.Output)
		cloned.Path = filepath.Join(stage, rel)
		copyMapper.Outputs = append(copyMapper.Outputs, cloned)
	}
	return &copyMapper, cleanup, errors.Join(preparationErrors...)
}

func snapshotOutput(ctx context.Context, root *os.Root, rel, stage string, hostRoots ...string) error {
	if !strings.Contains(rel, "*") {
		return copyOutputTree(ctx, root, rel, filepath.Join(stage, rel), hostRoots, nil)
	}
	prefix := filepath.Dir(rel[:strings.IndexByte(rel, '*')])
	resolved, err := resolveOutputSource(root, prefix, hostRoots...)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	pattern := filepath.Join(resolved, strings.TrimPrefix(rel, prefix+string(filepath.Separator)))
	matches, err := fs.Glob(root.FS(), filepath.ToSlash(pattern))
	if err != nil {
		return err
	}
	for _, match := range matches {
		suffix, err := filepath.Rel(resolved, filepath.FromSlash(match))
		if err != nil {
			return err
		}
		if err := copyOutputTree(ctx, root, filepath.FromSlash(match), filepath.Join(stage, prefix, suffix), hostRoots, nil); err != nil {
			return err
		}
	}
	return nil
}

// Resolve container-absolute links relative to the task root, and host-absolute
// in-workspace links relative to that same root. Never open an outside target.
func resolveOutputSource(root *os.Root, path string, hostRoots ...string) (string, error) {
	if !filepath.IsLocal(path) {
		return "", fmt.Errorf("output is outside task workspace")
	}
	pending := strings.Split(filepath.ToSlash(path), "/")
	var resolved []string
	links := 0
	for len(pending) > 0 {
		part := pending[0]
		pending = pending[1:]
		switch part {
		case "", ".":
			continue
		case "..":
			if len(resolved) == 0 {
				return "", fmt.Errorf("output symlink escapes task workspace")
			}
			resolved = resolved[:len(resolved)-1]
			continue
		}
		candidate := filepath.Join(append(append([]string(nil), resolved...), part)...)
		info, err := root.Lstat(candidate)
		if err != nil {
			return "", fmt.Errorf("resolve output: %w", err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			resolved = append(resolved, part)
			continue
		}
		links++
		if links > 40 {
			return "", fmt.Errorf("too many output symlinks")
		}
		target, err := root.Readlink(candidate)
		if err != nil {
			return "", err
		}
		if filepath.IsAbs(target) {
			mapped := ""
			for _, base := range hostRoots {
				if rel, err := filepath.Rel(base, target); err == nil && filepath.IsLocal(rel) {
					mapped = rel
					break
				}
			}
			if mapped == "" {
				mapped = strings.TrimLeft(target, string(filepath.Separator))
			}
			target = mapped
			resolved = nil
		}
		pending = append(strings.Split(filepath.ToSlash(target), "/"), pending...)
	}
	if len(resolved) == 0 {
		return ".", nil
	}
	return filepath.Join(resolved...), nil
}

func copyOutputTree(ctx context.Context, root *os.Root, source, destination string, hostRoots []string, ancestors []os.FileInfo) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	rel, err := resolveOutputSource(root, source, hostRoots...)
	if err != nil {
		return err
	}
	info, err := root.Stat(rel)
	if err != nil {
		return err
	}
	if info.IsDir() {
		dir, err := root.OpenRoot(rel)
		if err != nil {
			return err
		}
		actual, err := dir.Stat(".")
		if err != nil {
			_ = dir.Close() //nolint:errcheck // Preserve the failed stat.
			return err
		}
		for _, ancestor := range ancestors {
			if os.SameFile(actual, ancestor) {
				_ = dir.Close() //nolint:errcheck // Preserve the cycle error.
				return fmt.Errorf("output directory symlink cycle")
			}
		}
		entries, readErr := fs.ReadDir(dir.FS(), ".")
		closeErr := dir.Close()
		if err := errors.Join(readErr, closeErr); err != nil {
			return err
		}
		if err := os.MkdirAll(destination, 0700); err != nil {
			return err
		}
		ancestors = append(ancestors, actual)
		for _, entry := range entries {
			if err := copyOutputTree(ctx, root, filepath.Join(rel, entry.Name()), filepath.Join(destination, entry.Name()), hostRoots, ancestors); err != nil {
				return err
			}
		}
		return nil
	}
	sourceFile, err := storage.OpenRootRegularFile(root, rel)
	if err != nil {
		return err
	}
	defer sourceFile.Close()                                          //nolint:errcheck // The explicit close below reports normal errors; cancellation may already close the source.
	stop := context.AfterFunc(ctx, func() { _ = sourceFile.Close() }) //nolint:errcheck // Cancellation closes the read descriptor to interrupt copying.
	defer stop()
	info, err = sourceFile.Stat()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return err
	}
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, sourceFile)
	modeErr := out.Chmod(info.Mode().Perm())
	return errors.Join(copyErr, modeErr, out.Close(), sourceFile.Close(), ctx.Err())
}
