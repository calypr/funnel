//go:build !unix

package storage

import "os"

func openLocalFile(root *os.Root, path string, flags int, mode os.FileMode) (*os.File, error) {
	if root != nil {
		return root.OpenFile(path, flags, mode)
	}
	return os.OpenFile(path, flags, mode)
}

func preserveLocalFileMetadata(file *os.File, source os.FileInfo) error {
	return nil
}
