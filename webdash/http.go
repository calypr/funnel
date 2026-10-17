package webdash

import (
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"sync"
)

//go:embed all:build
var buildFS embed.FS

var fsys = func() fs.FS {
	sub, err := fs.Sub(buildFS, "build")
	if err != nil {
		return errorFS{err}
	}
	return sub
}()

// errorFS fails every Open so a broken embed surfaces as a 500 instead of a
// panic during package initialization.
type errorFS struct{ err error }

func (e errorFS) Open(name string) (fs.File, error) {
	return nil, fmt.Errorf("webdash: %w", e.err)
}

var index = sync.OnceValues(func() ([]byte, error) {
	return fs.ReadFile(fsys, "index.html")
})

// FileServer provides access to the bundled web assets (HTML, CSS, etc)
// via an http.Handler
func FileServer() http.Handler {
	return http.FileServer(http.FS(fsys))
}

// RootHandler returns an http handler which always responds with the /index.html file.
func RootHandler() http.Handler {
	return http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
		b, err := index()
		if err != nil {
			http.Error(resp, "web dashboard assets unavailable", http.StatusInternalServerError)
			return
		}
		_, _ = resp.Write(b)
	})
}
