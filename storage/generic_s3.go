package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	neturl "net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/minio/minio-go/v7/pkg/encrypt"
	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/logger"
	s3util "github.com/ohsu-comp-bio/funnel/util/s3"
)

// GenericS3 provides access to an S3 object store.
type GenericS3 struct {
	client        *minio.Client
	endpoint      string
	kmskeyId      string
	MountedBucket string
}

// NewGenericS3 creates a new GenericS3 instance, given an endpoint URL
// and a set of authentication credentials.
func NewGenericS3(conf *config.GenericS3Storage) (*GenericS3, error) {

	var endpoint string
	if conf.Endpoint != "" {
		endpoint = s3util.ParseEndpoint(conf.Endpoint)
	}

	logger.Debug("generics3: connecting to endpoint", "endpoint", endpoint)

	client, err := minio.New(
		endpoint,
		&minio.Options{
			Creds:  credentials.NewStaticV4(conf.Key, conf.Secret, ""),
			Secure: strings.HasPrefix(conf.Endpoint, "https"),
			Region: conf.Region,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("error creating generic s3 backend: %v", err)
	}

	return &GenericS3{client, endpoint + "/", conf.KmsKeyID, conf.Bucket}, nil
}

// `The MountedBucket` logic assumes that there is a mounted volume (S3 mountpoint or
// S3Files), which should be the case when Funnel is deployed through Kubernetes and the
// task has inputs/outputs. If this assumption causes issues, we can add a config flag to
// enable/disable this logic.
// Note: this is currently only implemented for GenericS3, not for AmazonS3.
func (s3 *GenericS3) IsThisBucketMounted(bucketName string) bool {
	return bucketName == s3.MountedBucket
}

func (s3 *GenericS3) GetLocalMountedPath(url string) string {
	return "/opt/funnel/funnel-work-dir/" + strings.TrimPrefix(url, "s3://"+s3.MountedBucket+"/")
}

// Returns true if a remote S3 object is a directory, false otherwise
func (s3 *GenericS3) isDir(ctx context.Context, minioClient *minio.Client, url string, bucketName string, objectName string) (bool, error) {
	if s3.IsThisBucketMounted(bucketName) { // skip call to S3 if bucket is mounted
		fileInfo, err := os.Stat(s3.GetLocalMountedPath(url))
		if err != nil {
			return false, err
		}
		return fileInfo.IsDir(), nil
	}

	// Check if the objectName ends with '/' - often used to represent 'folders'
	if strings.HasSuffix(objectName, "/") {
		return true, nil
	}

	// List objects with the prefix to see if there are multiple keys with the given prefix
	// Recursively list all objects
	recursive := true
	for object := range minioClient.ListObjects(ctx, bucketName, minio.ListObjectsOptions{Prefix: objectName, Recursive: recursive}) {
		if object.Err != nil {
			return false, object.Err
		}

		// If any object's key starts with the objectName and is not equal, it's a directory
		if strings.HasPrefix(object.Key, objectName) && object.Key != objectName {
			return true, nil
		}
	}

	// If no objects share the prefix or the objectName exactly matches a key, it's considered a file
	return false, nil
}

// Stat returns information about the object at the given storage URL.
func (s3 *GenericS3) Stat(ctx context.Context, url string) (*Object, error) {
	u, err := s3.Parse(url)
	if err != nil {
		return nil, err
	}

	if s3.IsThisBucketMounted(u.bucket) { // skip call to S3 if bucket is mounted
		local := &Local{}
		obj, err := local.Stat(ctx, s3.GetLocalMountedPath(url))
		if err != nil {
			return nil, err
		}
		obj.URL = url // overwrite the local path returned by `Stat`, set it to the original url
		return obj, nil
	}

	opts := minio.GetObjectOptions{}
	logger.Debug("genericS3: s3.client.GetObject", "bucket", u.bucket, "path", u.path)
	obj, err := s3.client.GetObject(ctx, u.bucket, u.path, opts)
	if err != nil {
		logger.Debug("genericS3: getting object from s3.client.GetObject", "url", url, "error", err)
		return nil, fmt.Errorf("genericS3: getting object %s in bucket %s: %s", u.path, u.bucket, err)
	}

	isDir, err := s3.isDir(ctx, s3.client, url, u.bucket, u.path)
	if err != nil {
		return nil, fmt.Errorf("genericS3: stat object %s in bucket %s: %s", u.path, u.bucket, err)
	}
	if isDir {
		return &Object{
			URL:  url,
			Name: u.path,
			Size: 0,
		}, nil
	}

	info, err := obj.Stat()
	if err != nil {
		return nil, fmt.Errorf("genericS3: stat object %s in bucket %s: %s", u.path, u.bucket, err)
	}

	return &Object{
		URL:          url,
		Name:         info.Key,
		ETag:         info.ETag,
		LastModified: info.LastModified,
		Size:         info.Size,
	}, nil
}

// List lists the objects at the given url.
func (s3 *GenericS3) List(ctx context.Context, url string) ([]*Object, error) {
	u, err := s3.Parse(url)
	if err != nil {
		return nil, err
	}

	var objects []*Object

	if s3.IsThisBucketMounted(u.bucket) { // skip call to S3 if bucket is mounted
		local := &Local{}
		objects, err = local.List(ctx, s3.GetLocalMountedPath(url))
		if err != nil {
			return nil, err
		}
	} else {
		// Recursively list all objects.
		recursive := true
		listRes := s3.client.ListObjects(ctx, u.bucket, minio.ListObjectsOptions{Prefix: u.path, Recursive: recursive})
		for info := range listRes {
			if info.Err != nil {
				return nil, info.Err
			}
			// check if key represents a directory
			if strings.HasSuffix(info.Key, "/") {
				continue
			}
			objects = append(objects, &Object{
				URL:          "s3://" + u.bucket + "/" + info.Key,
				Name:         info.Key,
				ETag:         info.ETag,
				LastModified: info.LastModified,
				Size:         info.Size,
			})
		}
	}

	return objects, nil
}

// Get copies an object from S3 to the host path.
func (s3 *GenericS3) Get(ctx context.Context, url, path string) (*Object, error) {
	logger.Debug("genericS3: 'Get' called", "url", url, "path", path)

	obj, err := s3.Stat(ctx, url)
	if err != nil {
		return nil, err
	}

	u, err := s3.Parse(url)
	if err != nil {
		return nil, err
	}

	// _GetObject: utility function that either copies a file from the mounted bucket, or downloads it if the source is not the mounted S3 bucket
	_GetObject := func(sourcePath string, destPath string) error {
		if s3.IsThisBucketMounted(u.bucket) { // skip call to S3 if bucket is mounted
			// TODO (MIDRC-1353) Add "funnel-temp-files" prefix back to separate temp files from the rest
			logger.Debug("genericS3: getting input from mounted bucket", "sourcePath", sourcePath)
			// use the path to the mounted bucket instead of the file's S3 URL
			localMountedPath := s3.GetLocalMountedPath(url)
			err = copyFile(ctx, localMountedPath, path)
			if err != nil {
				return fmt.Errorf("genericS3: failed to copy file %s to %s: %v", localMountedPath, path, err)
			}
		} else {
			logger.Debug("genericS3: not getting input from mounted bucket, input URL does not match", "sourcePath", sourcePath)
			err = download(ctx, s3.client, u.bucket, sourcePath, destPath, s3.kmskeyId)
			if err != nil {
				return fmt.Errorf("genericS3: getting object %s: %v", url, err)
			}
		}
		return nil
	}

	isDir, err := s3.isDir(ctx, s3.client, url, u.bucket, u.path)
	if err != nil {
		logger.Debug("genericS3: error while checking directory", "bucket", u.bucket, "path", u.path, "err", err)
		return nil, fmt.Errorf("genericS3: getting object from isDir %s: %v", url, err)
	}
	if isDir {
		objects, err := s3.List(ctx, url)
		if err != nil {
			return nil, err
		}

		for _, obj := range objects {
			relPath, err := filepath.Rel(u.path, obj.Name)
			if err != nil {
				return nil, fmt.Errorf("genericS3: computing relative path for %s: %v", obj.Name, err)
			}
			err = _GetObject(obj.Name, filepath.Join(path, relPath))
			if err != nil {
				return nil, fmt.Errorf("genericS3: getting nested object %s: %v", url, err)
			}
		}
	} else {
		err = _GetObject(u.path, path)
		if err != nil {
			return nil, err
		}
	}

	return obj, nil
}

// download streams an object to a file without using os.Rename
func download(ctx context.Context, client *minio.Client, bucket, objectPath, filePath, kmskeyId string) error {
	opts := minio.GetObjectOptions{}
	if kmskeyId != "" {
		logger.Debug("genericS3: using KMS encryption", "kmsKeyId", kmskeyId)
		SSEKMS, err := encrypt.NewSSEKMS(kmskeyId, ctx)
		if err != nil {
			return fmt.Errorf("genericS3: download(): creating SSEKMS: %v", err)
		}
		opts.ServerSideEncryption = SSEKMS
	}

	logger.Debug("genericS3: downloading object", "bucket", bucket, "objectPath", objectPath, "filePath", filePath)
	// Step 1: Get the object stream
	// TODO: Add logging with file contents here...
	obj, err := client.GetObject(ctx, bucket, objectPath, opts)
	if err != nil {
		return fmt.Errorf("failed getting object from S3: %w", err)
	}
	defer obj.Close()

	// Step 2: Create the local file (overwrite if exists)
	logger.Debug("genericS3: creating local file", "filePath", filePath)
	dir := filepath.Dir(filePath)
	err = os.MkdirAll(dir, 0755)
	if err != nil {
		return fmt.Errorf("failed creating directories: %w", err)
	}

	outFile, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("failed creating file: %w", err)
	}

	// Step 3: Copy the contents
	logger.Debug("genericS3: io.Copy", "outFile", outFile.Name(), "obj", obj)
	if _, err := io.Copy(outFile, obj); err != nil {
		outFile.Close()
		return fmt.Errorf("failed writing file: %w", err)
	}

	// Close explicitly (not via defer) so Mountpoint begins flushing to S3
	// before we poll for readability below.
	if err := outFile.Close(); err != nil {
		return fmt.Errorf("failed closing file: %w", err)
	}

	// Wait for the file to become readable. When the path is on a
	// Mountpoint-for-S3 backed filesystem, closing the write handle triggers
	// an async S3 upload; os.Stat returns EPERM until that upload finishes.
	const mountpointFlushTimeout = 5 * time.Minute
	const mountpointFlushInterval = 2 * time.Second
	deadline := time.Now().Add(mountpointFlushTimeout)
	for {
		_, err := os.Stat(filePath)
		if err == nil {
			break
		}
		if time.Now().Before(deadline) && (errors.Is(err, syscall.EPERM) || errors.Is(err, os.ErrNotExist)) {
			logger.Debug("genericS3: waiting for input file to be readable after write", "path", filePath, "error", err)
			time.Sleep(mountpointFlushInterval)
			continue
		}
		return fmt.Errorf("failed waiting for file to become readable: %w", err)
	}

	return nil
}

// Put copies an object (file) from the host path to S3.
// Update Put function to be able to upload directories (a la Get() function)
func (s3 *GenericS3) Put(ctx context.Context, url, path string) ([]*Object, error) {
	u, err := s3.Parse(url)
	if err != nil {
		return nil, err
	}

	opts := minio.PutObjectOptions{}
	if s3.kmskeyId != "" {
		logger.Debug("genericS3: using KMS encryption for upload", "kmsKeyId", s3.kmskeyId)
		SSEKMS, err := encrypt.NewSSEKMS(s3.kmskeyId, ctx)
		if err != nil {
			return nil, fmt.Errorf("genericS3: Put(): creating SSEKMS: %v", err)
		}
		opts.ServerSideEncryption = SSEKMS
	}

	// Wait for the local path to become readable. When the local path is on a
	// Mountpoint-for-S3 backed filesystem and the file was written by another
	// mount instance (the executor pod), os.Stat returns EPERM until Mountpoint
	// finishes flushing the write to S3. Poll until the file is readable or the
	// timeout expires.
	const mountpointFlushTimeout = 5 * time.Minute
	const mountpointFlushInterval = 2 * time.Second
	deadline := time.Now().Add(mountpointFlushTimeout)
	var fileInfo os.FileInfo
	for {
		var err error
		fileInfo, err = os.Stat(path)
		if err == nil {
			break
		}
		if time.Now().Before(deadline) && (errors.Is(err, syscall.EPERM) || errors.Is(err, os.ErrNotExist)) {
			logger.Debug("genericS3: waiting for output file to become readable", "path", path, "error", err)
			time.Sleep(mountpointFlushInterval)
			continue
		}
		return nil, fmt.Errorf("genericS3: putting object %s: %v", url, err)
	}

	// _PutObject: utility function that either copies a file to the mounted bucket, or uploads it
	// if the destination is not the mounted S3 bucket
	_PutObject := func(sourcePath string, destPath string) error {
		if s3.IsThisBucketMounted(u.bucket) { // skip call to S3 if bucket is mounted
			// TODO (MIDRC-1353) Add "funnel-temp-files" prefix back to separate temp files from the rest
			logger.Debug("genericS3: outputting to mounted bucket", "destPath", destPath)
			// use the path to the mounted bucket instead of the file's S3 URL
			localMountedPath := s3.GetLocalMountedPath(destPath)
			err = copyFile(ctx, sourcePath, localMountedPath)
			if err != nil {
				return fmt.Errorf("genericS3: failed to copy file %s to %s: %v", sourcePath, localMountedPath, err)
			}
		} else {
			logger.Debug("genericS3: not outputting to mounted bucket, output URL does not match", "destPath", destPath)
			_, err = s3.client.FPutObject(ctx, u.bucket, destPath, sourcePath, opts)
			if err != nil {
				return fmt.Errorf("genericS3: putting object %s: %v", url, err)
			}
		}
		return nil
	}

	objs := []*Object{}

	// Check if the path is a directory
	if fileInfo.IsDir() {
		// Walk the directory and upload all files and subdirectories
		err = filepath.Walk(path, func(filePath string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !info.IsDir() {
				// Upload the file
				relativePath, err := filepath.Rel(path, filePath)
				if err != nil {
					return err
				}
				uploadPath := filepath.Join(u.path, relativePath)
				err = _PutObject(filePath, uploadPath)
				if err != nil {
					return fmt.Errorf("genericS3: putting nested object %s: %v", url, err)
				}
				fullUrl, err := neturl.JoinPath(url, relativePath)
				if err != nil {
					return fmt.Errorf("genericS3: generating url for nested object %s %s: %v", url, relativePath, err)
				}
				obj, err := s3.Stat(ctx, fullUrl)
				if err != nil {
					return err
				}
				objs = append(objs, obj)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	} else {
		err := _PutObject(path, u.path)
		if err != nil {
			return nil, err
		}
		obj, err := s3.Stat(ctx, url)
		if err != nil {
			return nil, err
		}
		objs = append(objs, obj)
	}

	return objs, nil
}

// Join joins the given URL with the given subpath.
func (s3 *GenericS3) Join(url, path string) (string, error) {
	return strings.TrimSuffix(url, "/") + "/" + path, nil
}

// UnsupportedOperations describes which operations (Get, Put, etc) are not
// supported for the given URL.
func (s3 *GenericS3) UnsupportedOperations(url string) UnsupportedOperations {
	u, err := s3.Parse(url)
	if err != nil {
		return AllUnsupported(err)
	}
	if s3.IsThisBucketMounted(u.bucket) { // skip call to S3 if bucket is mounted
		return AllSupported()
	}
	ok, err := s3.client.BucketExists(context.Background(), u.bucket)
	if err != nil {
		err = fmt.Errorf("genericS3: failed to find bucket %q: %v", u.bucket, err)
		return AllUnsupported(err)
	}
	if !ok {
		err := fmt.Errorf("genericS3: bucket does not exist: %q", u.bucket)
		return AllUnsupported(err)
	}
	return AllSupported()
}

func (s3 *GenericS3) Parse(rawurl string) (*urlparts, error) {
	if !strings.HasPrefix(rawurl, s3Protocol) {
		return nil, &ErrUnsupportedProtocol{"genericS3"}
	}

	path := strings.TrimPrefix(rawurl, s3Protocol)
	path = strings.TrimPrefix(path, s3.endpoint)
	if path == "" {
		return nil, &ErrInvalidURL{"genericS3"}
	}

	split := strings.SplitN(path, "/", 2)
	url := &urlparts{}
	if len(split) > 0 {
		url.bucket = split[0]
	}
	if len(split) == 2 {
		url.path = split[1]
	}
	return url, nil
}
