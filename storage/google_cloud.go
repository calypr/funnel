package storage

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/util/fsutil"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
	"google.golang.org/api/storage/v1"
)

// The gs url protocol
const gsProtocol = "gs://"

// GoogleCloud provides access to an GS object store.
type GoogleCloud struct {
	svc *storage.Service
}

// NewGoogleCloud creates an GoogleCloud client instance, give an endpoint URL
// and a set of authentication credentials.
func NewGoogleCloud(conf *config.GoogleCloudStorage) (*GoogleCloud, error) {
	ctx := context.Background()
	client := &http.Client{}

	if conf.CredentialsFile != "" {
		// Pull the client configuration (e.g. auth) from a given account file.
		// This is likely downloaded from Google Cloud manually via IAM & Admin > Service accounts.
		bytes, rerr := os.ReadFile(conf.CredentialsFile)
		if rerr != nil {
			return nil, rerr
		}

		config, tserr := google.JWTConfigFromJSON(bytes, storage.CloudPlatformScope)
		if tserr != nil {
			return nil, tserr
		}
		client = config.Client(ctx)
	} else {
		// Pull the information (auth and other config) from the environment,
		// which is useful when this code is running in a Google Compute instance.
		defClient, err := google.DefaultClient(ctx, storage.CloudPlatformScope)
		if err == nil {
			client = defClient
		}
	}

	svc, cerr := storage.NewService(ctx, option.WithHTTPClient(client))
	if cerr != nil {
		return nil, cerr
	}

	return &GoogleCloud{svc}, nil
}

// Stat returns information about the object at the given storage URL.
func (gs *GoogleCloud) Stat(ctx context.Context, url string) (*Object, error) {
	u, err := gs.parse(url)
	if err != nil {
		return nil, err
	}

	obj, err := gs.svc.Objects.Get(u.bucket, u.path).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("googleStorage: calling stat on object %s: %v", url, err)
	}

	modtime, _ := time.Parse(time.RFC3339, obj.Updated)
	return &Object{
		URL:          url,
		Name:         obj.Name,
		ETag:         obj.Etag,
		Size:         int64(obj.Size),
		LastModified: modtime,
	}, nil
}

// List lists the objects at the given url.
func (gs *GoogleCloud) List(ctx context.Context, url string) ([]*Object, error) {
	u, err := gs.parse(url)
	if err != nil {
		return nil, err
	}

	var objects []*Object

	err = gs.svc.Objects.List(u.bucket).Prefix(u.path).Pages(ctx,
		func(objs *storage.Objects) error {

			for _, obj := range objs.Items {
				if strings.HasSuffix(obj.Name, "/") {
					continue
				}

				modtime, _ := time.Parse(time.RFC3339, obj.Updated)

				objects = append(objects, &Object{
					URL:          gsProtocol + obj.Bucket + "/" + obj.Name,
					Name:         obj.Name,
					ETag:         obj.Etag,
					Size:         int64(obj.Size),
					LastModified: modtime,
				})
			}
			return nil
		})

	if err != nil {
		return nil, err
	}
	return objects, nil
}

// Get copies an object from GS to the host path.
func (gs *GoogleCloud) Get(ctx context.Context, url, path string) (*Object, error) {
	obj, err := gs.Stat(ctx, url)
	if err != nil {
		return nil, err
	}

	u, err := gs.parse(url)
	if err != nil {
		return nil, err
	}

	resp, err := gs.svc.Objects.Get(u.bucket, u.path).Context(ctx).Download()
	if err != nil {
		return nil, fmt.Errorf("googleStorage: getting object %s: %v", url, err)
	}

	dest, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("googleStorage: creating file %s: %v", path, err)
	}

	_, copyErr := io.Copy(dest, fsutil.Reader(ctx, resp.Body))
	closeErr := dest.Close()

	if copyErr != nil {
		return nil, fmt.Errorf("googleStorage: copying file: %v", copyErr)
	}

	if closeErr != nil {
		return nil, fmt.Errorf("googleStorage: closing file: %v", closeErr)
	}

	return obj, nil
}

// Put copies an object (file) from the host path to GS.
func (gs *GoogleCloud) Put(ctx context.Context, url, path string) (*Object, error) {
	u, err := gs.parse(url)
	if err != nil {
		return nil, err
	}

	reader, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("googleStorage: opening file: %v", err)
	}
	defer reader.Close()

	obj := &storage.Object{
		Name: u.path,
	}

	_, err = gs.svc.Objects.Insert(u.bucket, obj).Media(fsutil.Reader(ctx, reader)).Do()
	if err != nil {
		return nil, fmt.Errorf("googleStorage: uploading object %s: %v", url, err)
	}
	return gs.Stat(ctx, url)
}

// Join joins the given URL with the given subpath.
func (gs *GoogleCloud) Join(url, path string) (string, error) {
	return strings.TrimSuffix(url, "/") + "/" + path, nil
}

// UnsupportedOperations describes which operations (Get, Put, etc) are not
// supported for the given URL.
func (gs *GoogleCloud) UnsupportedOperations(url string) UnsupportedOperations {
	u, err := gs.parse(url)
	if err != nil {
		return AllUnsupported(err)
	}
	_, err = gs.svc.Buckets.Get(u.bucket).Do()
	if err != nil {
		err = fmt.Errorf("googleStorage: failed to find bucket: %s. error: %v", u.bucket, err)
		return AllUnsupported(err)
	}
	return AllSupported()
}

func (gs *GoogleCloud) parse(rawurl string) (*urlparts, error) {
	if !strings.HasPrefix(rawurl, gsProtocol) {
		return nil, &ErrUnsupportedProtocol{"googleStorage"}
	}

	path := strings.TrimPrefix(rawurl, gsProtocol)
	if path == "" {
		return nil, &ErrInvalidURL{"googleStorage"}
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
