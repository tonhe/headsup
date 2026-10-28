// Package badge renders small chips: counts, states and tags.
package badge

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/tonhe/fathom/internal/ui/styles"
)

// Render draws text as a s.Badge chip.
func Render(s *styles.Styles, text string) string { return s.Badge.Render(text) }

// Chip draws text with st plus one cell of horizontal padding.
func Chip(st lipgloss.Style, text string) string { return st.Padding(0, 1).Render(text) }

// Tag draws a tag chip using s.Tag; a missing "tag:" prefix is not added.
func Tag(s *styles.Styles, tag string) string { return Chip(s.Tag, tag) }

// Tags draws tag chips separated by spaces that fit in w cells, ending in a
// muted "+N" when some do not fit. w <= 0 means unlimited.
func Tags(s *styles.Styles, tags []string, w int) string {
	var parts []string
	used := 0
	for i, t := range tags {
		c := Tag(s, t)
		cw := lipgloss.Width(c)
		if used > 0 {
			cw++
		}
		more := ""
		if rest := len(tags) - i - 1; rest > 0 {
			more = fmt.Sprintf(" +%d", rest)
		}
		if w > 0 && used+cw+lipgloss.Width(more) > w {
			m := fmt.Sprintf("+%d", len(tags)-i)
			if used > 0 {
				m = " " + m
			}
			if used+lipgloss.Width(m) <= w {
				parts = append(parts, s.Muted.Render(m))
			}
			break
		}
		if used > 0 {
			parts = append(parts, " ")
		}
		parts = append(parts, c)
		used += cw
	}
	return strings.Join(parts, "")
}
