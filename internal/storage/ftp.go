package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/textproto"
	urllib "net/url"
	"os"
	pathlib "path"
	"strconv"
	"strings"

	"github.com/calypr/funnel/config"
	"github.com/jlaffaye/ftp"
)

// FTP provides read access to public URLs.
type FTP struct {
	conf *config.FTPStorage
}

// NewFTP creates a new FTP instance.
func NewFTP(conf *config.FTPStorage) (*FTP, error) {
	if configuredFTPCredentials(conf) {
		if _, err := ftpCredentialEndpoint(conf.GetEndpoint()); err != nil {
			return nil, err
		}
	}
	return &FTP{conf: conf}, nil
}

// Stat returns information about the object at the given storage URL.
func (b *FTP) Stat(ctx context.Context, url string) (*Object, error) {
	client, err := connect(url, b.conf)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	return client.Stat(ctx, url)
}

// Get copies a file from a given URL to the host path.
func (b *FTP) Get(ctx context.Context, url, path string) (*Object, error) {
	client, err := connect(url, b.conf)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	return client.Get(ctx, url, path)
}

// Put is not supported by FTP storage.
func (b *FTP) Put(ctx context.Context, url string, hostPath string) (*Object, error) {
	client, err := connect(url, b.conf)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	return client.Put(ctx, url, hostPath)
}

// Join joins the given URL with the given subpath.
func (b *FTP) Join(url, path string) (string, error) {
	return ftpJoin(url, path)
}

// List is not supported by FTP storage.
func (b *FTP) List(ctx context.Context, url string) ([]*Object, error) {
	client, err := connect(url, b.conf)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	return client.List(ctx, url)
}

// UnsupportedOperations describes which operations (Get, Put, etc) are not
// supported for the given URL.
func (b *FTP) UnsupportedOperations(url string) UnsupportedOperations {
	if err := b.supportsPrefix(url); err != nil {
		return AllUnsupported(err)
	}
	return AllSupported()
}

func (b *FTP) supportsPrefix(url string) error {
	if !strings.HasPrefix(url, "ftp://") {
		return &ErrUnsupportedProtocol{"ftpStorage"}
	}
	return nil
}

// ftpclient exists implements the storage API and reuses an FTP client
// for recursive calls.
type ftpclient struct {
	client *ftp.ServerConn
}

// Only the public anonymous defaults may be sent to arbitrary task URLs.
func configuredFTPCredentials(conf *config.FTPStorage) bool {
	user, pass := conf.GetUser(), conf.GetPassword()
	return !((user == "" || strings.EqualFold(user, "anonymous")) && (pass == "" || pass == "anonymous"))
}

func ftpOrigin(u *urllib.URL) (string, error) {
	if u.Scheme != "ftp" || u.Hostname() == "" {
		return "", fmt.Errorf("ftpStorage: invalid FTP origin")
	}
	port := 21
	if u.Port() != "" {
		number, err := strconv.Atoi(u.Port())
		if err != nil || number < 1 || number > 65535 {
			return "", fmt.Errorf("ftpStorage: invalid FTP port")
		}
		port = number
	}
	return net.JoinHostPort(strings.ToLower(u.Hostname()), strconv.Itoa(port)), nil
}

func ftpCredentialEndpoint(endpoint string) (string, error) {
	u, err := urllib.Parse(endpoint)
	if err != nil || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", fmt.Errorf("ftpStorage: credential endpoint must be an FTP origin without credentials, path, query, or fragment")
	}
	origin, err := ftpOrigin(u)
	if err != nil {
		return "", fmt.Errorf("ftpStorage: credential endpoint must specify ftp://host[:port]")
	}
	return origin, nil
}

func connect(url string, conf *config.FTPStorage) (*ftpclient, error) {
	u, err := urllib.Parse(url)
	if err != nil {
		return nil, fmt.Errorf("ftpStorage: parsing URL: %s", err)
	}
	if u.Scheme != "ftp" {
		return nil, &ErrUnsupportedProtocol{"ftpStorage"}
	}

	host, err := ftpOrigin(u)
	if err != nil {
		return nil, err
	}
	user, pass := conf.GetUser(), conf.GetPassword()
	if u.User != nil {
		user = u.User.Username()
		pass, _ = u.User.Password()
	} else if configuredFTPCredentials(conf) {
		endpoint, err := ftpCredentialEndpoint(conf.GetEndpoint())
		if err != nil {
			return nil, err
		}
		if host != endpoint {
			return nil, fmt.Errorf("ftpStorage: credential endpoint does not match requested origin")
		}
	}
	client, err := ftp.Dial(host)
	if err != nil {
		return nil, fmt.Errorf("ftpStorage: connecting to server: %v", err)
	}

	err = client.Login(user, pass)
	if err != nil {
		_ = client.Quit() //nolint:errcheck // Preserve the authentication failure after closing the connection.
		return nil, fmt.Errorf("ftpStorage: logging in: %v", err)
	}
	return &ftpclient{client}, nil
}

func (b *ftpclient) Close() {
	_ = b.client.Logout() //nolint:errcheck // Logout is best-effort connection cleanup after the operation result is known.
	_ = b.client.Quit()   //nolint:errcheck // Quit is best-effort connection cleanup after the operation result is known.
}

// Stat returns information about the object at the given storage URL.
func (b *ftpclient) Stat(ctx context.Context, url string) (*Object, error) {
	u, err := urllib.Parse(url)
	if err != nil {
		return nil, fmt.Errorf("ftpStorage: parsing URL: %s", err)
	}

	resp, err := b.client.List(u.Path)
	if err != nil {
		return nil, fmt.Errorf("ftpStorage: listing path: %q %v", u.Path, err)
	}

	if len(resp) != 1 {
		return nil, fmt.Errorf("ftpStorage: object not found: %s", url)
	}

	r := resp[0]

	// TODO there is a "link" file type. can we support that?
	if r.Type != ftp.EntryTypeFile {
		return nil, fmt.Errorf("ftpStorage: stat on non-regular file type: %s", url)
	}

	return &Object{
		URL:          url,
		Name:         strings.TrimPrefix(u.Path, "/"),
		LastModified: r.Time,
		Size:         int64(r.Size),
	}, nil
}

// Get copies a file from a given URL to the host path.
func (b *ftpclient) Get(ctx context.Context, url, path string) (obj *Object, retErr error) {
	obj, err := b.Stat(ctx, url)
	if err != nil {
		return nil, err
	}

	src, err := b.client.Retr(obj.Name)
	if err != nil {
		return nil, fmt.Errorf("ftpStorage: executing RETR request: %s", err)
	}
	defer func() {
		err := src.Close()
		if err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("ftpStorage: closing download stream: %w", err))
		}
	}()

	dest, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("ftpStorage: creating host file: %s", err)
	}

	_, copyErr := io.Copy(dest, contextReader(ctx, src))
	closeErr := dest.Close()

	if copyErr != nil {
		return nil, fmt.Errorf("ftpStorage: copying file: %s", copyErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("ftpStorage: closing file: %s", closeErr)
	}

	return obj, err
}

func (b *ftpclient) Put(ctx context.Context, url string, hostPath string) (*Object, error) {

	u, err := urllib.Parse(url)
	if err != nil {
		return nil, fmt.Errorf("ftpStorage: parsing URL: %s", err)
	}

	reader, err := os.Open(hostPath)
	if err != nil {
		return nil, fmt.Errorf("ftpStorage: opening host file for %q: %v", url, err)
	}
	defer reader.Close() //nolint:errcheck // The upload source is read-only, so close cannot change uploaded data.

	dirpath, name := pathlib.Split(u.Path)
	if dirpath != "" {
		for _, dir := range strings.Split(strings.Trim(dirpath, "/"), "/") {
			err := b.client.ChangeDir(dir)
			if isUnavailable(err) {
				// Directory doesn't exist. Create it.
				err = b.client.MakeDir(dir)
				// It's possible that the directory was created by a concurrent process.
				// In that case, allow the ChangeDir call below to retry.
				if isUnavailable(err) {
					err = nil
				}
				if err == nil {
					err = b.client.ChangeDir(dir)
				}
			}

			if err != nil {
				return nil, fmt.Errorf("ftpStorage: changing directory to %q: %v", dir, err)
			}
		}
	}

	err = b.client.Stor(name, reader)
	if err != nil {
		return nil, fmt.Errorf("ftpStorage: uploading file for %q: %v", url, err)
	}

	return b.Stat(ctx, url)
}

func isUnavailable(err error) bool {
	e, ok := err.(*textproto.Error)
	return ok && e.Code == ftp.StatusFileUnavailable
}

func (b *ftpclient) List(ctx context.Context, url string) ([]*Object, error) {
	u, err := urllib.Parse(url)
	if err != nil {
		return nil, fmt.Errorf("ftpStorage: parsing URL: %s", err)
	}

	resp, err := b.client.List(u.Path)
	if err != nil {
		return nil, fmt.Errorf("ftpStorage: listing path: %q %v", u.Path, err)
	}

	// Special case where the user called List on a regular file.
	if len(resp) == 1 && resp[0].Type == ftp.EntryTypeFile {
		r := resp[0]
		return []*Object{
			{
				URL:          url,
				Name:         strings.TrimPrefix(u.Path, "/"),
				LastModified: r.Time,
				Size:         int64(r.Size),
			},
		}, nil
	}

	// List the objects, recursively.
	var objects []*Object
	for _, r := range resp {
		switch r.Type {

		case ftp.EntryTypeFolder:
			if r.Name == "." || r.Name == ".." {
				continue
			}

			joined, err := ftpJoin(url, r.Name)
			if err != nil {
				return nil, err
			}

			sub, err := b.List(ctx, joined)
			if err != nil {
				return nil, err
			}
			objects = append(objects, sub...)

		case ftp.EntryTypeLink:
			// Link type is currently not supported. Skip it.
			// TODO there is a "EntryTypeLink" type. can we support that?

		case ftp.EntryTypeFile:
			joined, err := ftpJoin(url, r.Name)
			if err != nil {
				return nil, err
			}

			obj := &Object{
				URL:          joined,
				Name:         strings.TrimPrefix(pathlib.Join(u.Path, r.Name), "/"),
				LastModified: r.Time,
				Size:         int64(r.Size),
			}
			objects = append(objects, obj)
		}
	}
	return objects, nil
}

// ftpJoin joins the given URL with the given subpath.
func ftpJoin(url, path string) (string, error) {
	joined, err := joinURLPath(url, path)
	if err != nil {
		return "", fmt.Errorf("ftpStorage: parsing URL: %s", err)
	}
	return joined, nil
}
