//go:build !linux

package storage

import (
	"context"
	"os"
)

func writeRootDownloadedFilePlatform(ctx context.Context, parent *os.Root, name string, write func(*os.File) error) error {
	return writeRootFileAtomically(ctx, parent, name, write)
}
