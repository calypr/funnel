package configtest

import (
	"path/filepath"
	"testing"

	"github.com/calypr/funnel/api/config"
	appconfig "github.com/calypr/funnel/internal/config"
)

// WriteFile writes a configuration into a directory cleaned up by the test.
func WriteFile(t testing.TB, c *config.Config, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := appconfig.ToYamlFile(c, path); err != nil {
		t.Fatal(err)
	}
	return path
}
