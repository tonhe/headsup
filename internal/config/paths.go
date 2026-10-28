package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/tonhe/headsup/internal/brand"
)

// Overridable seams for tests.
var (
	getenv      = os.Getenv
	userHomeDir = os.UserHomeDir
	goos        = runtime.GOOS
)

// ConfigDir is $XDG_CONFIG_HOME/<app>, defaulting to ~/.config/<app>. On
// Windows, without XDG_CONFIG_HOME, it is %APPDATA%\<app>.
func ConfigDir() string {
	if d := getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, brand.Name)
	}
	if goos == "windows" {
		if d := getenv("APPDATA"); d != "" {
			return filepath.Join(d, brand.Name)
		}
	}
	return filepath.Join(home(), ".config", brand.Name)
}

// StateDir is $XDG_STATE_HOME/<app>, defaulting to ~/.local/state/<app>. On
// Windows, without XDG_STATE_HOME, it is %LOCALAPPDATA%\<app>\state.
func StateDir() string {
	if d := getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, brand.Name)
	}
	if goos == "windows" {
		if d := getenv("LOCALAPPDATA"); d != "" {
			return filepath.Join(d, brand.Name, "state")
		}
	}
	return filepath.Join(home(), ".local", "state", brand.Name)
}

// ThemesDir holds user theme files.
func ThemesDir() string { return filepath.Join(ConfigDir(), "themes") }

// ConfigFile is the default config.toml path.
func ConfigFile() string { return filepath.Join(ConfigDir(), "config.toml") }

// ExpandHome replaces a leading "~" or "~/" with the user's home directory.
func ExpandHome(p string) string {
	if p == "~" {
		return home()
	}
	if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		return filepath.Join(home(), p[2:])
	}
	return p
}

func home() string {
	if h, err := userHomeDir(); err == nil && h != "" {
		return h
	}
	return "."
}
