// Package uitest holds small helpers shared by the ui component tests.
package uitest

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/tonhe/headsup/internal/theme"
	"github.com/tonhe/headsup/internal/ui/styles"
)

// StyleFor returns styles built from the theme with the given slug, falling
// back to the first registered theme when the slug is unknown.
func StyleFor(slug string) *styles.Styles {
	t := theme.ByName(slug)
	if t == nil {
		t = theme.ByIndex(0)
	}
	if t == nil {
		return styles.New(theme.Current)
	}
	return styles.New(*t)
}

// MaxWidth returns the widest line of s in terminal cells.
func MaxWidth(s string) int {
	m := 0
	for _, l := range strings.Split(s, "\n") {
		if w := lipgloss.Width(l); w > m {
			m = w
		}
	}
	return m
}

// Lines splits s into lines.
func Lines(s string) []string { return strings.Split(s, "\n") }

// ForceColor makes lipgloss emit true-colour ANSI so tests exercise escape
// handling even when stdout is not a terminal.
func ForceColor() { lipgloss.SetColorProfile(termenv.TrueColor) }
