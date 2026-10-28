// Package kv renders an aligned key/value list.
package kv

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/tonhe/headsup/internal/ui/styles"
)

// Pair is one row. Value may be pre-styled and may contain newlines (shown
// as continuation lines). An empty Key continues the previous row.
type Pair struct{ Key, Value string }

// Pairs builds pairs from alternating key, value arguments.
func Pairs(kv ...string) []Pair {
	out := make([]Pair, 0, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		out = append(out, Pair{kv[i], kv[i+1]})
	}
	return out
}

// Render aligns keys (s.Label) in a column of at most w/3 cells and clips
// every line to w.
func Render(s *styles.Styles, pairs []Pair, w int) string {
	if w <= 0 {
		return ""
	}
	kw := 0
	for _, p := range pairs {
		kw = max(kw, lipgloss.Width(p.Key))
	}
	kw = min(kw, max(1, w/3))
	vw := max(0, w-kw-2)
	var lines []string
	for _, p := range pairs {
		key := p.Key
		if lipgloss.Width(key) > kw {
			key = ansi.Truncate(key, kw, "")
		}
		key = s.Label.Render(key) + strings.Repeat(" ", kw-lipgloss.Width(key)+2)
		for i, v := range strings.Split(p.Value, "\n") {
			if i > 0 {
				key = strings.Repeat(" ", kw+2)
			}
			if lipgloss.Width(v) > vw {
				v = ansi.Truncate(v, vw, "…") + ansi.ResetStyle
			}
			l := key + v
			if lipgloss.Width(l) > w {
				l = ansi.Truncate(l, w, "") + ansi.ResetStyle
			}
			lines = append(lines, l)
		}
	}
	return strings.Join(lines, "\n")
}
