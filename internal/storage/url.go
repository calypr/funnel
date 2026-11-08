package storage

import (
	"net/url"
	"strings"
)

func joinURLPath(base, path string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}

	// JoinPath expects escaped segments; escape each separately to keep slashes.
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return u.JoinPath(segments...).String(), nil
}
