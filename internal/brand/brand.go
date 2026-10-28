// Package brand holds the application's name in one place. Renaming means
// editing this file and the module path.
package brand

const (
	// Name is the lowercase identifier used for the binary, config
	// directories, keyring service, and environment variable prefix.
	Name = "headsup"
	// Display is the human-facing product name.
	Display = "Headsup"
	// EnvPrefix prefixes the app's own environment variables (HEADSUP_URL, ...).
	EnvPrefix = "HEADSUP"
	// Repo is the canonical source repository.
	Repo = "github.com/tonhe/headsup"
)
