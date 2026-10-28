package theme

import (
	"os"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// detectProfile asks the terminal what it supports. A variable for tests.
var detectProfile = func() termenv.Profile {
	return termenv.NewOutput(os.Stdout).EnvColorProfile()
}

// Profile returns the colour depth to render with. NO_COLOR (any non-empty
// value) forces Ascii; otherwise the terminal is probed (COLORTERM, TERM,
// CLICOLOR_FORCE, ...).
func Profile() termenv.Profile { return ProfileFor(os.Getenv, detectProfile) }

// ProfileFor is Profile with injectable environment and detection.
func ProfileFor(env func(string) string, detect func() termenv.Profile) termenv.Profile {
	if env("NO_COLOR") != "" {
		return termenv.Ascii
	}
	return detect()
}

// ApplyProfile sets lipgloss's default renderer to p, so truecolor theme
// colours are downsampled to 256 or 16 colours, or dropped for Ascii.
func ApplyProfile(p termenv.Profile) { lipgloss.SetColorProfile(p) }
