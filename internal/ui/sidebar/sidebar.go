// Package sidebar renders the module navigation list.
package sidebar

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/tonhe/fathom/internal/icons"
	"github.com/tonhe/fathom/internal/ui/styles"
)

// Item is one navigation entry. Key is the jump key ("1"), Badge a short
// count or state shown right-aligned, Dim marks unavailable modules.
type Item struct {
	ID, Title, Icon, Badge string
	Dim                    bool
	Key                    string
}

// Render draws items as exactly w x h. The active item is highlighted.
func Render(s *styles.Styles, ic icons.Set, items []Item, active string, w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	lines := make([]string, 0, h)
	for _, it := range items {
		if len(lines) == h {
			break
		}
		lines = append(lines, line(s, ic, it, it.ID == active, w))
	}
	blank := strings.Repeat(" ", w)
	for len(lines) < h {
		lines = append(lines, blank)
	}
	return strings.Join(lines, "\n")
}

func line(s *styles.Styles, ic icons.Set, it Item, act bool, w int) string {
	marker := " "
	if act {
		marker = ic.Arrow
	}
	left := marker + " "
	if it.Key != "" {
		left += it.Key + " "
	}
	if it.Icon != "" {
		left += it.Icon + " "
	}
	left += it.Title
	badge := it.Badge
	bw := 0
	if badge != "" {
		bw = lipgloss.Width(badge) + 2
	}
	room := w - 1 // trailing space
	if bw > 0 && lipgloss.Width(left)+1+bw > room {
		if lipgloss.Width(marker+" "+it.Title)+1+bw > room {
			badge, bw = "", 0
		}
	}
	tw := room - bw
	if bw > 0 {
		tw--
	}
	if lipgloss.Width(left) > tw {
		left = ansi.Truncate(left, max(0, tw), ic.Ellipsis)
		if lipgloss.Width(left) > tw {
			left = ansi.Truncate(left, max(0, tw), "")
		}
	}
	gap := max(0, w-lipgloss.Width(left)-bw)
	if act {
		txt := left + strings.Repeat(" ", gap)
		if badge != "" {
			txt += " " + badge + " "
		}
		return s.SidebarActive.Render(fit(txt, w))
	}
	st := s.SidebarItem
	if it.Dim {
		st = s.SidebarDim
	}
	out := st.Render(left) + strings.Repeat(" ", gap)
	if badge != "" {
		out += s.Badge.Render(badge)
	}
	return fit(out, w)
}

func fit(s string, w int) string {
	if lipgloss.Width(s) > w {
		s = ansi.Truncate(s, w, "") + ansi.ResetStyle
	}
	if d := w - lipgloss.Width(s); d > 0 {
		s += strings.Repeat(" ", d)
	}
	return s
}
