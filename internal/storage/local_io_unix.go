//go:build unix

package storage

import (
	"fmt"
	"os"
	"syscall"
)

func openLocalFile(root *os.Root, path string, flags int, mode os.FileMode) (*os.File, error) {
	// A FIFO must not block before its descriptor can be rejected by Stat.
	flags |= syscall.O_NONBLOCK
	if root != nil {
		return root.OpenFile(path, flags, mode)
	}
	return os.OpenFile(path, flags, mode)
}

func preserveLocalFileMetadata(file *os.File, source os.FileInfo) error {
	if os.Geteuid() != 0 {
		return nil
	}
	stat, ok := source.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("localStorage: unsupported source stat type %T", source.Sys())
	}
	if err := file.Chown(int(stat.Uid), int(stat.Gid)); err != nil {
		return err
	}
	return file.Chmod(source.Mode().Perm())
}
