package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	awss3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// S3WriteMount records an explicit destination and its publication deadline.
type S3WriteMount struct {
	Endpoint string
	Bucket   string
	Prefix   string
	Root     string
	Timeout  time.Duration
}

type mountedS3Writer struct {
	mount        mountedS3Reader
	timeout      time.Duration
	pollInterval time.Duration
}

func ValidateS3WriteMounts(mounts []S3WriteMount) error {
	roots := make([]S3ReadMount, 0, len(mounts))
	for _, mount := range mounts {
		if mount.Timeout <= 0 {
			return fmt.Errorf("mounted S3 write timeout must be positive")
		}
		roots = append(roots, S3ReadMount{Endpoint: mount.Endpoint, Bucket: mount.Bucket, Prefix: mount.Prefix, Root: mount.Root})
	}
	if err := ValidateS3ReadMounts(roots); err != nil {
		return err
	}
	for i, mount := range mounts {
		for _, other := range mounts[:i] {
			if pathOverlaps(mount.Root, other.Root) {
				return fmt.Errorf("mounted S3 write roots %q and %q overlap", other.Root, mount.Root)
			}
		}
	}
	return nil
}

func validateS3WriteMountRootAliases(mounts []S3WriteMount) error {
	for i, mount := range mounts {
		for _, other := range mounts[:i] {
			resolvedMount, mountErr := filepath.EvalSymlinks(mount.Root)
			resolvedOther, otherErr := filepath.EvalSymlinks(other.Root)
			if mountErr == nil && otherErr == nil && pathOverlaps(resolvedMount, resolvedOther) {
				return fmt.Errorf("mounted S3 write roots %q and %q resolve to overlapping directories", other.Root, mount.Root)
			}
			mountInfo, mountErr := os.Stat(mount.Root)
			otherInfo, otherErr := os.Stat(other.Root)
			if mountErr == nil && otherErr == nil && os.SameFile(mountInfo, otherInfo) {
				return fmt.Errorf("mounted S3 write roots %q and %q refer to the same directory", other.Root, mount.Root)
			}
		}
	}
	return nil
}

func pathOverlaps(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	return a == b || strings.HasPrefix(a, pathPrefix(b)) || strings.HasPrefix(b, pathPrefix(a))
}

func pathPrefix(value string) string {
	if value == string(filepath.Separator) {
		return value
	}
	return value + string(filepath.Separator)
}

func (s3 *GenericS3) mountedWriteURL(raw string) (string, bool) {
	if s3.mountedWrite == nil {
		return "", false
	}
	u, err := s3.parse(raw)
	if err != nil {
		return "", false
	}
	canonical := "s3://" + u.bucket + "/" + u.path
	return canonical, inputPrefix(strings.TrimSuffix(canonical, "/"), s3.mountedWrite.mount.prefix)
}

func (s3 *GenericS3) putMounted(ctx context.Context, raw, sourcePath string) (*Object, error) {
	ctx, cancel := context.WithTimeout(ctx, s3.mountedWrite.timeout)
	defer cancel()
	canonical, _ := s3.mountedWriteURL(raw)
	relative, err := s3.mountedWrite.mount.resolve(canonical)
	if err != nil {
		return nil, err
	}
	info, err := waitForLocalFile(ctx, sourcePath)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(s3.mountedWrite.mount.root)
	if err != nil {
		return nil, fmt.Errorf("open mounted output root: %w", err)
	}
	defer root.Close()
	if !info.IsDir() {
		return s3.publishMountedFile(ctx, root, relative, raw, sourcePath)
	}
	result := &Object{URL: raw}
	err = filepath.WalkDir(sourcePath, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		suffix, err := filepath.Rel(sourcePath, name)
		if err != nil {
			return err
		}
		fileURL := strings.TrimSuffix(raw, "/") + "/" + filepath.ToSlash(suffix)
		canonical, _ := s3.mountedWriteURL(fileURL)
		destination, err := s3.mountedWrite.mount.resolve(canonical)
		if err != nil {
			return err
		}
		obj, err := s3.publishMountedFile(ctx, root, destination, fileURL, name)
		if err != nil {
			return err
		}
		result.Size += obj.Size
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s3 *GenericS3) publishMountedFile(ctx context.Context, root *os.Root, destination, raw, sourcePath string) (*Object, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	source, err := openRegularFile(sourcePath)
	if err != nil {
		return nil, fmt.Errorf("open mounted output source %q: %w", sourcePath, err)
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil {
		return nil, err
	}
	if err := root.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return nil, err
	}
	existing, err := root.Lstat(destination)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if existing != nil && !existing.Mode().IsRegular() {
		return nil, fmt.Errorf("mounted output destination is not a regular file")
	}
	if existing != nil && existing.Mode().IsRegular() && os.SameFile(info, existing) {
		return s3.waitForMountedOutput(ctx, raw, info.Size())
	}
	var copied int64
	err = writeRootFileAtomicallyWithMode(ctx, root, destination, 0600, func(target *os.File) error {
		var copyErr error
		copied, copyErr = io.Copy(target, inputReader{ctx: ctx, source: source})
		if copyErr != nil {
			return copyErr
		}
		if copied != info.Size() {
			return fmt.Errorf("mounted output source size changed during copy")
		}
		current, err := source.Stat()
		if err != nil {
			return err
		}
		if current.Size() != info.Size() {
			return fmt.Errorf("mounted output source size changed during copy")
		}
		return ctx.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("copy mounted output: %w", err)
	}
	return s3.waitForMountedOutput(ctx, raw, info.Size())
}

func (s3 *GenericS3) waitForMountedOutput(ctx context.Context, raw string, expected int64) (*Object, error) {
	u, err := s3.parse(raw)
	if err != nil {
		return nil, err
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("waiting for mounted output %s: %w", raw, err)
		}
		// Query S3 directly; GenericS3.Stat can read the mount instead.
		info, err := s3.client(ctx, u.bucket).HeadObject(ctx, &awss3.HeadObjectInput{
			Bucket: aws.String(u.bucket),
			Key:    aws.String(u.path),
		})
		if err != nil {
			var nf *awss3types.NotFound
			var ns *awss3types.NoSuchBucket
			if !(errors.As(err, &nf) || errors.As(err, &ns)) {
				return nil, fmt.Errorf("checking mounted output %s: %w", raw, err)
			}
		} else if aws.ToInt64(info.ContentLength) == expected {
			return &Object{URL: raw, Name: u.path, Size: aws.ToInt64(info.ContentLength), ETag: unquoteETag(aws.ToString(info.ETag)), LastModified: aws.ToTime(info.LastModified)}, nil
		}
		timer := time.NewTimer(s3.mountedWrite.pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, fmt.Errorf("waiting for mounted output %s: %w", raw, ctx.Err())
		case <-timer.C:
		}
	}
}
