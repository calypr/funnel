package fsutil

import (
	"os"
	"path"
	"syscall"
)

// exists returns whether the given file or directory exists or not
func exists(p string) (bool, error) {
	_, err := os.Stat(p)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return true, err
}

// EnsureDir ensures a directory exists.
func EnsureDir(p string) error {
	e, err := exists(p)
	if err != nil {
		return err
	}
	if !e {
		// TODO configurable mode?
		_ = syscall.Umask(0000)
		err := os.MkdirAll(p, 0775)
		if err != nil {
			return err
		}
	}
	return nil
}

// EnsurePath ensures a directory exists, given a file path. This calls path.Dir(p)
func EnsurePath(p string) error {
	dir := path.Dir(p)
	return EnsureDir(dir)
}
