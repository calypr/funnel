package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	urllib "net/url"
	"os"
	"strings"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/util/fsutil"
)

// HTTP provides access to URLs that need no credentials, such as public or
// presigned URLs: downloads with GET and uploads with PUT.
type HTTP struct {
	client *http.Client
}

// NewHTTP creates a new HTTP instance.
func NewHTTP(conf *config.HTTPStorage) (*HTTP, error) {
	client := &http.Client{
		Timeout:       conf.Timeout.GetDuration().AsDuration(),
		CheckRedirect: checkRedirect,
	}
	return &HTTP{client}, nil
}

// checkRedirect keeps net/http's limit of 10 redirects and stops an upload at
// a redirect that would turn it into a GET. net/http follows a 301, 302 or 303
// for a PUT by sending a GET without the body, so a successful GET would pass
// for a successful upload. A 307 or 308 keeps the method and resends the body,
// so the upload follows it.
func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	if via[0].Method == http.MethodPut && req.Method != http.MethodPut {
		return http.ErrUseLastResponse
	}
	return nil
}

// Stat returns information about the object at the given storage URL.
func (b *HTTP) Stat(ctx context.Context, url string) (*Object, error) {
	u, err := urllib.Parse(url)
	if err != nil {
		return nil, fmt.Errorf("httpStorage: parsing URL: %s", err)
	}

	req, err := http.NewRequestWithContext(ctx, "HEAD", url, nil)
	if err != nil {
		return nil, fmt.Errorf("httpStorage: creating HEAD request: %s", err)
	}

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

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("httpStorage: creating GET request: %s", err)
	}

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

// Put uploads a file from the host path to the given URL with a PUT request.
// This is how presigned upload URLs (S3, Google Cloud Storage and others) are
// used, so the worker needs no storage credentials.
func (b *HTTP) Put(ctx context.Context, url string, hostPath string) (*Object, error) {
	u, err := urllib.Parse(url)
	if err != nil {
		return nil, fmt.Errorf("httpStorage: parsing URL: %s", err)
	}

	src, err := os.Open(hostPath)
	if err != nil {
		return nil, fmt.Errorf("httpStorage: opening host file: %s", err)
	}
	defer src.Close()

	info, err := src.Stat()
	if err != nil {
		return nil, fmt.Errorf("httpStorage: getting host file info: %s", err)
	}

	// Object stores reject chunked uploads, so the length is sent up front.
	// net/http treats a zero length with a non-nil body as unknown and would
	// fall back to chunked encoding, hence NoBody for empty files.
	var body io.Reader = http.NoBody
	if info.Size() > 0 {
		body = fsutil.Reader(ctx, src)
	}
	req, err := http.NewRequestWithContext(ctx, "PUT", url, body)
	if err != nil {
		return nil, fmt.Errorf("httpStorage: creating PUT request: %s", err)
	}
	req.ContentLength = info.Size()
	// Lets net/http resend the file when a 307 or 308 redirect moves the
	// upload elsewhere, such as to a presigned URL created on demand. With
	// Expect: 100-continue, net/http waits up to one second for the server to
	// answer before it sends the file, so a server that redirects within that
	// second doesn't receive it.
	if info.Size() > 0 {
		req.Header.Set("Expect", "100-continue")
		req.GetBody = func() (io.ReadCloser, error) {
			f, err := os.Open(hostPath)
			if err != nil {
				return nil, err
			}
			return struct {
				io.Reader
				io.Closer
			}{fsutil.Reader(ctx, f), f}, nil
		}
	}

	resp, err := b.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("httpStorage: executing PUT request: %s", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		// Object stores explain a rejection, such as an expired or mismatched
		// signature, in the response body.
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("httpStorage: PUT request returned status code: %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}

	return &Object{
		URL: url,
		// The path alone: the query of a presigned URL carries its signature.
		Name: u.Path,
		Size: info.Size(),
		ETag: resp.Header.Get("ETag"),
	}, nil
}

// Join joins the given URL with the given subpath. Each segment of the subpath
// is escaped, so a file name containing a character such as #, ? or % stays
// part of the path, and the URL's query, which may carry a token, is kept.
func (b *HTTP) Join(url, path string) (string, error) {
	joined, err := joinURLPath(url, path)
	if err != nil {
		return "", fmt.Errorf("httpStorage: parsing URL: %s", err)
	}
	return joined, nil
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
	}

	return ops
}

func (b *HTTP) supportsPrefix(url string) error {
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return &ErrUnsupportedProtocol{"httpStorage"}
	}
	return nil
}
