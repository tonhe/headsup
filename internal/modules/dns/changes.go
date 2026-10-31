package dns

import (
	"fmt"
	"strings"

	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/icons"
	"github.com/tonhe/headsup/internal/ui/badge"
	"github.com/tonhe/headsup/internal/ui/styles"
)

// effectChip is the badge saying when a change takes effect.
func effectChip(s *styles.Styles, e host.Effect) string {
	switch e {
	case host.EffectLive:
		return badge.Chip(s.OK, "live")
	case host.EffectReload:
		return badge.Chip(s.Pending, "on reload")
	}
	return badge.Chip(s.Warn, "needs restart")
}

func added(d []host.DiffLine) (a, r int) {
	for _, l := range d {
		switch l.Op {
		case '+':
			a++
		case '-':
			r++
		}
	}
	return a, r
}

func issueLine(s *styles.Styles, ic icons.Set, i host.Issue) string {
	st, g := s.Warn, ic.Warn
	if i.Severity == host.SevError {
		st, g = s.Danger, ic.Cross
	}
	msg := i.Message
	if i.Line > 0 {
		msg = fmt.Sprintf("line %d: %s", i.Line, msg)
	}
	return st.Render(g + " " + msg)
}

// renderChanges lays out a change set as lines: a header per file (path,
// +/- counts, effect), its validation issues, and its diff with ctx lines of
// context around each change (ctx < 0 shows every line).
func renderChanges(s *styles.Styles, ic icons.Set, cs []*host.Change, checks []host.Issue, ctx int) []string {
	var out []string
	for _, i := range checks {
		out = append(out, issueLine(s, ic, i))
	}
	for _, c := range cs {
		a, r := added(c.Diff)
		head := s.Strong.Render(c.File.Path) + "  " + s.OK.Render(fmt.Sprintf("+%d", a)) + " " + s.Danger.Render(fmt.Sprintf("-%d", r)) + "  " + effectChip(s, c.Effect)
		if c.File.New {
			head += " " + badge.Chip(s.Accent, "new file")
		}
		out = append(out, head)
		for _, i := range c.Issues {
			out = append(out, "  "+issueLine(s, ic, i))
		}
		show := make([]bool, len(c.Diff))
		for i, l := range c.Diff {
			if ctx < 0 {
				show[i] = true
				continue
			}
			if l.Op != ' ' {
				for j := max(0, i-ctx); j <= min(len(c.Diff)-1, i+ctx); j++ {
					show[j] = true
				}
			}
		}
		gap := false
		for i, l := range c.Diff {
			if !show[i] {
				gap = true
				continue
			}
			if gap {
				out = append(out, s.Muted.Render("  "+ic.Ellipsis))
				gap = false
			}
			n := l.New
			if l.Op == '-' {
				n = l.Old
			}
			text := fmt.Sprintf("%4d %c %s", n, l.Op, strings.ReplaceAll(l.Text, "\t", "  "))
			switch l.Op {
			case '+':
				text = s.OK.Render(text)
			case '-':
				text = s.Danger.Render(text)
			default:
				text = s.Muted.Render(text)
			}
			out = append(out, text)
		}
		out = append(out, "")
	}
	return out
}
