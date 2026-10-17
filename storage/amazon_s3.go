package storage

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/calypr/funnel/config"
	util "github.com/calypr/funnel/util/aws"
	s3util "github.com/calypr/funnel/util/s3"
)

// s3Protocol defines the s3 URL protocol
const s3Protocol = "s3://"

const defaultS3RetryMaxAttempts = 4

// AmazonS3 provides access to an S3 object store.
type AmazonS3 struct {
	cfg            aws.Config
	endpoint       string
	forcePathStyle bool
	// region is the pre-configured AWS/S3-compatible region.  When set together
	// with endpoint (non-AWS deployments), GetBucketRegion is skipped and this
	// value is used directly.
	region               string
	customerAlgorithm    *string
	customerKey          *string
	customerKeyMD5       *string
	kmsKeyID             *string
	serverSideEncryption types.ServerSideEncryption
}

// NewAmazonS3 creates an AmazonS3 session instance
func NewAmazonS3(conf *config.AmazonS3Storage) (*AmazonS3, error) {
	sess, err := util.NewAWSConfig(context.Background(), conf.AWSConfig)
	if err != nil {
		return nil, fmt.Errorf("error creating amazon s3 backend: %v", err)
	}
	if conf.AWSConfig.MaxRetries <= 0 && sess.RetryMaxAttempts == 0 {
		sess.RetryMaxAttempts = defaultS3RetryMaxAttempts
	}

	var endpoint string
	if conf.AWSConfig.Endpoint != "" {
		endpoint = s3util.ParseEndpoint(conf.AWSConfig.Endpoint)
	}

	// handle SSE config
	var customerAlgorithm *string
	var customerKey *string
	var customerKeyMD5 *string
	var kmsKeyID *string
	var serverSideEncryption types.ServerSideEncryption

	if conf.SSE != nil && conf.SSE.CustomerKeyFile != "" && conf.SSE.KMSKey != "" {
		return nil, fmt.Errorf("invalid SSE config: can't provide both Customer and KMS keys")
	}

	if conf.SSE != nil && conf.SSE.CustomerKeyFile != "" {
		key, err := os.ReadFile(conf.SSE.CustomerKeyFile)
		if err != nil {
			return nil, fmt.Errorf("error reading sse-c file: %v", err)
		}

		customerAlgorithm = aws.String("AES256")
		customerKey = aws.String(base64.StdEncoding.EncodeToString(key))
		b := md5.Sum(key)
		customerKeyMD5 = aws.String(base64.StdEncoding.EncodeToString(b[:]))
	}

	if conf.SSE != nil && conf.SSE.KMSKey != "" {
		serverSideEncryption = types.ServerSideEncryptionAwsKms
		kmsKeyID = aws.String(conf.SSE.KMSKey)
	}

	return &AmazonS3{
		sess,
		endpoint,
		conf.AWSConfig.Endpoint != "" && !regexp.MustCompile(`^s3.*\.amazonaws\.com/`).MatchString(conf.AWSConfig.Endpoint),
		conf.AWSConfig.Region,
		customerAlgorithm,
		customerKey,
		customerKeyMD5,
		kmsKeyID,
		serverSideEncryption,
	}, nil
}

// Stat returns information about the object at the given storage URL.
func (s3b *AmazonS3) Stat(ctx context.Context, url string) (*Object, error) {
	u, region, err := s3b.parse(url)
	if err != nil {
		return nil, err
	}

	client := s3b.client(region)

	statInput := &s3.HeadObjectInput{
		Bucket:               aws.String(u.bucket),
		Key:                  aws.String(u.path),
		SSECustomerAlgorithm: s3b.customerAlgorithm,
		SSECustomerKey:       s3b.customerKey,
		SSECustomerKeyMD5:    s3b.customerKeyMD5,
	}
	var res *s3.HeadObjectOutput
	res, err = client.HeadObject(ctx, statInput)
	if err != nil {
		// the file may not be encrypted => retry without sse-c keys
		if s3b.customerKey != nil {
			statInput = &s3.HeadObjectInput{
				Bucket: aws.String(u.bucket),
				Key:    aws.String(u.path),
			}
			var rerr error
			res, rerr = client.HeadObject(ctx, statInput)
			if rerr != nil {
				return nil, fmt.Errorf("amazonS3: calling stat on URL %s: %v", url, err)
			}
		} else {
			return nil, fmt.Errorf("amazonS3: calling stat on URL %s: %v", url, err)
		}
	}
	return &Object{
		URL:          url,
		Name:         u.path,
		LastModified: *res.LastModified,
		ETag:         *res.ETag,
		Size:         *res.ContentLength,
	}, nil
}

// List returns a list of objects at the given url.
func (s3b *AmazonS3) List(ctx context.Context, url string) ([]*Object, error) {
	u, region, err := s3b.parse(url)
	if err != nil {
		return nil, err
	}

	var objects []*Object

	client := s3b.client(region)
	pages := s3.NewListObjectsV2Paginator(client, &s3.ListObjectsV2Input{
		Bucket: aws.String(u.bucket),
		Prefix: aws.String(u.path),
	})
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, obj := range page.Contents {
			if strings.HasSuffix(*obj.Key, "/") {
				continue
			}
			objects = append(objects, &Object{
				URL:          fmt.Sprintf("s3://%s/%s", u.bucket, *obj.Key),
				Name:         *obj.Key,
				ETag:         *obj.ETag,
				LastModified: *obj.LastModified,
				Size:         *obj.Size,
			})
		}
	}

	return objects, nil
}

// Get copies an object from S3 to the host path.
func (s3b *AmazonS3) Get(ctx context.Context, url, path string) (*Object, error) {
	obj, err := s3b.Stat(ctx, url)
	if err != nil {
		return nil, err
	}

	u, region, err := s3b.parse(url)
	if err != nil {
		return nil, err
	}

	client := s3b.client(region)
	downloader := manager.NewDownloader(client, func(d *manager.Downloader) {
		d.PartSize = 5 * 1024 * 1024
		d.Concurrency = 1
	})

	// Create a file to write the S3 Object contents to.
	hf, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("amazonS3: creating file: %v", err)
	}

	getInput := &s3.GetObjectInput{
		Bucket:               aws.String(u.bucket),
		Key:                  aws.String(u.path),
		SSECustomerAlgorithm: s3b.customerAlgorithm,
		SSECustomerKey:       s3b.customerKey,
		SSECustomerKeyMD5:    s3b.customerKeyMD5,
	}
	_, copyErr := downloader.Download(ctx, hf, getInput)
	if copyErr != nil {
		if s3b.customerKey != nil {
			// the file may not be encrypted => retry without sse-c keys
			getInput = &s3.GetObjectInput{
				Bucket: aws.String(u.bucket),
				Key:    aws.String(u.path),
			}
			_, copyErr = downloader.Download(ctx, hf, getInput)
		}
	}
	closeErr := hf.Close()
	if copyErr != nil {
		return nil, fmt.Errorf("amazonS3: copying file: %v", copyErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("amazonS3: closing file: %v", closeErr)
	}
	return obj, nil
}

// Put copies an object (file) from the host path to S3.
func (s3b *AmazonS3) Put(ctx context.Context, url, path string) (*Object, error) {
	u, region, err := s3b.parse(url)
	if err != nil {
		return nil, err
	}

	client := s3b.client(region)
	uploader := manager.NewUploader(client, func(u *manager.Uploader) {
		u.PartSize = 5 * 1024 * 1024
	})

	var hf *os.File
	// If path contains a wildcard, then handle globbing
	if strings.Contains(path, "*") {
		globs, err := filepath.Glob(path)
		if err != nil {
			return nil, fmt.Errorf("amazonS3: failed to resolve path %v: %v", path, err)
		}
		for _, glob := range globs {
			hf, err = os.Open(glob)
			if err != nil {
				return nil, fmt.Errorf("amazonS3: opening file %v: %v", glob, err)
			}
		}
	} else {
		hf, err = os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("amazonS3: opening file %v: %v", path, err)
		}
	}
	defer hf.Close()

	uploadInput := &s3.PutObjectInput{
		Bucket:               aws.String(u.bucket),
		Key:                  aws.String(u.path),
		Body:                 hf,
		SSECustomerAlgorithm: s3b.customerAlgorithm,
		SSECustomerKey:       s3b.customerKey,
		SSECustomerKeyMD5:    s3b.customerKeyMD5,
		ServerSideEncryption: s3b.serverSideEncryption,
		SSEKMSKeyId:          s3b.kmsKeyID,
	}

	_, copyErr := uploader.Upload(ctx, uploadInput)
	if copyErr != nil {
		return nil, fmt.Errorf("amazonS3: copying file: %v", copyErr)
	}
	return s3b.Stat(ctx, url)
}

// Join joins the given URL with the given subpath.
func (s3b *AmazonS3) Join(url, path string) (string, error) {
	return strings.TrimSuffix(url, "/") + "/" + path, nil
}

// UnsupportedOperations describes which operations (Get, Put, etc) are not
// supported for the given URL.
func (s3b *AmazonS3) UnsupportedOperations(url string) UnsupportedOperations {
	_, _, err := s3b.parse(url)
	if err != nil {
		return AllUnsupported(err)
	}
	return AllSupported()
}

func (s3b *AmazonS3) parse(rawurl string) (*urlparts, string, error) {
	if !strings.HasPrefix(rawurl, s3Protocol) {
		return nil, "", &ErrUnsupportedProtocol{"amazonS3"}
	}

	path := strings.TrimPrefix(rawurl, s3Protocol)
	if s3b.endpoint != "" {
		path = strings.TrimPrefix(path, s3b.endpoint)
	} else {
		re := regexp.MustCompile(`^s3.*\.amazonaws\.com/`)
		path = re.ReplaceAllString(path, "")
	}
	if path == "" {
		return nil, "", &ErrInvalidURL{"amazonS3"}
	}

	split := strings.SplitN(path, "/", 2)
	url := &urlparts{}
	if len(split) > 0 {
		url.bucket = split[0]
	}
	if len(split) == 2 {
		url.path = split[1]
	}

	// When a custom endpoint and a region are both configured (non-AWS S3-compatible
	// services such as OVH Object Storage), skip the GetBucketRegion call: it
	// performs an AWS-specific HeadBucket request that non-AWS providers reject
	// with a 400 Bad Request.  Use the pre-configured region directly instead.
	var region string
	if s3b.endpoint != "" && s3b.region != "" {
		region = s3b.region
	} else {
		var err error
		region, err = s3b.bucketRegion(context.Background(), url.bucket)
		if err != nil {
			return nil, "", fmt.Errorf("amazonS3: failed to determine region for bucket %q: %v", url.bucket, err)
		}
	}
	return url, region, nil
}

func (s3b *AmazonS3) client(region string) *s3.Client {
	return s3.NewFromConfig(s3b.cfg, func(options *s3.Options) {
		options.Region = region
		options.UsePathStyle = s3b.forcePathStyle
		options.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
		options.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
	})
}

func (s3b *AmazonS3) bucketRegion(ctx context.Context, bucket string) (string, error) {
	result, err := s3b.client("us-east-1").HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(bucket)}, func(options *s3.Options) {
		options.Credentials = nil
	})
	if err == nil {
		if region := aws.ToString(result.BucketRegion); region != "" {
			return region, nil
		}
		return "us-east-1", nil
	}
	var response *smithyhttp.ResponseError
	if errors.As(err, &response) {
		if region := response.HTTPResponse().Header.Get("X-Amz-Bucket-Region"); region != "" {
			return region, nil
		}
	}
	return "", err
}
