package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// S3ReadMount records a worker mount resolved for one S3 backend.
// Credentials remain in worker configuration, outside the submitted plan.
type S3ReadMount struct {
	Endpoint string
	Bucket   string
	Prefix   string
	Root     string
}

type mountedS3Reader struct {
	prefix string
	root   string
}

func ValidateS3ReadMounts(mounts []S3ReadMount) error {
	seen := make(map[string]bool, len(mounts))
	for _, mount := range mounts {
		if mount.Endpoint == "" || mount.Bucket == "" || strings.ContainsAny(mount.Bucket, "/@?#\\ \x00") {
			return fmt.Errorf("mounted S3 reads require an endpoint and bucket")
		}
		if strings.HasPrefix(mount.Prefix, "/") {
			return fmt.Errorf("mounted S3 prefix %q must be bucket-relative", mount.Prefix)
		}
		if err := validateInputURL("s3://" + mount.Bucket + "/" + mount.Prefix); err != nil {
			return err
		}
		if !filepath.IsAbs(mount.Root) || filepath.Clean(mount.Root) != mount.Root {
			return fmt.Errorf("mounted S3 root %q must be absolute and clean", mount.Root)
		}
		identity := mount.Endpoint + "\x00" + mount.Bucket
		if seen[identity] {
			return fmt.Errorf("duplicate mounted S3 backend %q bucket %q", mount.Endpoint, mount.Bucket)
		}
		seen[identity] = true
	}
	return nil
}

func newMountedS3Reader(mount S3ReadMount) (*mountedS3Reader, error) {
	if err := ValidateS3ReadMounts([]S3ReadMount{mount}); err != nil {
		return nil, err
	}
	prefix := strings.TrimSuffix("s3://"+mount.Bucket+"/"+mount.Prefix, "/")
	return &mountedS3Reader{prefix: prefix, root: mount.Root}, nil
}

// Funnel's S3 backends use literal keys, including spaces, #, and percent signs.
// Do not URI-decode keys into different filesystem names.
func validateInputURL(raw string) error {
	rest, ok := strings.CutPrefix(raw, "s3://")
	host, key, _ := strings.Cut(rest, "/")
	if !ok || host == "" || strings.ContainsAny(host, "@?#\\ \x00") {
		return fmt.Errorf("invalid mounted S3 input prefix %q", raw)
	}
	for _, value := range []string{key, decodedInputKey(key)} {
		for _, component := range strings.Split(value, "/") {
			if component == "." || component == ".." || strings.ContainsAny(component, "\\\x00") {
				return fmt.Errorf("unsafe mounted input URL %q", raw)
			}
		}
		if strings.Contains(value, "//") {
			return fmt.Errorf("ambiguous mounted input path %q", raw)
		}
	}
	return nil
}

func decodedInputKey(key string) string {
	decoded, err := url.PathUnescape(key)
	if err != nil {
		return key
	}
	return decoded
}

func inputPrefix(value, prefix string) bool {
	return value == prefix || strings.HasPrefix(value, prefix+"/")
}

func (s *mountedS3Reader) resolve(raw string) (string, error) {
	if !inputPrefix(strings.TrimSuffix(raw, "/"), s.prefix) {
		return "", fmt.Errorf("URL %q is outside mounted S3 prefix", raw)
	}
	if err := validateInputURL(raw); err != nil {
		return "", err
	}
	rel := strings.TrimPrefix(strings.TrimPrefix(raw, s.prefix), "/")
	if rel == "" {
		rel = "."
	}
	return strings.TrimSuffix(rel, "/"), nil
}

func mountedObject(raw string, info fs.FileInfo) *Object {
	return &Object{URL: raw, Name: info.Name(), Size: info.Size(), LastModified: info.ModTime()}
}

func (s *mountedS3Reader) Stat(ctx context.Context, raw string) (*Object, error) {
	rel, err := s.resolve(raw)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return nil, fmt.Errorf("open mounted input root: %w", err)
	}
	defer root.Close() // Read-only directory handle; closing cannot affect copied data.
	info, err := root.Stat(rel)
	if err != nil {
		return nil, fmt.Errorf("stat mounted input: %w", err)
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return nil, fmt.Errorf("mounted input %q is not a regular file or directory", raw)
	}
	return mountedObject(raw, info), nil
}

func (s *mountedS3Reader) List(ctx context.Context, raw string) ([]*Object, error) {
	rel, err := s.resolve(raw)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return nil, fmt.Errorf("open mounted input root: %w", err)
	}
	defer root.Close() // Read-only directory handle.
	info, err := root.Stat(rel)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("mounted input %q is not a directory", raw)
	}
	var objects []*Object
	err = fs.WalkDir(root.FS(), rel, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := root.Stat(name)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("mounted directory entry %q is not a regular file", name)
		}
		fileURL := s.prefix + "/" + name
		objects = append(objects, mountedObject(fileURL, info))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list mounted input: %w", err)
	}
	return objects, nil
}

func (s *mountedS3Reader) Get(ctx context.Context, raw, destination string) (*Object, error) {
	rel, err := s.resolve(raw)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return nil, fmt.Errorf("open mounted input root: %w", err)
	}
	defer root.Close() // Read-only directory handle.
	source, err := openRootRegularFile(root, rel)
	if err != nil {
		return nil, fmt.Errorf("open mounted input: %w", err)
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil {
		return nil, err
	}
	targetInfo, err := os.Lstat(destination)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if targetInfo != nil && targetInfo.Mode().IsRegular() && os.SameFile(info, targetInfo) {
		return nil, errors.New("mounted input source and destination must differ")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return nil, err
	}
	destinationRoot, err := os.OpenRoot(filepath.Dir(destination))
	if err != nil {
		return nil, fmt.Errorf("open mounted input destination directory: %w", err)
	}
	defer destinationRoot.Close()
	err = writeRootDownloadedFile(ctx, destinationRoot, filepath.Base(destination), func(target *os.File) error {
		size, err := io.Copy(target, inputReader{ctx: ctx, source: source})
		if err != nil {
			return err
		}
		if size != info.Size() {
			return fmt.Errorf("mounted input source size changed during copy")
		}
		current, err := source.Stat()
		if err != nil {
			return err
		}
		if current.Size() != info.Size() {
			return fmt.Errorf("mounted input source size changed during copy")
		}
		return ctx.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("copy mounted input: %w", err)
	}
	return mountedObject(raw, info), nil
}

type inputReader struct {
	ctx    context.Context
	source io.Reader
}

func (r inputReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.source.Read(p)
}
