// Package pane draws a rounded box with its title embedded in the top edge:
//
//	╭ title ─ extra ──────── right ─╮
//	│ body                          │
//	╰───────────────────────────────╯
package pane

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/tonhe/headsup/internal/ui/styles"
)

const (
	tl, tr, bl, br, hz, vt = "╭", "╮", "╰", "╯", "─", "│"
)

type options struct {
	extra, right, bottom string
	noPad                bool
}

// Opt customises a pane.
type Opt func(*options)

// WithExtra adds a muted segment after the title: "╭ title ─ extra ───╮".
func WithExtra(s string) Opt { return func(o *options) { o.extra = s } }

// WithRight adds a right-aligned label to the top edge.
func WithRight(s string) Opt { return func(o *options) { o.right = s } }

// WithBottom embeds a label in the bottom edge.
func WithBottom(s string) Opt { return func(o *options) { o.bottom = s } }

// NoPad removes the one-cell horizontal padding inside the border.
func NoPad() Opt { return func(o *options) { o.noPad = true } }

// Inner returns the body area for a pane of w x h with default padding.
func Inner(w, h int) (int, int) { return max(0, w-4), max(0, h-2) }

// Render draws the pane at exactly w x h. The body is padded or clipped.
func Render(s *styles.Styles, title, body string, w, h int, focused bool, opts ...Opt) string {
	var o options
	for _, f := range opts {
		f(&o)
	}
	if w < 2 || h < 1 {
		return ""
	}
	bs, ts := s.Pane, s.PaneTitle
	if focused {
		bs, ts = s.PaneFocus, s.PaneTitleFo
	}
	inner := w - 2
	lines := make([]string, 0, h)
	lines = append(lines, top(s, bs, ts, title, o.extra, o.right, inner))
	if h == 1 {
		return lines[0]
	}
	pad := 1
	if o.noPad {
		pad = 0
	}
	cw := max(0, inner-2*pad)
	padS := strings.Repeat(" ", pad)
	if inner < 2*pad {
		padS, cw = "", inner
	}
	body = strings.ReplaceAll(body, "\r", "")
	bodyLines := strings.Split(body, "\n")
	if body == "" {
		bodyLines = nil
	}
	v := bs.Render(vt)
	for i := 0; i < h-2; i++ {
		var l string
		if i < len(bodyLines) {
			l = Fit(bodyLines[i], cw)
		} else {
			l = strings.Repeat(" ", cw)
		}
		lines = append(lines, v+padS+l+padS+v)
	}
	lines = append(lines, bottomEdge(s, bs, o.bottom, inner))
	return strings.Join(lines, "\n")
}

// Fit truncates (ANSI- and wide-char-safe) or pads line to exactly w cells.
func Fit(line string, w int) string {
	if w <= 0 {
		return ""
	}
	lw := lipgloss.Width(line)
	if lw > w {
		line = ansi.Truncate(line, w, "")
		lw = lipgloss.Width(line)
		line += ansi.ResetStyle
	}
	if lw < w {
		line += strings.Repeat(" ", w-lw)
	}
	return line
}

func top(s *styles.Styles, bs, ts lipgloss.Style, title, extra, right string, inner int) string {
	// Budget: "─" + " title " + ["─ extra "] + fill + [" right "] + "─"
	tw := func(x string) int { return lipgloss.Width(x) }
	fixed := 1 // leading dash after the corner is the space; keep one trailing dash
	avail := inner - fixed
	seg := func(x string) int { return tw(x) + 2 }
	if title != "" && seg(title) > avail {
		title = ansi.Truncate(title, max(1, avail-2), "…")
	}
	used := 0
	if title != "" {
		used = seg(title)
	}
	if extra != "" && used+seg(extra)+1 > avail {
		extra = ""
	}
	if extra != "" {
		used += seg(extra) + 1
	}
	if right != "" && used+seg(right)+1 > avail {
		right = ""
	}
	if right != "" {
		used += seg(right)
	}
	fill := max(0, avail-used)
	if title == "" && extra == "" && right == "" {
		return bs.Render(tl + strings.Repeat(hz, inner) + tr)
	}
	var b strings.Builder
	b.WriteString(bs.Render(tl))
	if title != "" {
		b.WriteString(" " + ts.Render(title) + " ")
	} else {
		b.WriteString(bs.Render(hz))
		fill = max(0, fill-1)
	}
	if extra != "" {
		b.WriteString(bs.Render(hz) + " " + s.Muted.Render(extra) + " ")
	}
	b.WriteString(bs.Render(strings.Repeat(hz, fill)))
	if right != "" {
		b.WriteString(" " + s.Muted.Render(right) + " ")
	}
	b.WriteString(bs.Render(hz + tr))
	return Fit(b.String(), inner+2)
}

func bottomEdge(s *styles.Styles, bs lipgloss.Style, label string, inner int) string {
	if label == "" || lipgloss.Width(label)+3 > inner {
		return bs.Render(bl + strings.Repeat(hz, inner) + br)
	}
	fill := inner - lipgloss.Width(label) - 3
	return bs.Render(bl+hz) + " " + label + " " + bs.Render(strings.Repeat(hz, fill)+br)
}
