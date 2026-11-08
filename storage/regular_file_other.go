//go:build !unix

package storage

import (
	"errors"
	"os"
)

const mountedFileOperationsSupported = false

func openRegularFile(path string) (*os.File, error) {
	return nil, errors.New("mounted S3 file access requires Unix nonblocking file opens")
}

// OpenRootRegularFile opens a confined regular file without blocking on a FIFO.
func OpenRootRegularFile(root *os.Root, path string) (*os.File, error) {
	return nil, errors.New("mounted S3 file access requires Unix nonblocking file opens")
}
