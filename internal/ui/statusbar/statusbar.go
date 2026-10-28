// Package statusbar renders the bottom key-hint line.
package statusbar

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/ui/styles"
)

// Render draws as many hints as fit, plus right-aligned text, as exactly one
// line of w cells. Hints are kept in order; the first that does not fit and
// everything after it are dropped.
func Render(s *styles.Styles, hints []module.Hint, right string, w int) string {
	if w <= 0 {
		return ""
	}
	inner := w - 2
	rw := lipgloss.Width(right)
	if rw > inner {
		right = ansi.Truncate(right, max(0, inner), "")
		rw = lipgloss.Width(right)
	}
	budget := inner - rw
	if rw > 0 {
		budget--
	}
	var b strings.Builder
	used := 0
	for _, h := range hints {
		seg := s.FooterKey.Render(h.Key)
		if h.Desc != "" {
			seg += " " + s.FooterDesc.Render(h.Desc)
		}
		sw := lipgloss.Width(seg)
		if used > 0 {
			sw += 2
		}
		if used+sw > budget {
			break
		}
		if used > 0 {
			b.WriteString("  ")
		}
		b.WriteString(seg)
		used += sw
	}
	gap := max(0, inner-used-rw)
	line := " " + b.String() + strings.Repeat(" ", gap) + s.Muted.Render(right) + " "
	if lipgloss.Width(line) > w {
		line = ansi.Truncate(line, w, "")
	}
	if d := w - lipgloss.Width(line); d > 0 {
		line += strings.Repeat(" ", d)
	}
	return line
}
