// Package version exposes build metadata stamped in via -ldflags.
package version

import "fmt"

var (
	Version = "dev"
	Commit  = "none"
	Build   = "unknown"
)

// String returns a one-line version description.
func String() string {
	return fmt.Sprintf("%s (commit %s, built %s)", Version, Commit, Build)
}
