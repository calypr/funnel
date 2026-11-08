package storage

import (
	"context"
)

// Fake implements a the Storage interface with methods that do nothing
// and return nil. This is a testing utility.
type Fake struct{}

// Stat returns information about the object at the given storage URL.
func (f Fake) Stat(ctx context.Context, url string) (*Object, error) {
	return nil, nil
}

// List a directory. Calling List on a File is an error.
func (f Fake) List(ctx context.Context, url string) ([]*Object, error) {
	return nil, nil
}

// Get a single object from storage URL, written to a local file path.
func (f Fake) Get(ctx context.Context, url, path string) (*Object, error) {
	return nil, nil
}

// Put a single object to storage URL, from a local file path.
// Returns the Object that was created in storage.
func (f Fake) Put(ctx context.Context, url, path string) (*Object, error) {
	return nil, nil
}

// Join a directory URL with a subpath.
func (f Fake) Join(url, path string) (string, error) {
	return "", nil
}

// UnsupportedOperations describes which operations are supported by the storage system
// for the given URL.
func (f Fake) UnsupportedOperations(url string) UnsupportedOperations {
	return AllSupported()
}
