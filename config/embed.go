package config

import (
	"embed"
	"io/fs"
	"strings"
)

// The bundled asset names are prefixed with "config/" to match the
// previous go-bindata based API.
//
//go:embed *.txt *.yaml datastore kubernetes
var bundled embed.FS

// AssetNames returns the names of the bundled config assets,
// prefixed with "config/".
func AssetNames() []string {
	var names []string
	err := fs.WalkDir(bundled, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			names = append(names, "config/"+p)
		}
		return nil
	})
	if err != nil {
		panic(err)
	}
	return names
}

// MustAsset returns the content of the named bundled config asset,
// panicking on error. The name must be prefixed with "config/".
func MustAsset(name string) []byte {
	b, err := bundled.ReadFile(strings.TrimPrefix(name, "config/"))
	if err != nil {
		panic(err)
	}
	return b
}
