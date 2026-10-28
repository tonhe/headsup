// Package tabs renders sidebar items as a one-line tab bar for narrow
// terminals.
package tabs

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/tonhe/headsup/internal/icons"
	"github.com/tonhe/headsup/internal/ui/sidebar"
	"github.com/tonhe/headsup/internal/ui/styles"
)

// Item is the shared navigation item type.
type Item = sidebar.Item

// Render draws items as one line of exactly w cells, scrolled so the active
// tab is always visible; hidden tabs are marked with the ellipsis glyph.
func Render(s *styles.Styles, ic icons.Set, items []Item, active string, w int) string {
	if w <= 0 {
		return ""
	}
	segs := make([]string, len(items))
	widths := make([]int, len(items))
	act := 0
	for i, it := range items {
		label := it.Title
		if it.Key != "" {
			label = it.Key + " " + label
		}
		if it.Badge != "" {
			label += " (" + it.Badge + ")"
		}
		label = " " + label + " "
		switch {
		case it.ID == active:
			act = i
			segs[i] = s.SidebarActive.Render(label)
		case it.Dim:
			segs[i] = s.SidebarDim.Render(label)
		default:
			segs[i] = s.SidebarItem.Render(label)
		}
		widths[i] = lipgloss.Width(label)
	}
	sep := s.Muted.Render("│")
	el := s.Muted.Render(ic.Ellipsis)
	ew := lipgloss.Width(ic.Ellipsis)
	total := func(lo, hi int) int {
		n := 0
		for i := lo; i <= hi; i++ {
			n += widths[i]
		}
		n += hi - lo // separators
		if lo > 0 {
			n += ew
		}
		if hi < len(items)-1 {
			n += ew
		}
		return n
	}
	lo, hi := 0, len(items)-1
	if len(items) > 0 && total(lo, hi) > w {
		lo, hi = act, act
		for {
			grew := false
			if hi+1 < len(items) && total(lo, hi+1) <= w {
				hi++
				grew = true
			}
			if lo > 0 && total(lo-1, hi) <= w {
				lo--
				grew = true
			}
			if !grew {
				break
			}
		}
	}
	var b strings.Builder
	if lo > 0 {
		b.WriteString(el)
	}
	for i := lo; i <= hi && i < len(items); i++ {
		if i > lo {
			b.WriteString(sep)
		}
		b.WriteString(segs[i])
	}
	if hi < len(items)-1 {
		b.WriteString(el)
	}
	out := b.String()
	if lipgloss.Width(out) > w {
		out = ansi.Truncate(out, w, "") + ansi.ResetStyle
	}
	if d := w - lipgloss.Width(out); d > 0 {
		out += strings.Repeat(" ", d)
	}
	return out
}
