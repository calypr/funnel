package storage

import (
	"context"
	"time"
)

// Storage provides an interface for a storage backend,
// providing access to concrete storage systems such as Google Storage,
// local filesystem, etc.
//
// Storage backends must be safe for concurrent use.
type Storage interface {
	// Stat returns information about the object at the given storage URL.
	Stat(ctx context.Context, url string) (*Object, error)

	// List a directory. Calling List on a File is an error.
	List(ctx context.Context, url string) ([]*Object, error)

	// Get a single object from storage URL, written to a local file path.
	Get(ctx context.Context, url, path string) (*Object, error)

	// Put a single object to storage URL, from a local file path.
	// Returns the Object that was created in storage.
	Put(ctx context.Context, url, path string) (*Object, error)

	// Join a directory URL with a subpath.
	Join(url, path string) (string, error)

	// UnsupportedOperations describes which operations are supported by the storage system
	// for the given URL.
	//
	// A backend normally uses this to match the url prefix (e.g. "s3://"),
	// and some backends (generic s3) might check for the existence of the bucket.
	UnsupportedOperations(url string) UnsupportedOperations
}

// Object represents metadata about an object in storage.
type Object struct {
	// The storage-specific full URL of the object.
	// e.g. for S3 this might be "s3://my-bucket/dir1/obj.txt"
	URL string

	// The name of the object in the storage system.
	// e.g. for S3 this might be "dir/object.txt"
	Name string

	// ETag is an identifier for a specific version of the object.
	// This is an opaque string. Each system has a different representation:
	// md5, sha1, crc32, etc. This field may be empty, if the system can't provide
	// a unique ID (for example the local filesystem).
	ETag string

	LastModified time.Time

	// Size of the object, in bytes.
	Size int64
}

// UnsupportedOperations describes any operations that are not supported
// by a storage backend.
type UnsupportedOperations struct {
	Get, Put, List, Stat, Join error
}

// AllSupported returns an UnsupportedOperations indicating that
// all operations are supported.
func AllSupported() UnsupportedOperations {
	return UnsupportedOperations{}
}

// AllUnsupported returns an UnsupportedOperations indicating that
// no operations are supported.
func AllUnsupported(err error) UnsupportedOperations {
	return UnsupportedOperations{
		Get:  err,
		Put:  err,
		List: err,
		Stat: err,
		Join: err,
	}
}

type urlparts struct {
	bucket, path string
}
