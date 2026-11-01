package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLocalTransfersDoNotShareSourceInode(t *testing.T) {
	for _, operation := range []string{"Get", "Put"} {
		t.Run(operation, func(t *testing.T) {
			root := t.TempDir()
			source, dest := filepath.Join(root, "source"), filepath.Join(root, "destination")
			if err := os.WriteFile(source, []byte("original"), 0600); err != nil {
				t.Fatal(err)
			}
			local := &Local{allowedDirs: []string{root}}
			var err error
			if operation == "Get" {
				_, err = local.Get(t.Context(), source, dest)
			} else {
				_, err = local.Put(t.Context(), dest, source)
			}
			if err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(dest)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() & ^os.FileMode(0600) != 0 {
				t.Error("transfer widened source permissions")
			}
			if err := os.WriteFile(dest, []byte("changed"), 0600); err != nil {
				t.Fatal(err)
			}
			body, err := os.ReadFile(source)
			if err != nil {
				t.Fatal(err)
			}
			if string(body) != "original" {
				t.Fatal("changing the transfer destination changed its source")
			}
		})
	}
}
