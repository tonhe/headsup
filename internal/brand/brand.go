// Package brand holds the application's name in one place. The project name
// is a placeholder; renaming means editing this file and the module path.
package brand

const (
	// Name is the lowercase identifier used for the binary, config
	// directories, keyring service, and environment variable prefix.
	Name = "fathom"
	// Display is the human-facing product name.
	Display = "Fathom"
	// EnvPrefix prefixes the app's own environment variables (FATHOM_URL, ...).
	EnvPrefix = "FATHOM"
	// Repo is the canonical source repository.
	Repo = "github.com/tonhe/fathom"
)
