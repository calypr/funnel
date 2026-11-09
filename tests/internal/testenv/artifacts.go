package testenv

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// CleanupScope owns integration-test resources for a test or an entire suite.
type CleanupScope interface {
	Cleanup(func())
	Errorf(string, ...any)
	Logf(string, ...any)
}

var keepArtifacts bool

func init() {
	flag.StringVar(&configFile, "funnel-config", "", "Funnel configuration file. Must be an absolute path.")
	flag.BoolVar(&keepArtifacts, "funnel-keep-artifacts", false, "Keep generated integration-test files after shutdown; use -v to display their paths.")
}

// RunSuite releases suite resources on normal exit, skipped setup, and setup panic.
// Setup returns false when the configured integration backend is unavailable.
func RunSuite(m *testing.M, setup func(CleanupScope) bool) int {
	ParseConfig()
	return runSuite(m.Run, setup)
}

func runSuite(run func() int, setup func(CleanupScope) bool) (code int) {
	scope := &suiteScope{}
	defer func() {
		scope.close()
		scope.mu.Lock()
		failed := scope.failed
		scope.mu.Unlock()
		if failed && code == 0 {
			code = 1
		}
	}()
	if !setup(scope) {
		return 0
	}
	return run()
}

type suiteScope struct {
	mu        sync.Mutex
	callbacks []func()
	failed    bool
}

func (s *suiteScope) Cleanup(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.callbacks = append(s.callbacks, fn)
}

func (s *suiteScope) Errorf(format string, args ...any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failed = true
	fmt.Fprintf(os.Stderr, "test cleanup: "+format+"\n", args...)
}

func (s *suiteScope) Logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

func (s *suiteScope) close() {
	s.mu.Lock()
	if len(s.callbacks) == 0 {
		s.mu.Unlock()
		return
	}
	last := len(s.callbacks) - 1
	fn := s.callbacks[last]
	s.callbacks = s.callbacks[:last]
	s.mu.Unlock()
	// Remaining resources must still close if one cleanup callback panics.
	defer s.close()
	fn()
}

func artifactDir(scope CleanupScope, prefix string) string {
	root, err := filepath.Abs("test_tmp")
	if err != nil {
		panic(fmt.Errorf("resolve test artifact directory: %w", err))
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		panic(fmt.Errorf("create test artifact directory: %w", err))
	}
	dir, err := os.MkdirTemp(root, prefix)
	if err != nil {
		panic(fmt.Errorf("create test artifact directory: %w", err))
	}
	scope.Cleanup(func() {
		if keepArtifacts {
			scope.Logf("Retained test artifacts: %s", dir)
			return
		}
		if err := os.RemoveAll(dir); err != nil {
			scope.Errorf("remove generated directory %s: %v", dir, err)
		}
	})
	return dir
}
