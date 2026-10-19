package worker

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ohsu-comp-bio/funnel/events"
	"github.com/ohsu-comp-bio/funnel/logger"
	"github.com/ohsu-comp-bio/funnel/storage"
	"github.com/ohsu-comp-bio/funnel/tes"
	"github.com/ohsu-comp-bio/funnel/util"
	"github.com/ohsu-comp-bio/funnel/util/fsutil"
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

			pathPrefix := strings.TrimSuffix(input.Url, "/") + "/"

			// If the storage backend is GenericS3 and this input is in the GenericS3 mounted
			// bucket, listing the directory files returns URLs prefixed with the path to the
			// mounted bucket instead of the direct s3 path => update `pathPrefix`.
			// Example:
			// The URL is "/opt/funnel/funnel-work-dir/mydir/file.txt"
			// instead of "s3://mybucket/mydir/file.txt",
			// and the prefix to trim is "/opt/funnel/funnel-work-dir/mydir"
			// instead of "s3://mybucket/mydir".
			if muxStore, ok := store.(*storage.Mux); ok {
				backend, err := muxStore.FindBackend(input.Url, storage.GetOp)
				if err != nil {
					return nil, err
				}
				if genericS3Store, ok := backend.(*storage.GenericS3); ok {
					u, err := genericS3Store.Parse(input.Url)
					if err != nil {
						return nil, err
					}
					if genericS3Store.IsThisBucketMounted(u.GetBucket()) {
						pathPrefix = genericS3Store.GetLocalMountedPath(input.Url)
					}
				}
			}

			list, err := store.List(ctx, input.Url)
			if err != nil {
				return nil, fmt.Errorf("listing directory: %s", err)
			}

			if len(list) == 0 {
				ev.Warn("download source directory is empty", "url", input.Url)
				continue
			}

			for _, obj := range list {
				flat = append(flat, &tes.Input{
					Url:  obj.URL,
					Path: filepath.Join(input.Path, strings.TrimPrefix(obj.URL, pathPrefix)),
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
		// NOTE: this is the TES task's `outputs.type` field, which is user-specified and may not
		// be accurate! The downstream `Put` function that uploads outputs may still detect
		// directories and upload them correctly even when the type is `File` (e.g. `GenericS3.Put`)
		switch output.Type {
		case tes.File:
			flat = append(flat, output)

		case tes.Directory:
			list, err := fsutil.WalkFiles(output.Path)
			if err != nil {
				return nil, fmt.Errorf("walking directory: %s", err)
			}

			if len(list) == 0 {
				ev.Warn("upload source directory is empty", "url", output.Url)
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
	}
	return flat, nil
}

// UploadOutputs uploads the outputs.
func UploadOutputs(ctx context.Context, outputs []*tes.Output, store storage.Storage, ev *events.TaskWriter, parallelLimit int, s3FilesFilesystemId string) ([]*tes.OutputFileLog, error) {

	flat, err := FlattenOutputs(ctx, outputs, store, ev)
	if err != nil {
		return nil, err
	}

	// List all files and send to uploader routines.
	var uploads []storage.Transfer
	for _, output := range flat {
		uploads = append(uploads, storage.Transfer(&upload{ev: ev, out: output}))
	}

	storage.Upload(ctx, store, uploads, parallelLimit)

	var logs []*tes.OutputFileLog
	var errs util.MultiError

	for _, x := range uploads {
		up := x.(*upload)
		if up.err != nil {
			errs = append(errs, up.err)
		} else {
			logs = append(logs, up.logs...)
		}
	}

	if s3FilesFilesystemId != "" && len(uploads) > 0 && len(errs) == 0 {
		// When using S3Files, wait after uploading output files before declaring the task
		// complete, or the user may attempt to access output files before they are accessible.
		// See https://docs.aws.amazon.com/AmazonS3/latest/userguide/s3-files-synchronization.html:
		// "S3 Files waits for a period of write inactivity (60 seconds) before exporting changes
		// back to your S3 bucket."
		// NOTE: The file uploads _start_ after 60s but may not complete for a while. Waiting for
		// outputs to be fully uploaded can be managed on the client side.
		logger.Debug("done uploading outputs, waiting for S3Files to sync")
		time.Sleep(60 * time.Second)
	}

	return logs, errs.ToError()
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
	d.ev.Info("download started", "url", d.in.Url)
}
func (d *download) Finished(objs []*storage.Object) {
	for _, obj := range objs {
		d.ev.Info("download finished", "url", d.in.Url, "size", obj.Size, "etag", obj.ETag)
	}
}
func (d *download) Failed(err error) {
	d.ev.Error("download failed", "url", d.in.Url, "error", err)
	d.cancel()
	d.err = err
}

type upload struct {
	ev  *events.TaskWriter
	out *tes.Output
	// In the GA4GH TES spec, the root-level `outputs` field defines the intended output files
	// declared when submitting a task, whereas `logs.outputs` records the actual result and
	// metadata of output files produced and uploaded after execution finishes.
	// A single `upload` action can therefore result in multiple `OutputFileLog` objects: when
	// uploading a directory, we record an output log for each file in the directory.
	logs []*tes.OutputFileLog
	err  error
}

func (u *upload) URL() string {
	return u.out.Url
}
func (u *upload) Path() string {
	return u.out.Path
}
func (u *upload) Started() {
	u.ev.Info("upload started", "url", u.out.Url)
}
func (u *upload) Finished(objs []*storage.Object) {
	u.logs = []*tes.OutputFileLog{}
	for _, obj := range objs {
		u.logs = append(u.logs,
			&tes.OutputFileLog{
				Url:       obj.URL,
				Path:      u.out.Path,
				SizeBytes: fmt.Sprintf("%d", obj.Size),
			},
		)
		u.ev.Info("upload finished", "url", obj.URL, "etag", obj.ETag, "size", obj.Size)
	}
}
func (u *upload) Failed(err error) {
	u.err = err
	u.ev.Error("upload failed", "url", u.out.Url, "error", err)
}

// fixLinks walks the output paths, fixing cases where a symlink is
// broken because it's pointing to a path inside a container volume.
func fixLinks(mapper *FileMapper, basepath string) {
	filepath.Walk(basepath, func(p string, f os.FileInfo, err error) error {
		if err != nil {
			// There's an error, so be safe and give up on this file
			return nil
		}

		// Only bother to check symlinks
		if f.Mode()&os.ModeSymlink != 0 {
			// Test if the file can be opened because it doesn't exist
			fh, rerr := os.Open(p)
			fh.Close()

			if rerr != nil && os.IsNotExist(rerr) {

				// Get symlink source path
				src, err := os.Readlink(p)
				if err != nil {
					return nil
				}
				// Map symlink source (possible container path) to host path
				mapped, err := mapper.HostPath(src)
				if err != nil {
					return nil
				}

				// Check whether the mapped path exists
				fh, err := os.Open(mapped)
				fh.Close()

				// If the mapped path exists, fix the symlink
				if err == nil {
					err := os.Remove(p)
					if err != nil {
						return nil
					}
					os.Symlink(mapped, p)
				}
			}
		}
		return nil
	})
}

// resolveWildcards resolves any wildcards/globs in the output paths.
func resolveWildcards(mapper *FileMapper) error {
	var outputs []*tes.Output

	for _, output := range mapper.Outputs {
		// If path contains a wildcard, handle globbing and upload each file individually
		if strings.Contains(output.Path, "*") {
			globs, err := filepath.Glob(output.Path)
			if err != nil {
				return fmt.Errorf("failed to resolve path %v: %v", output.Path, err)
			}

			for _, glob := range globs {
				out := proto.Clone(output).(*tes.Output)
				out.Path = glob

				// Construct URL by:
				// - removing the mapper.WorkDir from the output.Path
				// - then removing the PathPrefix
				// - then joining the output.URL
				globPath := strings.TrimPrefix(glob, mapper.WorkDir)
				fixPath := strings.TrimPrefix(globPath, output.PathPrefix)
				out.Url, err = url.JoinPath(output.Url, fixPath)
				if err != nil {
					return fmt.Errorf("failed to join URL: %v", err)
				}

				outputs = append(outputs, out)
			}
		} else {
			outputs = append(outputs, output)
		}
	}

	mapper.Outputs = outputs
	return nil
}
