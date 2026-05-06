package storage

import (
	"context"
	"fmt"
	"io"
	"net/http"
	urllib "net/url"
	"os"
	"strings"

	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/util/fsutil"
)

// HTTP provides read access to public URLs.
type HTTP struct {
	client *http.Client
}

// NewHTTP creates a new HTTP instance.
func NewHTTP(conf *config.HTTPStorage) (*HTTP, error) {
	client := &http.Client{
		Timeout: conf.Timeout.GetDuration().AsDuration(),
	}
	return &HTTP{client}, nil
}

// Stat returns information about the object at the given storage URL.
func (b *HTTP) Stat(ctx context.Context, url string) (*Object, error) {
	u, err := urllib.Parse(url)
	if err != nil {
		return nil, fmt.Errorf("httpStorage: parsing URL: %s", err)
	}

	req, err := http.NewRequest("HEAD", url, nil)
	if err != nil {
		return nil, fmt.Errorf("httpStorage: creating HEAD request: %s", err)
	}
	req.WithContext(ctx)

	resp, err := b.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("httpStorage: executing HEAD request: %s", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("httpStorage: HEAD request returned status code: %d", resp.StatusCode)
	}

	modtime, _ := http.ParseTime(resp.Header.Get("Last-Modified"))

	return &Object{
		URL:          url,
		Name:         u.RequestURI(),
		Size:         resp.ContentLength,
		LastModified: modtime,
		ETag:         resp.Header.Get("ETag"),
	}, nil
}

// Get copies a file from a given URL to the host path.
func (b *HTTP) Get(ctx context.Context, url, path string) (*Object, error) {
	u, err := urllib.Parse(url)
	if err != nil {
		return nil, fmt.Errorf("httpStorage: parsing URL: %s", err)
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("httpStorage: creating GET request: %s", err)
	}
	req.WithContext(ctx)

	resp, err := b.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("httpStorage: executing GET request: %s", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("httpStorage: GET request returned status code: %d", resp.StatusCode)
	}

	dest, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("httpStorage: creating host file: %s", err)
	}

	_, copyErr := io.Copy(dest, fsutil.Reader(ctx, resp.Body))
	closeErr := dest.Close()

	if copyErr != nil {
		return nil, fmt.Errorf("httpStorage: copying file: %s", copyErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("httpStorage: closing file: %s", closeErr)
	}

	modtime, _ := http.ParseTime(resp.Header.Get("Last-Modified"))

	return &Object{
		URL:          url,
		Name:         u.RequestURI(),
		Size:         resp.ContentLength,
		LastModified: modtime,
		ETag:         resp.Header.Get("ETag"),
	}, nil
}

// Put is not supported by HTTP storage.
func (b *HTTP) Put(ctx context.Context, url string, hostPath string) (*Object, error) {
	return nil, fmt.Errorf("httpStorage: Put operation is not supported")
}

// Join joins the given URL with the given subpath.
func (b *HTTP) Join(url, path string) (string, error) {
	return strings.TrimSuffix(url, "/") + "/" + path, nil
}

// List is not supported by HTTP storage.
func (b *HTTP) List(ctx context.Context, url string) ([]*Object, error) {
	return nil, fmt.Errorf("httpStorage: List operation is not supported")
}

// UnsupportedOperations describes which operations (Get, Put, etc) are not
// supported for the given URL.
func (b *HTTP) UnsupportedOperations(url string) UnsupportedOperations {
	if err := b.supportsPrefix(url); err != nil {
		return AllUnsupported(err)
	}

	ops := UnsupportedOperations{
		List: fmt.Errorf("httpStorage: List operation is not supported"),
		Put:  fmt.Errorf("httpStorage: Put operation is not supported"),
	}

	return ops
}

func (b *HTTP) supportsPrefix(url string) error {
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return &ErrUnsupportedProtocol{"httpStorage"}
	}
	return nil
}
