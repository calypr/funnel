package testenv

import (
	"fmt"
	"os"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

func TestConfigArtifactsAreRemovedAfterTest(t *testing.T) {
	artifactMode(t, false)
	var paths []string
	t.Cleanup(func() {
		for _, path := range paths {
			if err := os.RemoveAll(path); err != nil {
				t.Error(err)
			}
		}
	})
	t.Run("allocate", func(t *testing.T) {
		conf := DefaultConfig(t)
		paths = []string{conf.Worker.WorkDir, conf.LocalStorage.AllowedDirs[0]}
		for _, path := range paths {
			if _, err := os.Stat(path); err != nil {
				t.Fatal(err)
			}
		}
	})
	for _, path := range paths {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("generated test artifacts remain at %s: %v", path, err)
		}
	}
}

func artifactMode(t *testing.T, keep bool) {
	t.Helper()
	old := keepArtifacts
	keepArtifacts = keep
	t.Cleanup(func() { keepArtifacts = old })
}

func TestConfigCleanupUsesAllocatedPaths(t *testing.T) {
	artifactMode(t, false)
	survivor := t.TempDir()
	var allocated []string
	t.Run("mutated configuration", func(t *testing.T) {
		conf := DefaultConfig(t)
		allocated = []string{conf.Worker.WorkDir, conf.LocalStorage.AllowedDirs[0]}
		conf.Worker.WorkDir = survivor
		conf.LocalStorage.AllowedDirs = []string{survivor}
	})
	for _, path := range allocated {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("owned directory remains: %s", path)
		}
	}
	if _, err := os.Stat(survivor); err != nil {
		t.Fatalf("cleanup removed an unowned directory: %v", err)
	}
}

func TestArtifactRetentionKeepsFilesButClosesDatabase(t *testing.T) {
	for _, keep := range []bool{false, true} {
		t.Run(fmt.Sprintf("keep=%t", keep), func(t *testing.T) {
			artifactMode(t, keep)
			var paths []string
			t.Cleanup(func() {
				for _, path := range paths {
					if err := os.RemoveAll(path); err != nil {
						t.Error(err)
					}
				}
			})
			t.Run("fixture", func(t *testing.T) {
				conf := DefaultConfig(t)
				conf.Compute = "noop"
				paths = []string{conf.Worker.WorkDir, conf.LocalStorage.AllowedDirs[0]}
				dbPath := conf.BoltDB.Path
				// This runs after Funnel shutdown and before directory cleanup.
				t.Cleanup(func() {
					db, err := bolt.Open(dbPath, 0600, &bolt.Options{Timeout: 100 * time.Millisecond})
					if err != nil {
						t.Fatalf("test database remains locked after shutdown: %v", err)
					}
					if err := db.Close(); err != nil {
						t.Error(err)
					}
				})
				NewFunnel(t, conf)
			})
			for _, path := range paths {
				_, err := os.Stat(path)
				if keep && err != nil {
					t.Errorf("retained directory missing: %v", err)
				}
				if !keep && !os.IsNotExist(err) {
					t.Errorf("directory remains after cleanup: %s", path)
				}
			}
		})
	}
}

func TestSuiteCleanupOnSkipFailureAndPanic(t *testing.T) {
	artifactMode(t, false)
	for _, mode := range []string{"success", "skip", "failure", "panic", "cleanup-error"} {
		t.Run(mode, func(t *testing.T) {
			var dir string
			var ran bool
			var recovered any
			code := 0
			func() {
				defer func() { recovered = recover() }()
				code = runSuite(func() int {
					ran = true
					if mode == "failure" {
						return 7
					}
					return 0
				}, func(scope CleanupScope) bool {
					dir = artifactDir(scope, "funnel-test-scope-")
					if mode == "panic" {
						panic("setup failed")
					}
					if mode == "cleanup-error" {
						scope.Cleanup(func() { scope.Errorf("intentional cleanup error") })
					}
					return mode != "skip"
				})
			}()
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Errorf("suite artifacts remain after %s", mode)
			}
			if mode == "panic" {
				if recovered != "setup failed" || ran {
					t.Fatalf("setup panic was lost or suite ran: %v, %t", recovered, ran)
				}
				return
			}
			expected := 0
			if mode == "failure" {
				expected = 7
			}
			if mode == "cleanup-error" {
				expected = 1
			}
			if code != expected || ran != (mode != "skip") {
				t.Fatalf("exit %d, ran %t", code, ran)
			}
		})
	}
}
