package storage

import "fmt"

// ErrUnsupportedProtocol is returned by SupportsGet / SupportsPut when a url's
// protocol is unsupported by that backend
type ErrUnsupportedProtocol struct {
	backend string
}

func (e *ErrUnsupportedProtocol) Error() string {
	return fmt.Sprintf("%s: unsupported protocol", e.backend)
}

// ErrInvalidURL is returned by SupportsGet / SupportsPut when a url's
// format is invalid.
type ErrInvalidURL struct {
	backend string
}

func (e *ErrInvalidURL) Error() string {
	return fmt.Sprintf("%s: invalid url", e.backend)
}
