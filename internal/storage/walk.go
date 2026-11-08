package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Hostfile returns information about a file found by WalkFiles.
type Hostfile struct {
	// The path relative to the "root" given to WalkFiles().
	Rel string
	// The absolute path of the file on the host.
	Abs string
	// Size in bytes.
	Size int64
	// LastModified time
	LastModified time.Time
}

// WalkFiles recursively walks a directory, returning a list of files.
func WalkFiles(root string) ([]Hostfile, error) {
	var files []Hostfile

	if dinfo, err := os.Stat(root); os.IsNotExist(err) || !dinfo.IsDir() {
		return nil, fmt.Errorf("%s does not exist or is not a directory", root)
	}

	err := filepath.Walk(root, func(p string, f os.FileInfo, err error) error {
		if !f.IsDir() {
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			files = append(files, Hostfile{
				Rel:          rel,
				Abs:          p,
				Size:         f.Size(),
				LastModified: f.ModTime(),
			})
		}
		return nil
	})
	return files, err
}

// FileSize returns the file size in bytes, or return 0 if there's an error calling os.Stat().
func FileSize(path string) int64 {
	st, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return st.Size()
}
