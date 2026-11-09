package config

import (
	"io/fs"
	"path"
	"strings"
)

// Examples returns the bundled configurations and scheduler templates by CLI name.
func Examples() map[string]string {
	examples := make(map[string]string)
	err := fs.WalkDir(bundled, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		key := strings.TrimSuffix(path.Base(name), path.Ext(name))
		examples[key] = embeddedText(name)
		return nil
	})
	if err != nil {
		panic(err)
	}
	return examples
}
