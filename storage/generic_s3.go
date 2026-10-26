package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/logger"
	s3util "github.com/calypr/funnel/util/s3"
)

// GenericS3 provides access to an S3 object store.
type GenericS3 struct {
	// api client state
	cfg      aws.Config
	endpoint string
	// baseURL is the endpoint URL with scheme, used as the SDK BaseEndpoint.
	baseURL string
	// pathStyle is true for S3-compatible stores; false for real AWS
	// endpoints, which use virtual-host addressing.
	pathStyle bool
	region    string
	// regionSet is true when the user explicitly configured a region.
	regionSet bool
	kmskeyId  string

	once sync.Once
	s3c  *s3.Client

	// mounted (CSI mountpoint) fast paths
	bucket             string
	configuredEndpoint string
	mountedRead        *mountedS3Reader
	mountedWrite       *mountedS3Writer
}

// NewGenericS3 creates a new GenericS3 instance, given an endpoint URL
// and a set of authentication credentials.
func NewGenericS3(conf *config.GenericS3Storage) (*GenericS3, error) {

	var endpoint string
	if conf.Endpoint != "" {
		endpoint = s3util.ParseEndpoint(conf.Endpoint)
	}

	region := conf.Region
	if region == "" {
		region = "us-east-1"
	}

	logger := logger.NewLogger("GenericS3", logger.DefaultConfig())
	logger.Debug("generics3: connecting to endpoint", "endpoint", endpoint)

	pathStyle := endpoint != "" && !strings.Contains(endpoint, ".amazonaws.com")

	loadOpts := []func(*awscfg.LoadOptions) error{
		awscfg.WithRegion(region),
	}
	if conf.Key != "" && conf.Secret != "" {
		loadOpts = append(loadOpts,
			awscfg.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(conf.Key, conf.Secret, "")))
	}
	awsConfig, err := awscfg.LoadDefaultConfig(context.Background(), loadOpts...)
	if err != nil {
		return nil, fmt.Errorf("error creating generic s3 backend: %v", err)
	}
	if conf.Key == "" || conf.Secret == "" {
		// No credentials configured: access anonymously (public buckets),
		// matching the behavior of the previous minio-based client.
		awsConfig.Credentials = aws.AnonymousCredentials{}
	}

	baseURL := ""
	if endpoint != "" {
		baseURL = schemeEndpoint(conf.Endpoint, endpoint)
	}

	return &GenericS3{
		cfg:                awsConfig,
		endpoint:           endpoint + "/",
		baseURL:            baseURL,
		pathStyle:          pathStyle,
		region:             region,
		regionSet:          conf.Region != "",
		kmskeyId:           conf.KmsKeyID,
		bucket:             conf.Bucket,
		configuredEndpoint: conf.Endpoint,
	}, nil
}

// unquoteETag strips the surrounding double quotes the S3 API puts around
// ETag values (the previous minio-based client returned them unquoted).
func unquoteETag(etag string) string {
	return strings.Trim(etag, `"`)
}

// schemeEndpoint rebuilds a full endpoint URL (with scheme) from the raw
// configured endpoint and the parsed host.
func schemeEndpoint(rawEndpoint, host string) string {
	if strings.HasPrefix(rawEndpoint, "https://") {
		return "https://" + host
	}
	return "http://" + host
}

// client lazily builds the S3 client, resolving the bucket region for AWS
// endpoints on first use.
func (s3b *GenericS3) client(ctx context.Context, bucket string) *s3.Client {
	s3b.once.Do(func() {
		region := s3b.region
		if !s3b.pathStyle && !s3b.regionSet {
			// AWS endpoint without a configured region: resolve the
			// bucket's region so cross-region buckets don't redirect.
			if r, err := s3b.bucketRegion(ctx, bucket); err == nil && r != "" {
				region = r
			}
		}
		opts := []func(*s3.Options){
			func(o *s3.Options) {
				o.UsePathStyle = s3b.pathStyle
				o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
				o.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
			},
		}
		if s3b.pathStyle && s3b.baseURL != "" {
			// Custom (non-AWS) endpoint: always address it directly.
			opts = append(opts, func(o *s3.Options) {
				o.BaseEndpoint = aws.String(s3b.baseURL)
			})
		}
		// For AWS endpoints the SDK builds the regional endpoint from the
		// resolved region (virtual-host addressing), so no BaseEndpoint.
		s3b.s3c = s3.NewFromConfig(s3b.cfg, append(opts, func(o *s3.Options) {
			o.Region = region
		})...)
	})
	return s3b.s3c
}

// bucketRegion resolves the region of an AWS bucket via the
// X-Amz-Bucket-Region header returned on a HEAD bucket request.
func (s3b *GenericS3) bucketRegion(ctx context.Context, bucket string) (string, error) {
	client := s3.NewFromConfig(s3b.cfg, func(o *s3.Options) {
		o.Region = "us-east-1"
		o.Credentials = nil
		o.UsePathStyle = false
	})
	return manager.GetBucketRegion(ctx, client, bucket)
}

// isDir returns true if a remote S3 object is a directory, false otherwise
func (s3b *GenericS3) isDir(ctx context.Context, bucketName, objectName string) (bool, error) {
	// Check if the objectName ends with '/' - often used to represent 'folders'
	if strings.HasSuffix(objectName, "/") {
		return true, nil
	}

	// List objects with the prefix to see if there are multiple keys with the given prefix
	pages := s3.NewListObjectsV2Paginator(s3b.client(ctx, bucketName), &s3.ListObjectsV2Input{
		Bucket: aws.String(bucketName),
		Prefix: aws.String(objectName),
	})
	seenExact := false
	seenPrefix := false
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return false, err
		}
		for _, obj := range page.Contents {
			key := aws.ToString(obj.Key)
			if key == objectName {
				seenExact = true
			} else if strings.HasPrefix(key, objectName) {
				seenPrefix = true
			}
		}
	}
	// The requested key wins over sibling keys sharing the prefix: an
	// existing object is a file even when sample.bam.bai sits next to
	// sample.bam. If no objects share the prefix, it's a file too.
	return !seenExact && seenPrefix, nil
}

// Stat returns information about the object at the given storage URL.
func (s3b *GenericS3) Stat(ctx context.Context, url string) (*Object, error) {
	if raw, ok := s3b.mountedURL(url); ok {
		obj, err := s3b.mountedRead.Stat(ctx, raw)
		if obj != nil {
			obj.URL = url
		}
		return obj, err
	}
	logger := logger.NewLogger("GenericS3", logger.DefaultConfig())
	u, err := s3b.parse(url)
	if err != nil {
		return nil, err
	}

	logger.Debug("genericS3: s3.client.HeadObject", "bucket", u.bucket, "path", u.path)
	res, err := s3b.client(ctx, u.bucket).HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(u.bucket),
		Key:    aws.String(u.path),
	})
	if err != nil {
		logger.Debug("genericS3: getting object from s3.client.HeadObject", "url", url, "error", err)
		// the key may represent a directory prefix rather than an object
		isDir, derr := s3b.isDir(ctx, u.bucket, u.path)
		if derr == nil && isDir {
			return &Object{
				URL:  url,
				Name: u.path,
				Size: 0,
			}, nil
		}
		return nil, fmt.Errorf("genericS3: stat object %s in bucket %s: %s", u.path, u.bucket, err)
	}

	return &Object{
		URL:          url,
		Name:         u.path,
		ETag:         unquoteETag(aws.ToString(res.ETag)),
		LastModified: aws.ToTime(res.LastModified),
		Size:         aws.ToInt64(res.ContentLength),
	}, nil
}

// List lists the objects at the given url.
func (s3b *GenericS3) List(ctx context.Context, url string) ([]*Object, error) {
	if raw, ok := s3b.mountedURL(url); ok {
		objects, err := s3b.mountedRead.List(ctx, raw)
		for _, obj := range objects {
			obj.URL = strings.TrimSuffix(url, "/") + strings.TrimPrefix(obj.URL, strings.TrimSuffix(raw, "/"))
		}
		return objects, err
	}
	u, err := s3b.parse(url)
	if err != nil {
		return nil, err
	}

	// Preserve endpoint-qualified URLs (e.g. "s3://host:port/bucket/key")
	// by building result URLs from the requested URL's base.
	base := url
	if u.path != "" {
		if !strings.HasSuffix(base, u.path) {
			return nil, fmt.Errorf("genericS3: listing URL %q does not end with parsed path %q", url, u.path)
		}
		base = strings.TrimSuffix(base, u.path)
	} else if !strings.HasSuffix(base, "/") {
		base += "/"
	}

	// Recursively list all objects.
	var objects []*Object
	pages := s3.NewListObjectsV2Paginator(s3b.client(ctx, u.bucket), &s3.ListObjectsV2Input{
		Bucket: aws.String(u.bucket),
		Prefix: aws.String(u.path),
	})
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, info := range page.Contents {
			key := aws.ToString(info.Key)
			if !strings.HasPrefix(key, u.path) {
				continue
			}
			// check if key represents a directory
			if strings.HasSuffix(key, "/") {
				continue
			}
			objects = append(objects, &Object{
				URL:          base + key,
				Name:         key,
				ETag:         unquoteETag(aws.ToString(info.ETag)),
				LastModified: aws.ToTime(info.LastModified),
				Size:         aws.ToInt64(info.Size),
			})
		}
	}

	return objects, nil
}

// Get copies an object from S3 to the host path.
func (s3b *GenericS3) Get(ctx context.Context, url, path string) (*Object, error) {
	if raw, ok := s3b.mountedURL(url); ok {
		obj, err := s3b.mountedRead.Get(ctx, raw, path)
		if obj != nil {
			obj.URL = url
		}
		return obj, err
	}
	logger := logger.NewLogger("GenericS3", logger.DefaultConfig())
	logger.Debug("genericS3: Get called", "url", url, "path", path)

	obj, err := s3b.Stat(ctx, url)
	if err != nil {
		return nil, err
	}

	u, err := s3b.parse(url)
	if err != nil {
		return nil, err
	}

	isDir, err := s3b.isDir(ctx, u.bucket, u.path)
	if err != nil {
		logger.Debug("genericS3: checking directory", "bucket", u.bucket, "path", u.path)
		return nil, fmt.Errorf("genericS3: getting object from isDir %s: %v", url, err)
	}
	if isDir {
		objects, err := s3b.List(ctx, url)
		if err != nil {
			return nil, err
		}

		for _, obj := range objects {
			relPath, err := filepath.Rel(u.path, obj.Name)
			if err != nil {
				return nil, fmt.Errorf("genericS3: computing relative path for %s: %v", obj.Name, err)
			}
			err = s3b.download(ctx, u.bucket, obj.Name, filepath.Join(path, relPath))
			if err != nil {
				return nil, err
			}
		}
	} else {
		err = s3b.download(ctx, u.bucket, u.path, path)
		if err != nil {
			return nil, fmt.Errorf("genericS3: getting object from download %s: %v", url, err)
		}
	}

	return obj, nil
}

// download streams an object to a local file, staged atomically.
func (s3b *GenericS3) download(ctx context.Context, bucket, objectPath, filePath string) error {
	logger := logger.NewLogger("GenericS3", logger.DefaultConfig())

	logger.Debug("genericS3: downloading object", "bucket", bucket, "objectPath", objectPath, "filePath", filePath)
	// Step 1: Get the object stream
	res, err := s3b.client(ctx, bucket).GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(objectPath),
	})
	if err != nil {
		return fmt.Errorf("failed getting object from S3: %w", err)
	}
	defer res.Body.Close()

	logger.Debug("genericS3: staging local file", "filePath", filePath)
	err = writeDownloadedFile(ctx, filePath, func(outFile *os.File) error {
		logger.Debug("genericS3: io.Copy", "outFile", outFile.Name(), "bucket", bucket)
		if _, err := io.Copy(outFile, res.Body); err != nil {
			return fmt.Errorf("failed writing file: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed writing local file: %w", err)
	}

	if _, err := waitForLocalFile(ctx, filePath); err != nil {
		return fmt.Errorf("failed waiting for input file to become readable: %w", err)
	}
	return nil
}

func waitForLocalFile(ctx context.Context, path string) (os.FileInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		info, err := os.Stat(path)
		if err == nil {
			return info, nil
		}
		if !errors.Is(err, syscall.EPERM) && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		timer := time.NewTimer(2 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

// Put publishes a local file through an API upload or a configured mounted write.
// Mounted writes verify remote existence and size; API uploads require an upload response.
// Directory publication returns aggregate metadata after every file succeeds.
func (s3b *GenericS3) Put(ctx context.Context, url, path string) (*Object, error) {
	if _, matched := s3b.mountedWriteURL(url); matched {
		return s3b.putMounted(ctx, url, path)
	}
	u, err := s3b.parse(url)
	if err != nil {
		return nil, err
	}

	fileInfo, err := waitForLocalFile(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("genericS3: waiting for output file %s: %w", path, err)
	}

	if fileInfo.IsDir() {
		obj := &Object{URL: url, Name: u.path}
		err = filepath.Walk(path, func(filePath string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if !info.IsDir() {
				relativePath, err := filepath.Rel(path, filePath)
				if err != nil {
					return err
				}
				uploadPath := filepath.ToSlash(filepath.Join(u.path, relativePath))
				size, _, err := s3b.upload(ctx, u.bucket, uploadPath, filePath)
				if err != nil {
					return fmt.Errorf("genericS3: putting nested object %s: %w", uploadPath, err)
				}
				obj.Size += size
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		return obj, nil
	}
	size, etag, err := s3b.upload(ctx, u.bucket, u.path, path)
	if err != nil {
		return nil, fmt.Errorf("genericS3: putting object %s: %w", url, err)
	}
	return &Object{URL: url, Name: u.path, ETag: etag, Size: size, LastModified: fileInfo.ModTime()}, nil
}

// upload copies a local file to the bucket at the given key, returning the
// size (reported from the local file, without inspecting remote state) and
// the ETag from the upload response.
func (s3b *GenericS3) upload(ctx context.Context, bucket, key, filePath string) (int64, string, error) {
	logger := logger.NewLogger("GenericS3", logger.DefaultConfig())

	f, err := os.Open(filePath)
	if err != nil {
		return 0, "", err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return 0, "", err
	}

	in := &s3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
		Body:   f,
	}
	if s3b.kmskeyId != "" {
		logger.Debug("genericS3: using KMS encryption for upload", "kmsKeyId", s3b.kmskeyId)
		in.ServerSideEncryption = types.ServerSideEncryptionAwsKms
		in.SSEKMSKeyId = aws.String(s3b.kmskeyId)
	}
	// Uploader switches to multipart above the 5 GiB single-request
	// PutObject limit, matching the replaced minio client's behavior.
	out, err := manager.NewUploader(s3b.client(ctx, bucket)).Upload(ctx, in)
	if err != nil {
		return 0, "", err
	}
	return info.Size(), unquoteETag(aws.ToString(out.ETag)), nil
}

// Join joins the given URL with the given subpath.
func (s3b *GenericS3) Join(url, path string) (string, error) {
	return strings.TrimSuffix(url, "/") + "/" + path, nil
}

// UnsupportedOperations describes which operations (Get, Put, etc) are not
// supported for the given URL.
func (s3b *GenericS3) UnsupportedOperations(url string) UnsupportedOperations {
	unsupported := s3b.apiUnsupportedOperations(url)
	if raw, matched := s3b.mountedURL(url); matched {
		err := validateInputURL(raw)
		unsupported.Get, unsupported.Stat, unsupported.List = err, err, err
	}
	if raw, matched := s3b.mountedWriteURL(url); matched {
		err := validateInputURL(raw)
		unsupported.Put, unsupported.Join = err, err
	}
	return unsupported
}

func (s3b *GenericS3) apiUnsupportedOperations(url string) UnsupportedOperations {
	u, err := s3b.parse(url)
	if err != nil {
		return AllUnsupported(err)
	}

	_, err = s3b.client(context.Background(), u.bucket).HeadBucket(context.Background(), &s3.HeadBucketInput{
		Bucket: aws.String(u.bucket),
	})
	if err != nil {
		err = fmt.Errorf("genericS3: failed to find bucket %q: %v", u.bucket, err)
		return AllUnsupported(err)
	}
	return AllSupported()
}

func (s3b *GenericS3) parse(rawurl string) (*urlparts, error) {
	if !strings.HasPrefix(rawurl, s3Protocol) {
		return nil, &ErrUnsupportedProtocol{"genericS3"}
	}

	path := strings.TrimPrefix(rawurl, s3Protocol)
	path = strings.TrimPrefix(path, s3b.endpoint)
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

func (s3b *GenericS3) mountedURL(raw string) (string, bool) {
	if s3b.mountedRead == nil {
		return "", false
	}
	u, err := s3b.parse(raw)
	if err != nil {
		return "", false
	}
	canonical := "s3://" + u.bucket + "/" + u.path
	return canonical, inputPrefix(strings.TrimSuffix(canonical, "/"), s3b.mountedRead.prefix)
}
