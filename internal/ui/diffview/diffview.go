// Package diffview renders a unified line diff: hunk headers, old and new
// line numbers, and added and removed lines in the theme's OK and danger
// colours. It is generic: callers convert their diff lines to Line.
package diffview

import (
	"fmt"
	"strings"

	"github.com/tonhe/headsup/internal/ui/pane"
	"github.com/tonhe/headsup/internal/ui/styles"
)

// Line is one diff line. Old and New are 1-based; 0 means absent on that side.
type Line struct {
	Op   byte // ' ', '+', '-'
	Text string
	Old  int
	New  int
}

// row is one rendered row: a hunk header or a line.
type row struct {
	header string
	line   Line
}

// DefaultContext is the number of unchanged lines kept around each change.
const DefaultContext = 3

// Rows groups lines into hunks with ctx context lines and returns how many
// rows the diff renders as, including hunk headers.
func Rows(lines []Line, ctx int) int { return len(rows(lines, ctx)) }

// Changed counts added and removed lines.
func Changed(lines []Line) (added, removed int) {
	for _, l := range lines {
		switch l.Op {
		case '+':
			added++
		case '-':
			removed++
		}
	}
	return added, removed
}

func rows(lines []Line, ctx int) []row {
	keep := make([]bool, len(lines))
	changed := false
	for i, l := range lines {
		if l.Op == ' ' {
			continue
		}
		changed = true
		for j := max(0, i-ctx); j <= min(len(lines)-1, i+ctx); j++ {
			keep[j] = true
		}
	}
	if !changed {
		return nil
	}
	var out []row
	for i := 0; i < len(lines); {
		if !keep[i] {
			i++
			continue
		}
		j := i
		var oldN, newN, oldS, newS int
		for j < len(lines) && keep[j] {
			l := lines[j]
			if l.Op != '+' {
				oldN++
				if oldS == 0 {
					oldS = l.Old
				}
			}
			if l.Op != '-' {
				newN++
				if newS == 0 {
					newS = l.New
				}
			}
			j++
		}
		out = append(out, row{header: fmt.Sprintf("@@ -%d,%d +%d,%d @@", oldS, oldN, newS, newN)})
		for k := i; k < j; k++ {
			out = append(out, row{line: lines[k]})
		}
		i = j
	}
	return out
}

// Options control a render.
type Options struct {
	Offset  int // first visible row
	Context int // context lines; <0 shows the whole file, 0 uses DefaultContext
}

// Render draws the diff at exactly w x h.
func Render(s *styles.Styles, lines []Line, w, h int, o Options) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	ctx := o.Context
	if ctx == 0 {
		ctx = DefaultContext
	}
	if ctx < 0 {
		ctx = len(lines)
	}
	rs := rows(lines, ctx)
	if len(rs) == 0 {
		return pane.Fit(s.Muted.Render("No changes."), w) + strings.Repeat("\n"+strings.Repeat(" ", w), h-1)
	}
	numW := 1
	for _, l := range lines {
		numW = max(numW, len(fmt.Sprint(max(l.Old, l.New))))
	}
	num := func(n int) string {
		if n == 0 {
			return strings.Repeat(" ", numW)
		}
		return fmt.Sprintf("%*d", numW, n)
	}
	off := max(0, min(o.Offset, len(rs)-h))
	out := make([]string, 0, h)
	for i := off; i < off+h; i++ {
		if i >= len(rs) {
			out = append(out, strings.Repeat(" ", w))
			continue
		}
		r := rs[i]
		if r.header != "" {
			out = append(out, pane.Fit(s.Accent.Render(r.header), w))
			continue
		}
		l := r.line
		gutter := s.Muted.Render(num(l.Old) + " " + num(l.New) + " ")
		text := strings.ReplaceAll(l.Text, "\t", "    ")
		var body string
		switch l.Op {
		case '+':
			body = s.OK.Render("+" + text)
		case '-':
			body = s.Danger.Render("-" + text)
		default:
			body = s.Text.Render(" " + text)
		}
		out = append(out, pane.Fit(gutter+body, w))
	}
	return strings.Join(out, "\n")
}

// Summary is "+3 -1" style text for a diff.
func Summary(lines []Line) string {
	a, d := Changed(lines)
	return fmt.Sprintf("+%d −%d", a, d)
}
