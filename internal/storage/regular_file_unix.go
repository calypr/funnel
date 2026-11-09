//go:build unix

package storage

import (
	"os"
	"syscall"
)

const mountedFileOperationsSupported = true

func openRegularFile(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	return checkOpenedRegularFile(file, path)
}

// OpenRootRegularFile opens a confined regular file without blocking on a FIFO.
func OpenRootRegularFile(root *os.Root, path string) (*os.File, error) {
	file, err := root.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	return checkOpenedRegularFile(file, path)
}
