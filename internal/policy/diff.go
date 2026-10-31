package policy

import (
	"fmt"
	"strings"

	"github.com/sergi/go-diff/diffmatchpatch"
)

// Line is one line of a line diff. Old and New are 1-based line numbers on
// each side; 0 means the line does not exist on that side.
type Line struct {
	Op   byte // ' ', '+', '-'
	Text string
	Old  int
	New  int
}

// Hunk is a run of changes with surrounding context, as in a unified diff.
type Hunk struct {
	OldStart, OldLines int
	NewStart, NewLines int
	Lines              []Line
}

// Header is the unified-diff hunk header, e.g. "@@ -3,6 +3,7 @@".
func (h Hunk) Header() string {
	return fmt.Sprintf("@@ -%d,%d +%d,%d @@", h.OldStart, h.OldLines, h.NewStart, h.NewLines)
}

// Diff returns a full line diff from old to next, unchanged lines included.
func Diff(old, next string) []Line {
	dmp := diffmatchpatch.New()
	a, b, lines := dmp.DiffLinesToRunes(old, next)
	diffs := dmp.DiffCharsToLines(dmp.DiffMainRunes(a, b, false), lines)

	var out []Line
	o, n := 0, 0
	for _, d := range diffs {
		for _, ln := range splitLines(d.Text) {
			switch d.Type {
			case diffmatchpatch.DiffEqual:
				o++
				n++
				out = append(out, Line{Op: ' ', Text: ln, Old: o, New: n})
			case diffmatchpatch.DiffDelete:
				o++
				out = append(out, Line{Op: '-', Text: ln, Old: o})
			case diffmatchpatch.DiffInsert:
				n++
				out = append(out, Line{Op: '+', Text: ln, New: n})
			}
		}
	}
	return out
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

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

// Hunks groups a full diff into hunks with ctx lines of context. Changes
// closer than 2*ctx lines share a hunk.
func Hunks(lines []Line, ctx int) []Hunk {
	var out []Hunk
	n := len(lines)
	i := 0
	for i < n {
		if lines[i].Op == ' ' {
			i++
			continue
		}
		start := max(0, i-ctx)
		end := i
		for j := i; j < n; j++ {
			if lines[j].Op != ' ' {
				end = j
				continue
			}
			if j-end > 2*ctx {
				break
			}
		}
		stop := min(n, end+ctx+1)
		h := Hunk{Lines: append([]Line(nil), lines[start:stop]...)}
		for _, l := range h.Lines {
			if l.Op != '+' {
				h.OldLines++
				if h.OldStart == 0 {
					h.OldStart = l.Old
				}
			}
			if l.Op != '-' {
				h.NewLines++
				if h.NewStart == 0 {
					h.NewStart = l.New
				}
			}
		}
		// A pure insertion at the top has no old line yet: point at the line before.
		if h.OldStart == 0 && start > 0 {
			h.OldStart = lines[start-1].Old
		}
		if h.NewStart == 0 && start > 0 {
			h.NewStart = lines[start-1].New
		}
		out = append(out, h)
		i = stop
	}
	return out
}

// Unified renders a unified diff with ctx lines of context.
func Unified(oldName, newName string, lines []Line, ctx int) string {
	hs := Hunks(lines, ctx)
	if len(hs) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "--- %s\n+++ %s\n", oldName, newName)
	for _, h := range hs {
		b.WriteString(h.Header() + "\n")
		for _, l := range h.Lines {
			b.WriteByte(l.Op)
			b.WriteString(l.Text + "\n")
		}
	}
	return b.String()
}
