package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/minio/minio-go/v7/pkg/encrypt"
	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/logger"
	s3util "github.com/ohsu-comp-bio/funnel/util/s3"
)

// GenericS3 provides access to an S3 object store.
type GenericS3 struct {
	client   *minio.Client
	endpoint string
	kmskeyId string
}

// NewGenericS3 creates a new GenericS3 instance, given an endpoint URL
// and a set of authentication credentials.
func NewGenericS3(conf *config.GenericS3Storage) (*GenericS3, error) {

	var endpoint string
	if conf.Endpoint != "" {
		endpoint = s3util.ParseEndpoint(conf.Endpoint)
	}

	logger := logger.NewLogger("GenericS3", logger.DefaultConfig())
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

	return &GenericS3{client, endpoint + "/", conf.KmsKeyID}, nil
}

// Returns true if a remote S3 object is a directory, false otherwise
func isDir(ctx context.Context, minioClient *minio.Client, bucketName, objectName string) (bool, error) {
	// Check if the objectName ends with '/' - often used to represent 'folders'
	if strings.HasSuffix(objectName, "/") {
		return true, nil
	}

	// List objects with the prefix to see if there are multiple keys with the given prefix
	// Recursively list all objects
	recursive := true
	for object := range minioClient.ListObjects(ctx, bucketName, minio.ListObjectsOptions{Recursive: recursive}) {
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
	logger := logger.NewLogger("GenericS3", logger.DefaultConfig())
	u, err := s3.parse(url)
	if err != nil {
		return nil, err
	}

	opts := minio.GetObjectOptions{}
	logger.Debug("genericS3: s3.client.GetObject", "bucket", u.bucket, "path", u.path)
	obj, err := s3.client.GetObject(ctx, u.bucket, u.path, opts)
	if err != nil {
		logger.Debug("genericS3: getting object from s3.client.GetObject", "url", url, "error", err)
		return nil, fmt.Errorf("genericS3: getting object %s in bucket %s: %s", u.path, u.bucket, err)
	}

	isDir, err := isDir(ctx, s3.client, u.bucket, u.path)
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
	u, err := s3.parse(url)
	if err != nil {
		return nil, err
	}

	// Recursively list all objects.
	var objects []*Object
	recursive := true
	for info := range s3.client.ListObjects(ctx, u.bucket, minio.ListObjectsOptions{Prefix: u.path, Recursive: recursive}) {
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

	return objects, nil
}

// Get copies an object from S3 to the host path.
func (s3 *GenericS3) Get(ctx context.Context, url, path string) (*Object, error) {
	logger := logger.NewLogger("GenericS3", logger.DefaultConfig())
	logger.Debug("genericS3: Get called", "url", url, "path", path)

	obj, err := s3.Stat(ctx, url)
	if err != nil {
		return nil, err
	}

	u, err := s3.parse(url)
	if err != nil {
		return nil, err
	}

	isDir, err := isDir(ctx, s3.client, u.bucket, u.path)
	if err != nil {
		logger.Debug("genericS3: checking directory", "bucket", u.bucket, "path", u.path)
		return nil, fmt.Errorf("genericS3: getting object from isDir %s: %v", url, err)
	}
	if isDir {
		objects, err := s3.List(ctx, url)
		if err != nil {
			return nil, err
		}

		for _, obj := range objects {
			// Recursively download files and subdirectories
			_, err := s3.Get(ctx, obj.URL, filepath.Join(path, obj.Name))
			if err != nil {
				return nil, err
			}
		}
	} else {
		err = download(ctx, s3.client, u.bucket, u.path, path, s3.kmskeyId)
		if err != nil {
			return nil, fmt.Errorf("genericS3: getting object from download %s: %v", url, err)
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
	outFile, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("failed creating file: %w", err)
	}
	defer outFile.Close()

	// Output the downloaded file contents for debugging
	content, err := os.ReadFile(outFile.Name())
	if err != nil {
		logger.Debug("Error reading file", "filePath", outFile.Name(), "error", err)
	}
	logger.Debug("genericS3: file contents", "filePath", outFile.Name(), "content", string(content))

	// Output the downloaded file contents for debugging
	content, err = os.ReadFile(outFile.Name())
	if err != nil {
		logger.Debug("Error reading file", "filePath", outFile.Name(), "error", err)
	}
	logger.Debug("genericS3: file contents", "filePath", outFile.Name(), "content", string(content))

	// Step 3: Copy the contents
	// TODO: Add stack trace (or simply more verbose logging) here...
	// Can we add stack traces for all errors in Funnel?
	logger.Debug("genericS3: io.Copy", "outFile", outFile.Name(), "obj", obj)
	if _, err := io.Copy(outFile, obj); err != nil {
		return fmt.Errorf("failed writing file: %w", err)
	}

	return nil
}

// Put copies an object (file) from the host path to S3.
// Update Put function to be able to upload directories (a la Get() function)
func (s3 *GenericS3) Put(ctx context.Context, url, path string) (*Object, error) {
	u, err := s3.parse(url)
	if err != nil {
		fmt.Println("DEBUG: error parsing URL:", err)
		return nil, err
	}

	opts := minio.PutObjectOptions{}
	if s3.kmskeyId != "" {
		logger.Debug("genericS3: using KMS encryption for upload", "kmsKeyId", s3.kmskeyId)
		SSEKMS, err := encrypt.NewSSEKMS(s3.kmskeyId, ctx)
		if err != nil {
			fmt.Println("DEBUG: error creating SSEKMS:", err)
			return nil, fmt.Errorf("genericS3: Put(): creating SSEKMS: %v", err)
		}
		opts.ServerSideEncryption = SSEKMS
	}

	// Check if the path is a directory
	fmt.Println("DEBUG: path", path)
	fileInfo, err := os.Stat(path)
	if err != nil {
		fmt.Println("DEBUG: error stating path:", err)
		return nil, err
	}
	fmt.Printf("DEBUG: Path %s is a directory\n", path)
	if fileInfo.IsDir() {
		// Walk the directory and upload all files and subdirectories
		err = filepath.Walk(path, func(filePath string, info os.FileInfo, err error) error {
			if err != nil {
				fmt.Println("DEBUG: error walking filepath:", err)
				return err
			}
			if !info.IsDir() {
				// Upload the file
				relativePath, err := filepath.Rel(path, filePath)
				if err != nil {
					return err
				}
				uploadPath := filepath.Join(u.path, relativePath)
				fmt.Println("DEBUG: u.bucket:", u.bucket)
				fmt.Println("DEBUG: uploadPath:", uploadPath)
				fmt.Println("DEBUG: filePath:", filePath)
				_, err = s3.client.FPutObject(ctx, u.bucket, uploadPath, filePath, opts)
				if err != nil {
					fmt.Println("DEBUG: error putting object:", err)
					return fmt.Errorf("genericS3: putting object %s: %v", url, err)
				}
			}
			fmt.Println("DEBUG: finished walking directory...")
			return nil
		})
		if err != nil {
			fmt.Println("DEBUG: error walking directory:", err)
			return nil, err
		}
	} else {
		// Upload the file directly
		fmt.Println("DEBUG: Uploading file directly")
		fmt.Println("DEBUG: u.bucket:", u.bucket)
		fmt.Println("DEBUG: u.path:", u.path)
		fmt.Println("DEBUG: path:", path)
		_, err = s3.client.FPutObject(ctx, u.bucket, u.path, path, opts)
		if err != nil {
			fmt.Println("DEBUG: error putting object directly:", err)
			return nil, fmt.Errorf("genericS3: putting object %s: %v", url, err)
		}
	}

	fmt.Println("DEBUG: url:", url)
	fmt.Println("DEBUG: url:", url)
	obj, err := s3.Stat(ctx, url)
	fmt.Println("DEBUG: obj:", obj)
	fmt.Println("DEBUG: err:", err)

	return obj, err
}

// Join joins the given URL with the given subpath.
func (s3 *GenericS3) Join(url, path string) (string, error) {
	return strings.TrimSuffix(url, "/") + "/" + path, nil
}

// UnsupportedOperations describes which operations (Get, Put, etc) are not
// supported for the given URL.
func (s3 *GenericS3) UnsupportedOperations(url string) UnsupportedOperations {
	u, err := s3.parse(url)
	if err != nil {
		return AllUnsupported(err)
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

func (s3 *GenericS3) parse(rawurl string) (*urlparts, error) {
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
