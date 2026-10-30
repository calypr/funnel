package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/storage"
	"github.com/calypr/funnel/tes"
	"github.com/calypr/funnel/util"
	"github.com/calypr/funnel/util/fsutil"
	"google.golang.org/protobuf/proto"
)

// FlattenInputs flattens any directory inputs into a list of file inputs.
// A warning event will be generated if an input directory is empty.
func FlattenInputs(ctx context.Context, inputs []*tes.Input, store storage.Storage, ev *events.TaskWriter) ([]*tes.Input, error) {

	var flat []*tes.Input
	for _, input := range inputs {
		switch input.Type {

		case tes.File:
			flat = append(flat, input)

		case tes.Directory:
			prefix := strings.TrimSuffix(input.Url, "/") + "/"
			list, err := store.List(ctx, prefix)
			if err != nil {
				return nil, fmt.Errorf("listing directory: %s", err)
			}

			if len(list) == 0 {
				_ = ev.Warn("download source directory is empty", "url", input.Url) //nolint:errcheck // A warning event must not change input flattening.
				continue
			}

			for _, obj := range list {
				rel, ok := strings.CutPrefix(obj.URL, prefix)
				if !ok || !filepath.IsLocal(rel) || rel == "." || strings.ContainsAny(rel, "\\\x00") {
					return nil, fmt.Errorf("listed input %q is outside directory %q", obj.URL, input.Url)
				}

				flat = append(flat, &tes.Input{
					Url:  obj.URL,
					Path: filepath.Join(input.Path, rel),
				})
			}
		}
	}
	return flat, nil
}

// DownloadInputs downloads the given inputs.
func DownloadInputs(pctx context.Context, inputs []*tes.Input, store storage.Storage, ev *events.TaskWriter, parallelLimit int) error {

	ctx, cancel := context.WithCancel(pctx)
	defer cancel()

	flat, err := FlattenInputs(ctx, inputs, store, ev)
	if err != nil {
		return err
	}

	var downloads []storage.Transfer
	for _, input := range flat {
		downloads = append(downloads, storage.Transfer(&download{
			ev:     ev,
			in:     input,
			cancel: cancel,
		}))
	}

	storage.Download(ctx, store, downloads, parallelLimit)

	var errs util.MultiError
	for _, x := range downloads {
		down := x.(*download)
		if down.err != nil {
			errs = append(errs, down.err)
		}
	}

	return errs.ToError()
}

// FlattenOutputs flattens output directories into a list of files.
// A warning event will be generated if an output directory is empty.
func FlattenOutputs(ctx context.Context, outputs []*tes.Output, store storage.Storage, ev *events.TaskWriter) ([]*tes.Output, error) {

	var flat []*tes.Output
	for _, output := range outputs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		info, statErr := os.Stat(output.Path)
		if output.Type == nil {
			if statErr != nil || !info.IsDir() {
				flat = append(flat, output)
				continue
			}
		} else {
			switch output.GetType() {
			case tes.File:
				if statErr == nil && info.IsDir() {
					return nil, fmt.Errorf("output path %q is a directory but is declared as a file", output.Path)
				}
				flat = append(flat, output)
				continue
			case tes.Directory:
				if statErr == nil && !info.IsDir() {
					return nil, fmt.Errorf("output path %q is not a directory but is declared as a directory", output.Path)
				}
			default:
				continue
			}
		}

		list, err := fsutil.WalkFiles(output.Path)
		if err != nil {
			return nil, fmt.Errorf("walking directory: %s", err)
		}

		if len(list) == 0 {
			_ = ev.Warn("upload source directory is empty", "url", output.Url) //nolint:errcheck // A warning event must not change output flattening.
			continue
		}

		for _, f := range list {
			u, err := store.Join(output.Url, f.Rel)
			if err != nil {
				return nil, fmt.Errorf("joining storage url: %s", err)
			}
			flat = append(flat, &tes.Output{
				Url:  u,
				Path: f.Abs,
			})
		}
	}
	return flat, nil
}

// UploadOutputs uploads the outputs.
func UploadOutputs(ctx context.Context, outputs []*tes.Output, store storage.Storage, ev *events.TaskWriter, parallelLimit int) ([]*tes.OutputFileLog, error) {
	return uploadOutputs(ctx, outputs, store, ev, parallelLimit, nil)
}

func uploadOutputs(ctx context.Context, outputs []*tes.Output, store storage.Storage, ev *events.TaskWriter, parallelLimit int, mapper *FileMapper) ([]*tes.OutputFileLog, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	flat, err := FlattenOutputs(ctx, outputs, store, ev)
	if err != nil {
		return nil, err
	}

	// List all files and send to uploader routines.
	var uploads []storage.Transfer
	for _, output := range flat {
		outputPath := output.Path
		if mapper != nil {
			outputPath = mapper.ContainerPath(output.Path)
		}
		uploads = append(uploads, storage.Transfer(&upload{ev: ev, out: output, outputPath: outputPath}))
	}

	storage.Upload(ctx, store, uploads, parallelLimit)

	var logs []*tes.OutputFileLog
	var errs []error

	for _, x := range uploads {
		up := x.(*upload)
		if up.err != nil {
			errs = append(errs, up.err)
		} else {
			logs = append(logs, up.log)
		}
	}

	return logs, errors.Join(errs...)
}

type download struct {
	ev     *events.TaskWriter
	in     *tes.Input
	err    error
	cancel context.CancelFunc
}

func (d *download) URL() string {
	return d.in.Url
}
func (d *download) Path() string {
	return d.in.Path
}
func (d *download) Started() {
	_ = d.ev.Info("download started", "url", d.in.Url) //nolint:errcheck // Transfer logging must not change download behavior.
}
func (d *download) Finished(obj *storage.Object) {
	_ = d.ev.Info("download finished", "url", d.in.Url, "size", obj.Size, "etag", obj.ETag) //nolint:errcheck // Transfer logging must not change download behavior.
}
func (d *download) Failed(err error) {
	_ = d.ev.Error("download failed", "url", d.in.Url, "error", err) //nolint:errcheck // The transfer error is retained in d.err; logging must not replace it.
	d.cancel()
	d.err = err
}

type upload struct {
	ev         *events.TaskWriter
	out        *tes.Output
	outputPath string
	log        *tes.OutputFileLog
	err        error
}

func (u *upload) URL() string {
	return u.out.Url
}
func (u *upload) Path() string {
	return u.out.Path
}
func (u *upload) Started() {
	_ = u.ev.Info("upload started", "url", u.out.Url) //nolint:errcheck // Transfer logging must not change upload behavior.
}
func (u *upload) Finished(obj *storage.Object) {
	u.log = &tes.OutputFileLog{
		Url:       obj.URL,
		Path:      u.out.Path,
		SizeBytes: fmt.Sprintf("%d", obj.Size),
	}
	_ = u.ev.Info("upload finished", "url", obj.URL, "etag", obj.ETag, "size", obj.Size) //nolint:errcheck // Transfer logging must not change upload behavior.
}
func (u *upload) Failed(err error) {
	u.err = err
	_ = u.ev.Error("upload failed", "path", u.outputPath, "url", u.out.Url, "error", err) //nolint:errcheck // The transfer error is retained in u.err; logging must not replace it.
}

// fixLinks walks the output paths, fixing cases where a symlink is
// broken because it's pointing to a path inside a container volume.
func fixLinks(mapper *FileMapper, basepath string) error {
	err := filepath.Walk(basepath, func(p string, f os.FileInfo, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}

		// Only bother to check symlinks
		if f.Mode()&os.ModeSymlink != 0 {
			// Test if the file can be opened because it doesn't exist
			fh, rerr := os.Open(p)
			if rerr == nil {
				return fh.Close()
			}

			if errors.Is(rerr, os.ErrNotExist) {

				// Get symlink source path
				src, err := os.Readlink(p)
				if err != nil {
					if errors.Is(err, os.ErrNotExist) {
						return nil
					}
					return fmt.Errorf("read output symlink %s: %w", p, err)
				}
				// Map symlink source (possible container path) to host path
				mapped, err := mapper.HostPath(src)
				if err != nil {
					// Links outside the work directory are left as the task created them.
					return nil
				}

				// Check whether the mapped path exists
				fh, err := os.Open(mapped)
				if errors.Is(err, os.ErrNotExist) {
					return nil
				}
				if err != nil {
					return fmt.Errorf("open mapped output symlink target %s: %w", mapped, err)
				}
				err = fh.Close()
				if err != nil {
					return fmt.Errorf("close mapped output symlink target %s: %w", mapped, err)
				}

				// Replace the broken link only after confirming its target exists.
				err = os.Remove(p)
				if err != nil {
					if errors.Is(err, os.ErrNotExist) {
						return nil
					}
					return fmt.Errorf("remove broken output symlink %s: %w", p, err)
				}
				err = os.Symlink(mapped, p)
				if err != nil {
					return fmt.Errorf("create output symlink %s: %w", p, err)
				}
			} else {
				return fmt.Errorf("open output symlink %s: %w", p, rerr)
			}
		}
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("walking output path %s: %w", basepath, err)
	}
	return nil
}

// resolveWildcards resolves any wildcards/globs in the output paths.
func resolveWildcards(mapper *FileMapper, store storage.Storage) ([]string, error) {
	var outputs []*tes.Output
	var unmatched []string

	for _, output := range mapper.Outputs {
		// If path contains a wildcard, handle globbing and upload each file individually
		if strings.Contains(output.Path, "*") {
			globs, err := filepath.Glob(output.Path)
			if err != nil {
				return nil, fmt.Errorf("failed to resolve output pattern %q: %v", mapper.ContainerPath(output.Path), err)
			}
			if len(globs) == 0 {
				unmatched = append(unmatched, mapper.ContainerPath(output.Path))
				continue
			}

			for _, glob := range globs {
				out := proto.Clone(output).(*tes.Output)
				out.Path = glob

				// Construct URL by:
				// - removing the mapper.WorkDir from the output.Path
				// - then removing the PathPrefix and the leading slash
				// - then joining the output.URL with the storage backend's
				//   Join, as for DIRECTORY outputs, so the file name is
				//   encoded the way that backend expects
				globPath := strings.TrimPrefix(glob, mapper.WorkDir)
				fixPath := strings.TrimPrefix(globPath, output.PathPrefix)
				out.Url, err = store.Join(output.Url, strings.TrimPrefix(fixPath, "/"))
				if err != nil {
					return nil, fmt.Errorf("failed to join URL: %v", err)
				}

				outputs = append(outputs, out)
			}
		} else {
			outputs = append(outputs, output)
		}
	}

	mapper.Outputs = outputs
	return unmatched, nil
}
