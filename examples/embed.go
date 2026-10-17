package examples

import (
	"embed"
	"io/fs"
	"strings"
)

// The bundled asset names are prefixed with "examples/" to match the
// previous go-bindata based API.
//
//go:embed *.json gcp
var bundled embed.FS

// AssetNames returns the names of the bundled example tasks,
// prefixed with "examples/".
func AssetNames() []string {
	var names []string
	err := fs.WalkDir(bundled, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			names = append(names, "examples/"+p)
		}
		return nil
	})
	if err != nil {
		panic(err)
	}
	return names
}

// MustAsset returns the content of the named bundled example task,
// panicking on error. The name must be prefixed with "examples/".
func MustAsset(name string) []byte {
	b, err := bundled.ReadFile(strings.TrimPrefix(name, "examples/"))
	if err != nil {
		panic(err)
	}
	return b
}
