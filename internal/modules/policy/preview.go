package policy

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tonhe/headsup/internal/module"
	pol "github.com/tonhe/headsup/internal/policy"
	"github.com/tonhe/headsup/internal/ui/pane"
)

// previewer is the "who can reach what" view (FR-025): pick a user, tag, or
// machine on the left; what it can reach is worked out on the right from the
// live policy, or from the unsaved edits when there are any.
type previewer struct {
	subjects []pol.Subject
	cursor   int
	offset   int
	scroll   int
}

// previewDoc is the document the preview evaluates and what to call it.
func (m *Model) previewDoc() (*pol.Document, string) {
	if m.hasDraft {
		if d, err := pol.Parse([]byte(m.draft)); err == nil {
			return d, "your unsaved edits"
		}
	}
	return m.doc, "the live policy"
}

func machinesOf(ctx module.Context) []pol.Machine {
	ns := ctx.Store().Nodes
	out := make([]pol.Machine, 0, len(ns))
	for _, n := range ns {
		out = append(out, pol.Machine{ID: n.ID, Name: n.DisplayName(), User: n.User.Name, Tags: n.Tags, IPs: n.IPAddresses})
	}
	return out
}

func (m *Model) openPreview(ctx module.Context) {
	d, _ := m.previewDoc()
	subs := d.Subjects(machinesOf(ctx))
	keep := ""
	if p := &m.prev; p.cursor < len(p.subjects) {
		keep = p.subjects[p.cursor].Kind.String() + p.subjects[p.cursor].Label()
	}
	m.prev = previewer{subjects: subs}
	for i, s := range subs {
		if s.Kind.String()+s.Label() == keep {
			m.prev.cursor = i
		}
	}
	m.back, m.mode = m.mode, modePreview
}

func (m *Model) previewAction() module.Action {
	return module.Action{
		ID: "policy.preview", Title: "Access preview (who can reach what)", Keys: []string{"p"},
		Enabled: func(ctx module.Context) (bool, string) {
			if ok, why := m.loadedGate(ctx); !ok {
				return false, why
			}
			if d, _ := m.previewDoc(); d == nil {
				return false, "The policy does not parse, so access cannot be worked out."
			}
			return true, ""
		},
		Run: func(ctx module.Context, _ module.Values) tea.Cmd { m.openPreview(ctx); return nil },
	}
}

func (m *Model) previewKey(ctx module.Context, k tea.KeyMsg) (tea.Cmd, bool) {
	p := &m.prev
	switch k.String() {
	case "j", "down":
		p.cursor, p.scroll = min(len(p.subjects)-1, p.cursor+1), 0
	case "k", "up":
		p.cursor, p.scroll = max(0, p.cursor-1), 0
	case "ctrl+d", "pgdown":
		p.scroll += 10
	case "ctrl+u", "pgup":
		p.scroll = max(0, p.scroll-10)
	case "m":
		s, ok := p.selected()
		if !ok {
			return nil, true
		}
		switch s.Kind {
		case pol.SubjectTag:
			return ctx.Navigate("machines", module.FilterArgs{Query: s.Name}), true
		case pol.SubjectUser:
			return ctx.Navigate("machines", module.FilterArgs{Query: "user:" + strings.TrimSuffix(s.Name, "@")}), true
		}
		return ctx.Navigate("machines", module.SelectArgs{ID: s.ID}), true
	default:
		return nil, false
	}
	return nil, true
}

func (p *previewer) selected() (pol.Subject, bool) {
	if p.cursor >= 0 && p.cursor < len(p.subjects) {
		return p.subjects[p.cursor], true
	}
	return pol.Subject{}, false
}

func (m *Model) previewView(ctx module.Context, w, h int) string {
	s, ic := ctx.Styles(), ctx.Icons()
	p := &m.prev
	side := w >= 100
	listW, listH := w, h
	if side {
		listW = min(36, w/3)
	} else {
		listH = max(6, h*2/5)
	}
	iw, ih := pane.Inner(listW, listH)
	if p.cursor < p.offset {
		p.offset = p.cursor
	}
	if p.cursor >= p.offset+ih {
		p.offset = p.cursor - ih + 1
	}
	var rows []string
	for i := p.offset; i < min(len(p.subjects), p.offset+ih); i++ {
		sub := p.subjects[i]
		label := fmt.Sprintf("%-8s %s", sub.Kind, sub.Label())
		if i == p.cursor {
			rows = append(rows, s.RowSel.Render(pane.Fit(" "+label, iw)))
		} else {
			rows = append(rows, " "+s.Muted.Render(fmt.Sprintf("%-8s", sub.Kind))+" "+sub.Label())
		}
	}
	if len(p.subjects) == 0 {
		rows = append(rows, s.Muted.Render("No users, tags, or machines."))
	}
	list := pane.Render(s, "Source", strings.Join(rows, "\n"), listW, listH, true,
		pane.WithRight(fmt.Sprintf("%d", len(p.subjects))))

	dw, dh := w, h-listH
	if side {
		dw, dh = w-listW, h
	}
	dIW, dIH := pane.Inner(dw, dh)
	d, under := m.previewDoc()
	title, lines := "Can reach", []string{s.Muted.Render("Pick a source on the left.")}
	if sub, ok := p.selected(); ok {
		pr := d.Preview(machinesOf(ctx), sub)
		title = sub.Label() + " can reach"
		lines = previewLines(ctx, pr, under, dIW)
	}
	if max(0, len(lines)-dIH) < p.scroll {
		p.scroll = max(0, len(lines)-dIH)
	}
	lines = lines[p.scroll:]
	if len(lines) > dIH {
		lines = lines[:dIH]
	}
	detail := pane.Render(s, title, strings.Join(lines, "\n"), dw, dh, false,
		pane.WithRight(ic.Warn+" best-effort"))
	if side {
		return lipgloss.JoinHorizontal(lipgloss.Top, list, detail)
	}
	return lipgloss.JoinVertical(lipgloss.Left, list, detail)
}

func previewLines(ctx module.Context, pr pol.Preview, under string, w int) []string {
	s := ctx.Styles()
	var from []string
	for _, f := range pr.From {
		from = append(from, f.Name)
	}
	wrap := func(t string) []string { return strings.Split(s.Muted.Width(max(1, w)).Render(t), "\n") }
	out := wrap("Under " + under + ". " + pol.PreviewNote)
	switch {
	case pr.Subject.Kind == pol.SubjectMachine:
	case len(from) == 0:
		out = append(out, wrap("No machines are "+pr.Subject.Label()+" right now; the rules apply when one joins.")...)
	default:
		out = append(out, wrap(fmt.Sprintf("Stands for %d machine(s): %s", len(from), strings.Join(from, ", ")))...)
	}
	out = append(out, "")
	if len(pr.Access) == 0 {
		out = append(out, s.Warn.Render("Nothing: no accept rule or grant matches this source."))
	}
	destW := 26
	for _, a := range pr.Access {
		dest := a.Dest
		switch a.Kind {
		case pol.DestAddress:
			dest += " (address)"
		case pol.DestAny:
			dest = "* (any address, incl. subnets)"
		case pol.DestInternet:
			dest = "internet (exit nodes)"
		}
		var ls []string
		for _, l := range a.Lines {
			ls = append(ls, fmt.Sprint(l))
		}
		row := fmt.Sprintf("%-*s %s  %s", destW, pane.Fit(dest, destW), s.Accent.Render(strings.Join(a.Ports, ", ")),
			s.Muted.Render("line "+strings.Join(ls, ",")))
		out = append(out, pane.Fit(row, w))
	}
	if len(pr.Notes) > 0 {
		out = append(out, "", s.Muted.Render("Not evaluated:"))
		for _, n := range pr.Notes {
			out = append(out, wrap("  "+n)...)
		}
	}
	return out
}
