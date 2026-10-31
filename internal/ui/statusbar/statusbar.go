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

// WhichKey draws the pending section-jump line as exactly one line of w
// cells: the lead (section digit and name) highlighted, then each page's
// key and title, with tail ("esc cancel") at the right edge. When it does
// not fit it degrades in steps, keeping every key visible as long as
// possible: the tail drops its description, entries move closer together,
// then the longest titles are shortened (with ellipsis) one cell at a time,
// and finally titles go and trailing keys are dropped.
func WhichKey(s *styles.Styles, lead module.Hint, items []module.Hint, tail module.Hint, ellipsis string, w int) string {
	if w <= 0 {
		return ""
	}
	inner := w - 2
	leadS := s.SidebarJump.Render(" " + strings.TrimSpace(lead.Key+" "+lead.Desc) + " ")
	titles := make([]string, len(items))
	for i, it := range items {
		titles[i] = it.Desc
	}
	sep := 2
	width := func() int {
		n := 0
		for i, it := range items {
			if i > 0 {
				n += sep
			}
			n += lipgloss.Width(it.Key)
			if titles[i] != "" {
				n += 1 + lipgloss.Width(titles[i])
			}
		}
		return n
	}
	tailFor := func(withDesc bool) string {
		t := s.FooterKey.Render(tail.Key)
		if withDesc && tail.Desc != "" {
			t += " " + s.FooterDesc.Render(tail.Desc)
		}
		return t
	}
	tailS := tailFor(true)
	budget := func() int { return inner - lipgloss.Width(leadS) - 2 - lipgloss.Width(tailS) - 2 }
	if width() > budget() {
		tailS = tailFor(false)
	}
	if width() > budget() {
		sep = 1
	}
	ew := lipgloss.Width(ellipsis)
	for width() > budget() {
		// Shorten the longest title by one cell; give up on titles when the
		// longest is down to a single character plus the ellipsis.
		li, lw := -1, 0
		for i, t := range titles {
			if tw := lipgloss.Width(t); tw > lw {
				li, lw = i, tw
			}
		}
		if li < 0 || lw <= 1+ew {
			for i := range titles {
				titles[i] = ""
			}
			break
		}
		titles[li] = ansi.Truncate(items[li].Desc, lw-1, ellipsis)
	}
	gapS := strings.Repeat(" ", sep)
	var b strings.Builder
	used := 0
	for i, it := range items {
		seg := s.FooterKey.Render(it.Key)
		if titles[i] != "" {
			seg += " " + s.FooterDesc.Render(titles[i])
		}
		sw := lipgloss.Width(seg)
		if used > 0 {
			sw += sep
		}
		if used+sw > budget() {
			break
		}
		if used > 0 {
			b.WriteString(gapS)
		}
		b.WriteString(seg)
		used += sw
	}
	gap := max(2, inner-lipgloss.Width(leadS)-2-used-lipgloss.Width(tailS))
	line := " " + leadS + "  " + b.String() + strings.Repeat(" ", gap) + tailS + " "
	if lipgloss.Width(line) > w {
		line = ansi.Truncate(line, w, "")
	}
	if d := w - lipgloss.Width(line); d > 0 {
		line += strings.Repeat(" ", d)
	}
	return line
}
