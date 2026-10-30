package storage

import (
	"fmt"
	"os"
)

func checkOpenedRegularFile(file *os.File, path string) (*os.File, error) {
	info, err := file.Stat()
	if err != nil {
		_ = file.Close() //nolint:errcheck // Preserve the stat error; closing this rejected input cannot change the result.
		return nil, fmt.Errorf("stat opened file %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		_ = file.Close() //nolint:errcheck // Preserve the validation error; closing this rejected input cannot change the result.
		return nil, fmt.Errorf("%q is not a regular file", path)
	}
	return file, nil
}
