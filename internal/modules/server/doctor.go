package server

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tonhe/headsup/internal/clip"
	"github.com/tonhe/headsup/internal/host/doctor"
	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/ui/pane"
	"github.com/tonhe/headsup/internal/ui/styles"
	"github.com/tonhe/headsup/internal/ui/table"
)

type doctorMsg struct {
	findings []doctor.Finding
	full     bool
	explicit bool
}

func bg() context.Context { return context.Background() }

// runDoctor runs every check in a command. full includes the clock check,
// which contacts an outside server.
func (m *Model) runDoctor(ctx module.Context, full bool) tea.Cmd {
	h := ctx.Host()
	if h == nil {
		return nil
	}
	var p doctor.Probes
	switch {
	case m.probes != nil:
		p = *m.probes
	case simulated(ctx):
		p = doctor.SimulatedProbes(h, ctx.Now)
	default:
		p = doctor.OSProbes()
	}
	if !full {
		p.ClockURL = ""
	}
	th := doctor.FromConfig(m.hostSettings(ctx).Doctor)
	return func() tea.Msg {
		c, cancel := context.WithTimeout(bg(), 30*time.Second)
		defer cancel()
		return doctorMsg{findings: doctor.Run(c, h, p, th), full: full, explicit: full}
	}
}

func (m *Model) onDoctor(msg doctorMsg) tea.Cmd {
	m.findings, m.docRan, m.docBusy, m.docFull = msg.findings, true, false, msg.full
	rows := make([]table.Row, len(msg.findings))
	for i, f := range msg.findings {
		rows[i] = findingRow{f: f, i: i}
	}
	m.doc.SetRows(rows)
	if !msg.explicit {
		return nil
	}
	w, e := doctor.Counts(msg.findings)
	text := "Doctor: no problems found."
	if w+e > 0 {
		text = fmt.Sprintf("Doctor: %s.", countText(w, e))
	}
	return func() tea.Msg { return module.ActionResult{ActionID: "server.doctor", Message: text} }
}

func countText(w, e int) string {
	var parts []string
	if e > 0 {
		parts = append(parts, plural(e, "error"))
	}
	if w > 0 {
		parts = append(parts, plural(w, "warning"))
	}
	return strings.Join(parts, ", ")
}

func plural(n int, s string) string {
	if n == 1 {
		return "1 " + s
	}
	return fmt.Sprintf("%d %ss", n, s)
}

func (m *Model) finding() (doctor.Finding, bool) {
	if m.doc == nil {
		return doctor.Finding{}, false
	}
	r, ok := m.doc.Cursor()
	if !ok {
		return doctor.Finding{}, false
	}
	return r.(findingRow).f, true
}

func copyCmd(id, text string) tea.Cmd {
	return func() tea.Msg {
		method, err := clip.Copy(text)
		msg := "Copied the fix command"
		if method == clip.MethodOSC52 {
			msg += " (via terminal)"
		}
		return module.ActionResult{ActionID: id, Err: err, Message: msg + "."}
	}
}

type findingRow struct {
	f   doctor.Finding
	i   int
	sev string // styled severity; plain name when empty
}

func (r findingRow) Key() string { return fmt.Sprintf("%03d %s", r.i, r.f.ID) }
func (r findingRow) Cell(col string) string {
	switch col {
	case "sev":
		if r.sev != "" {
			return r.sev
		}
		return r.f.Severity.String()
	case "check":
		return r.f.Check
	case "title":
		return r.f.Title
	}
	return ""
}
func (r findingRow) SortValue(col string) string {
	if col == "sev" {
		return fmt.Sprint(9 - int(r.f.Severity))
	}
	return r.Cell(col)
}

func doctorColumns() []table.Column {
	return []table.Column{
		{Key: "sev", Title: "", Width: 7},
		{Key: "check", Title: "Check", Width: 16, Priority: 1},
		{Key: "title", Title: "Finding", Width: 40, MinWidth: 20, Flex: true},
	}
}

func sevText(s *styles.Styles, glyphs struct{ ok, warn, err, skip string }, sev doctor.Severity) string {
	switch sev {
	case doctor.SevError:
		return s.Danger.Render(glyphs.err + " error")
	case doctor.SevWarn:
		return s.Warn.Render(glyphs.warn + " warn")
	case doctor.SevInfo:
		return s.Muted.Render(glyphs.skip + " info")
	case doctor.SevSkipped:
		return s.Muted.Render("- skip")
	}
	return s.OK.Render(glyphs.ok + " ok")
}

func (m *Model) doctorView(ctx module.Context, w, h int) string {
	s, ic := ctx.Styles(), ctx.Icons()
	if ctx.Host() == nil {
		return hostView(ctx, "Doctor", "The doctor", w, h)
	}
	g := struct{ ok, warn, err, skip string }{ic.Check, ic.Warn, ic.Cross, ic.Bullet}
	rows := make([]table.Row, len(m.findings))
	for i, f := range m.findings {
		rows[i] = findingRow{f: f, i: i, sev: sevText(s, g, f.Severity)}
	}
	m.doc.SetRows(rows)

	detailH := min(8, max(4, h/3))
	listH := h - detailH
	m.doc.SetSize(max(0, w-2), max(0, listH-2))
	extra := ""
	switch {
	case m.docBusy:
		extra = s.Muted.Render("running" + ic.Ellipsis)
	case m.docRan:
		we, ee := doctor.Counts(m.findings)
		sum := "no problems"
		if we+ee > 0 {
			sum = countText(we, ee)
		}
		extra = s.Muted.Render(sum)
		if !m.docFull {
			extra += s.Muted.Render(" " + ic.Bullet + " c for a full run")
		}
	}
	var list string
	if !m.docRan {
		iw, _ := pane.Inner(w, listH)
		list = pane.Render(s, "Doctor", pane.Fit(s.Muted.Render("Checking"+ic.Ellipsis), iw), w, listH, true, pane.WithExtra(extra))
	} else {
		list = pane.Render(s, "Doctor", m.doc.View(), w, listH, true, pane.NoPad(), pane.WithExtra(extra))
	}

	dw, dh := pane.Inner(w, detailH)
	var lines []string
	if r, ok := m.doc.Cursor(); ok {
		f := r.(findingRow).f
		lines = append(lines, pane.Fit(s.Strong.Render(f.Title), dw))
		for _, l := range wrap(f.Detail, dw) {
			lines = append(lines, pane.Fit(s.Text.Render(l), dw))
		}
		if f.Fix != "" {
			for i, l := range wrap(f.Fix, dw-5) {
				lead := "     "
				if i == 0 {
					lead = s.Label.Render("Fix: ")
				}
				lines = append(lines, pane.Fit(lead+s.Accent.Render(l), dw))
			}
		}
		if f.Action != "" {
			lines = append(lines, pane.Fit(s.Muted.Render("f opens the fix in the app"), dw))
		}
	}
	if len(lines) > dh {
		lines = lines[:dh]
	}
	return list + "\n" + pane.Render(s, "Finding", strings.Join(lines, "\n"), w, detailH, false)
}
