package storage

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/logger"
)

// operation codes help multiplex storage operations across multiple backends.
type operation int

const (
	getOp operation = iota
	putOp
	listOp
	statOp
	joinOp
)

// Mux provides a client for accessing multiple storage systems,
// i.e. for downloading/uploading task files from S3, GS, local disk, etc.
//
// For a given storage url, the storage backend is usually determined by the url prefix,
// e.g. "s3://my-bucket/file" will access the S3 backend.
type Mux struct {
	Backends []Storage
}

// NewMux returns a new Mux instance with the given additional configuration.
func NewMux(conf *config.Config) (*Mux, error) {
	mux := &Mux{}

	if conf.LocalStorage.Valid() {
		local, err := NewLocal(conf.LocalStorage)
		if err != nil {
			return mux, fmt.Errorf("failed to configure local storage backend: %s", err)
		}
		mux.Backends = append(mux.Backends, local)
	}

	if conf.AmazonS3.Valid() {
		s3, err := NewAmazonS3(conf.AmazonS3)
		if err != nil {
			return mux, fmt.Errorf("failed to configure Amazon S3 storage backend: %s", err)
		}
		mux.Backends = append(mux.Backends, s3)
	}

	if conf.GoogleStorage.Valid() {
		gs, nerr := NewGoogleCloud(conf.GoogleStorage)
		if nerr != nil {
			return mux, fmt.Errorf("failed to configure Google Storage backend: %s", nerr)
		}
		mux.Backends = append(mux.Backends, gs)
	}

	if conf.Swift.Valid() {
		s, err := NewSwiftRetrier(conf.Swift)
		if err != nil {
			return mux, fmt.Errorf("failed to config Swift storage backend: %s", err)
		}
		mux.Backends = append(mux.Backends, s)
	}

	for _, c := range conf.GenericS3 {
		if c.Valid() {
			s, err := NewGenericS3(c)
			if err != nil {
				return mux, fmt.Errorf("failed to config generic S3 storage backend: %s", err)
			}
			mux.Backends = append(mux.Backends, s)
		}
	}

	if conf.HTTPStorage.Valid() {
		http, err := NewHTTP(conf.HTTPStorage)
		if err != nil {
			return mux, fmt.Errorf("failed to config http storage backend: %s", err)
		}
		mux.Backends = append(mux.Backends, http)
	}

	if conf.FTPStorage != nil && conf.FTPStorage.Valid() {
		ftp, err := NewFTP(conf.FTPStorage)
		if err != nil {
			return mux, fmt.Errorf("failed to config ftp storage backend: %s", err)
		}
		mux.Backends = append(mux.Backends, ftp)
	}

	return mux, nil
}

// Stat returns information about the object at the given storage URL.
func (mux *Mux) Stat(ctx context.Context, url string) (*Object, error) {
	backend, err := mux.findBackend(url, statOp)
	if err != nil {
		return nil, err
	}
	return backend.Stat(ctx, url)
}

// List lists the objects at the given url.
func (mux *Mux) List(ctx context.Context, url string) ([]*Object, error) {
	backend, err := mux.findBackend(url, listOp)
	if err != nil {
		return nil, err
	}
	return backend.List(ctx, url)
}

// Get downloads a file from a storage system at the given "url".
// The file is downloaded to the given local "path".
func (mux *Mux) Get(ctx context.Context, url, path string) (*Object, error) {
	backend, err := mux.findBackend(url, getOp)
	if err != nil {
		return nil, err
	}
	return backend.Get(ctx, url, path)
}

// Put uploads a file to a storage system at the given "url".
// The file is uploaded from the given local "path".
func (mux *Mux) Put(ctx context.Context, url, path string) (*Object, error) {
	backend, err := mux.findBackend(url, putOp)
	if err != nil {
		return nil, err
	}
	return backend.Put(ctx, url, path)
}

// Join joins the given URL with the given subpath.
func (mux *Mux) Join(url, path string) (string, error) {
	backend, err := mux.findBackend(url, joinOp)
	if err != nil {
		return "", err
	}
	return backend.Join(url, path)
}

// UnsupportedOperations describes which operations (Get, Put, etc) are not
// supported for the given URL.
func (mux *Mux) UnsupportedOperations(url string) UnsupportedOperations {
	unsupported := UnsupportedOperations{}
	b, err := mux.findBackend(url, getOp)
	if err != nil {
		unsupported.Get = err
	} else {
		unsupported.Get = b.UnsupportedOperations(url).Get
	}

	b, err = mux.findBackend(url, putOp)
	if err != nil {
		unsupported.Put = err
	} else {
		unsupported.Put = b.UnsupportedOperations(url).Put
	}

	b, err = mux.findBackend(url, listOp)
	if err != nil {
		unsupported.List = err
	} else {
		unsupported.List = b.UnsupportedOperations(url).List
	}

	b, err = mux.findBackend(url, statOp)
	if err != nil {
		unsupported.Stat = err
	} else {
		unsupported.Stat = b.UnsupportedOperations(url).Stat
	}

	return unsupported
}

// AttachLogger will log information (such as retry warnings)
// to the given logger.
func (mux *Mux) AttachLogger(log *logger.Logger) {
	for _, b := range mux.Backends {
		if r, ok := b.(*Retrier); ok {
			r.Retrier.Notify = func(err error, sleep time.Duration) {
				log.Warn("Retrying", "error", err, "sleep", sleep)
			}
		}
	}
}

func (mux *Mux) findBackend(url string, op operation) (Storage, error) {
	var found = 0
	var useBackend Storage
	var err error
	var errs []string

	for _, backend := range mux.Backends {
		unsupported := backend.UnsupportedOperations(url)
		switch op {
		case getOp:
			err = unsupported.Get
		case putOp:
			err = unsupported.Put
		case listOp:
			err = unsupported.List
		case statOp:
			err = unsupported.Stat
		case joinOp:
			err = unsupported.Join
		}

		if err == nil {
			useBackend = backend
			found++
		} else {
			switch err.(type) {
			case *ErrUnsupportedProtocol:
				// noop
			case *ErrInvalidURL:
				errs = append(errs, err.Error())
			default:
				errs = append(errs, err.Error())
			}
		}
	}

	if found == 0 {
		return nil, fmt.Errorf("could not find matching storage system for: %s\n%s", url, strings.Join(errs, "\n"))
	} else if found > 1 {
		return nil, fmt.Errorf("request supported by multiple backends for: %s", url)
	}

	return useBackend, nil
}
