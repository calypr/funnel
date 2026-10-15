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

func openRootRegularFile(root *os.Root, path string) (*os.File, error) {
	file, err := root.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	return checkOpenedRegularFile(file, path)
}
