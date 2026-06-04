// Package version reports the Funnel version.
// Important: these fields are populated in the Makefile when running `make install`
package version

import "fmt"

// Build and version details
var (
	GitCommit   = ""
	GitBranch   = ""
	GitUpstream = ""
	BuildDate   = ""
	Version     = "unknown"
)

var tpl = `git commit:   %s
git branch:   %s
git upstream: %s
build date:   %s
version:      %s`

// String formats a string with version details.
func String() string {
	return fmt.Sprintf(tpl, GitCommit, GitBranch, GitUpstream, BuildDate, Version)
}

// LogFields logs build and version information to the given logger.
func LogFields() []interface{} {
	return []interface{}{
		"GitCommit", GitCommit,
		"GitBranch", GitBranch,
		"GitUpstream", GitUpstream,
		"BuildDate", BuildDate,
		"Version", Version,
	}
}
