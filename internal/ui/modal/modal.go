// Package modal composites boxes over a background view.
package modal

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/tonhe/headsup/internal/ui/styles"
)

// Center draws box centred over base, with base dimmed (ANSI stripped and
// re-rendered in s.Muted). The result is exactly w x h. A nil s dims with
// faint text instead.
func Center(s *styles.Styles, base, box string, w, h int) string {
	bw, bh := lipgloss.Width(box), lipgloss.Height(box)
	return Place(Dim(s, base, w, h), box, (w-bw)/2, (h-bh)/2, w, h)
}

// Dim returns base normalised to w x h with styles removed and re-rendered in
// the muted foreground.
func Dim(s *styles.Styles, base string, w, h int) string {
	st := lipgloss.NewStyle().Faint(true)
	if s != nil {
		st = s.Muted
	}
	lines := normalise(base, w, h)
	for i, l := range lines {
		lines[i] = st.Render(ansi.Strip(l))
	}
	return strings.Join(lines, "\n")
}

// Place draws box with its top-left corner at (x, y) over base without
// disturbing base cells outside the box. The result is exactly w x h; the box
// is clipped to the canvas.
func Place(base, box string, x, y, w, h int) string {
	lines := normalise(base, w, h)
	if w <= 0 || h <= 0 {
		return strings.Join(lines, "\n")
	}
	x, y = max(0, x), max(0, y)
	boxLines := strings.Split(box, "\n")
	bw := lipgloss.Width(box)
	for i, bl := range boxLines {
		row := y + i
		if row >= h {
			break
		}
		if x >= w {
			break
		}
		cw := min(bw, w-x)
		left := pad(ansi.Cut(lines[row], 0, x), x)
		mid := pad(ansi.Truncate(bl, cw, ""), cw)
		right := ansi.Cut(lines[row], x+cw, w)
		right = pad(right, w-x-cw)
		lines[row] = left + ansi.ResetStyle + mid + ansi.ResetStyle + right
	}
	return strings.Join(lines, "\n")
}

func normalise(base string, w, h int) []string {
	src := strings.Split(base, "\n")
	out := make([]string, h)
	for i := range out {
		var l string
		if i < len(src) {
			l = src[i]
		}
		if lipgloss.Width(l) > w {
			l = ansi.Truncate(l, w, "") + ansi.ResetStyle
		}
		out[i] = pad(l, w)
	}
	return out
}

func pad(s string, w int) string {
	if d := w - lipgloss.Width(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}
