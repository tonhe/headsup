package policy

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tonhe/headsup/internal/module"
	pol "github.com/tonhe/headsup/internal/policy"
	"github.com/tonhe/headsup/internal/ui/diffview"
	"github.com/tonhe/headsup/internal/ui/humanize"
	"github.com/tonhe/headsup/internal/ui/pane"
)

// historyView lists locally kept revisions; the selected one is shown as a
// diff against the live policy, and u restores it through the normal apply
// flow (check, diff, confirm), so a rollback is never a blind write (T110).
type historyView struct {
	revs    []pol.Revision
	err     error
	cursor  int
	content string
	shown   string // sha256 of content
	loadErr error
}

type historyMsg struct {
	revs []pol.Revision
	err  error
}

type revisionMsg struct {
	rev     pol.Revision
	raw     string
	err     error
	restore bool
}

func (h *historyView) selected() (pol.Revision, bool) {
	if h.cursor >= 0 && h.cursor < len(h.revs) {
		return h.revs[h.cursor], true
	}
	return pol.Revision{}, false
}

func (m *Model) openHistory(ctx module.Context) tea.Cmd {
	m.back, m.mode = m.mode, modeHistory
	hist, prof := m.hist, profileKey(ctx)
	return func() tea.Msg {
		if hist == nil {
			return historyMsg{}
		}
		revs, err := hist.List(prof)
		return historyMsg{revs: revs, err: err}
	}
}

// preview loads the selected revision's content.
func (h *historyView) preview(hist *pol.History) tea.Cmd {
	r, ok := h.selected()
	if !ok || hist == nil || r.SHA256 == h.shown {
		return nil
	}
	return func() tea.Msg {
		raw, err := hist.Load(r)
		return revisionMsg{rev: r, raw: raw, err: err}
	}
}

func (m *Model) historyKey(k tea.KeyMsg) (tea.Cmd, bool) {
	switch k.String() {
	case "j", "down":
		m.hst.cursor = min(m.hst.cursor+1, max(0, len(m.hst.revs)-1))
	case "k", "up":
		m.hst.cursor = max(0, m.hst.cursor-1)
	default:
		return nil, false
	}
	return m.hst.preview(m.hist), true
}

func (m *Model) restore(module.Context) tea.Cmd {
	r, _ := m.hst.selected()
	hist := m.hist
	return func() tea.Msg {
		raw, err := hist.Load(r)
		return revisionMsg{rev: r, raw: raw, err: err, restore: true}
	}
}

func (m *Model) onRevision(ctx module.Context, msg revisionMsg) tea.Cmd {
	if !msg.restore {
		if cur, ok := m.hst.selected(); ok && cur.SHA256 == msg.rev.SHA256 {
			m.hst.content, m.hst.shown, m.hst.loadErr = msg.raw, msg.rev.SHA256, msg.err
		}
		return nil
	}
	if msg.err != nil {
		return result(module.ActionResult{ActionID: "policy.rollback", Err: msg.err})
	}
	m.beginDraft()
	m.setDraft(msg.raw)
	if !m.hasDraft {
		return ctx.Toast(module.ToastInfo, "That revision is the live policy already.")
	}
	m.mode = modeView
	return m.startCheck(ctx, purposeApply)
}

func (m *Model) historyView(ctx module.Context, w, h int) string {
	s, now := ctx.Styles(), ctx.Now()
	hv := &m.hst
	listH := min(max(5, len(hv.revs)+2), max(5, h/3))
	iw, ih := pane.Inner(w, listH)
	var rows []string
	switch {
	case hv.err != nil:
		rows = append(rows, s.Danger.Render("Could not read the history: "+hv.err.Error()))
	case len(hv.revs) == 0:
		rows = append(rows, s.Muted.Render("No revisions kept yet. Each policy seen or applied is kept here (last 50)."))
	}
	start := max(0, min(hv.cursor-ih+1, len(hv.revs)-ih))
	for i := start; i < min(len(hv.revs), start+ih); i++ {
		r := hv.revs[i]
		live := ""
		if r.SHA256 == pol.Sum(m.cur) {
			live = s.OK.Render(" live")
		}
		line := fmt.Sprintf("%s  %-16s %-10s %s%s", r.FetchedAt.Local().Format("2006-01-02 15:04"),
			humanize.Ago(r.FetchedAt, now), r.Source, r.SHA256[:min(12, len(r.SHA256))], live)
		if i == hv.cursor {
			line = s.RowSel.Render(pane.Fit(cursorMark(ctx.Icons())+line, iw))
		} else {
			line = "  " + line
		}
		rows = append(rows, line)
	}
	list := pane.Render(s, "History", strings.Join(rows, "\n"), w, listH, true,
		pane.WithRight(fmt.Sprintf("%d kept", len(hv.revs))))

	dh := h - listH
	var body, extra string
	dw, dih := pane.Inner(w, dh)
	switch r, ok := hv.selected(); {
	case !ok:
	case hv.loadErr != nil && hv.shown == r.SHA256:
		body = s.Danger.Render(hv.loadErr.Error())
	case hv.shown != r.SHA256:
		body = s.Muted.Render("Loading…")
	default:
		lines := toView(pol.Diff(m.cur, hv.content))
		extra = diffview.Summary(lines) + " vs live"
		body = diffview.Render(s, lines, dw, dih, diffview.Options{})
	}
	diff := pane.Render(s, "Revision against the live policy", body, w, dh, false, pane.WithExtra(extra),
		pane.WithBottom(s.Muted.Render("u restore (check, diff, confirm)")))
	return list + "\n" + diff
}

func toView(ls []pol.Line) []diffview.Line {
	out := make([]diffview.Line, len(ls))
	for i, l := range ls {
		out[i] = diffview.Line{Op: l.Op, Text: l.Text, Old: l.Old, New: l.New}
	}
	return out
}
