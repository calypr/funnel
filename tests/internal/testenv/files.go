package testenv

import (
	"fmt"
	"os"
	"path/filepath"
)

// Tempdir returns a new temporary directory path
func (f *Funnel) Tempdir() string {
	d, err := os.MkdirTemp(f.StorageDir, "")
	if err != nil {
		panic(fmt.Errorf("create test storage directory: %w", err))
	}
	d, err = filepath.Abs(d)
	if err != nil {
		panic(fmt.Errorf("resolve test storage directory: %w", err))
	}
	return d
}

// WriteFile writes a file to the local (temporary) storage directory.
func (f *Funnel) WriteFile(name string, content string) {
	err := os.WriteFile(f.StorageDir+"/"+name, []byte(content), os.ModePerm)
	if err != nil {
		panic(err)
	}
}

// ReadFile reads a file to the local (temporary) storage directory.
func (f *Funnel) ReadFile(name string) string {
	b, err := os.ReadFile(f.StorageDir + "/" + name)
	if err != nil {
		panic(err)
	}
	return string(b)
}
