// Package empty renders an empty-state box centred in an area.
package empty

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/tonhe/fathom/internal/ui/styles"
)

// Render centres a box with title, hint and an optional key ("press n to
// add") in exactly w x h. The box is dropped when it does not fit.
func Render(s *styles.Styles, title, hint, key string, w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	lines := []string{s.Strong.Render(title)}
	if hint != "" {
		lines = append(lines, s.Muted.Render(hint))
	}
	if key != "" {
		lines = append(lines, "", s.Muted.Render("press ")+s.FooterKey.Render(key))
	}
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(s.R.Border).Padding(0, 2)
	maxInner := w - box.GetHorizontalFrameSize()
	var content string
	if maxInner >= 8 && h >= len(lines)+2 {
		for i, l := range lines {
			if lipgloss.Width(l) > maxInner {
				lines[i] = ansi.Truncate(l, maxInner, "…") + ansi.ResetStyle
			}
		}
		content = box.Render(lipgloss.JoinVertical(lipgloss.Center, lines...))
	} else {
		for i, l := range lines {
			if lipgloss.Width(l) > w {
				lines[i] = ansi.Truncate(l, w, "") + ansi.ResetStyle
			}
		}
		if len(lines) > h {
			lines = lines[:h]
		}
		content = lipgloss.JoinVertical(lipgloss.Center, lines...)
	}
	out := lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, content)
	return strings.TrimRight(out, "\n")
}
