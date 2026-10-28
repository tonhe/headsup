package files

import (
	"strings"

	"github.com/sergi/go-diff/diffmatchpatch"
	"github.com/tonhe/fathom/internal/host"
)

// Diff returns a line diff from old to next, with 1-based line numbers on
// each side. Unchanged lines are included so callers can render context.
func Diff(old, next []byte) []host.DiffLine {
	dmp := diffmatchpatch.New()
	a, b, lines := dmp.DiffLinesToRunes(string(old), string(next))
	diffs := dmp.DiffMainRunes(a, b, false)
	diffs = dmp.DiffCharsToLines(diffs, lines)

	var out []host.DiffLine
	oldN, newN := 0, 0
	for _, d := range diffs {
		for _, ln := range splitKeep(d.Text) {
			switch d.Type {
			case diffmatchpatch.DiffEqual:
				oldN++
				newN++
				out = append(out, host.DiffLine{Op: ' ', Text: ln, Old: oldN, New: newN})
			case diffmatchpatch.DiffDelete:
				oldN++
				out = append(out, host.DiffLine{Op: '-', Text: ln, Old: oldN})
			case diffmatchpatch.DiffInsert:
				newN++
				out = append(out, host.DiffLine{Op: '+', Text: ln, New: newN})
			}
		}
	}
	return out
}

// Changed reports how many lines a diff adds or removes.
func Changed(d []host.DiffLine) (added, removed int) {
	for _, l := range d {
		switch l.Op {
		case '+':
			added++
		case '-':
			removed++
		}
	}
	return added, removed
}

// splitKeep splits s into lines without their trailing newline.
func splitKeep(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.TrimSuffix(s, "\n")
	return strings.Split(s, "\n")
}
