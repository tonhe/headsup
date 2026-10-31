package policy

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/tonhe/headsup/internal/api"
	"github.com/tonhe/headsup/internal/module"
	pol "github.com/tonhe/headsup/internal/policy"
	"github.com/tonhe/headsup/internal/ui/codeview"
	"github.com/tonhe/headsup/internal/ui/diffview"
	"github.com/tonhe/headsup/internal/ui/humanize"
	"github.com/tonhe/headsup/internal/ui/pane"
	"github.com/tonhe/headsup/internal/ui/styles"
)

const lang = "json" // HuJSON: chroma's JSON lexer handles // comments

func (m *Model) View(ctx module.Context, r module.Rect) string {
	s := ctx.Styles()
	if !m.loaded {
		msg := s.Muted.Render("Loading the policy…")
		if m.loadErr != nil {
			e := api.AsError(m.loadErr)
			msg = s.Danger.Render("Could not load the policy: "+e.Error()) + "\n\n" + s.Muted.Render(e.Hint)
		}
		return lipgloss.Place(r.W, r.H, lipgloss.Center, lipgloss.Center, msg)
	}
	head := m.header(ctx, r.W)
	banners := m.banners(ctx, r.W)
	h := max(3, r.H-1-len(banners))
	var body string
	switch m.mode {
	case modeEdit:
		body = m.editView(ctx, r.W, h)
	case modeInvalid:
		body = m.invalidView(ctx, r.W, h)
	case modeReview:
		body = m.reviewView(ctx, r.W, h)
	case modeDiff:
		lines := toView(pol.Diff(m.cur, m.draft))
		body = m.diffPane(s, "Unsaved changes", lines, r.W, h, "")
	case modeExplore:
		body = m.exploreView(ctx, r.W, h)
	case modeHistory:
		body = m.historyView(ctx, r.W, h)
	case modeFilter:
		body = m.filterView(ctx, r.W, h)
	default:
		body = m.codePane(s, "Policy", m.cur, r.W, h, codeview.Options{Cursor: m.cursor})
	}
	parts := append([]string{head}, banners...)
	return strings.Join(append(parts, body), "\n")
}

// header is the one-line summary above the pane.
func (m *Model) header(ctx module.Context, w int) string {
	s, ic := ctx.Styles(), ctx.Icons()
	var left []string
	if fileMode(ctx) && m.file != nil {
		left = append(left, s.Accent.Render(m.file.Path), "file mode · applied by reload")
	} else {
		src := "stored on the server"
		if !m.curAt.IsZero() {
			src += " · updated " + humanize.Ago(m.curAt, ctx.Now())
		}
		left = append(left, src)
	}
	if d := m.doc; d != nil {
		left = append(left, fmt.Sprintf("%d acls · %d grants · %d ssh · %d groups · %d tags · %d tests",
			len(d.ACLs), len(d.Grants), len(d.SSH), len(d.Groups), len(d.TagOwners), len(d.Tests)))
	}
	line := s.Muted.Render(strings.Join(left, " "+ic.Bullet+" "))
	if m.hasDraft {
		line += "  " + s.Warn.Render(ic.Warn+" unsaved edits")
	}
	if m.checking {
		line += "  " + s.Pending.Render(ic.Pending+" checking…")
	}
	return pane.Fit(line, w)
}

func (m *Model) banners(ctx module.Context, w int) []string {
	s, ic := ctx.Styles(), ctx.Icons()
	var out []string
	add := func(st lipgloss.Style, text string) {
		for _, l := range strings.Split(lipgloss.NewStyle().Width(w).Render(text), "\n") {
			out = append(out, pane.Fit(st.Render(l), w))
		}
	}
	if m.applied != nil {
		add(s.Danger, fmt.Sprintf("%s The policy file was written, but %s. Press u to roll back to the previous file.", ic.Cross, m.reloadErr))
	}
	if !fileMode(ctx) && (m.viewOnly != "" || !ctx.Backend().Capabilities().Has(api.CapPolicyWritable)) {
		add(s.Warn, ic.Lock+" View only. "+fileModeRemote)
	}
	if m.docErr != nil && m.mode == modeView {
		add(s.Danger, ic.Cross+" The live policy does not parse: "+m.docErr.Error())
	}
	if m.hasDraft && m.mode == modeView {
		add(s.Warn, ic.Warn+" You have unsaved edits: e continue · d diff · ctrl+s check and review · D discard.")
	}
	if m.loadErr != nil {
		add(s.Muted, "Could not refresh the policy: "+m.loadErr.Error())
	}
	return out
}

func (m *Model) codePane(s *styles.Styles, title, text string, w, h int, o codeview.Options) string {
	iw, ih := pane.Inner(w, h)
	m.code.Set(s, lang, text)
	m.offset = codeview.ClampOffset(m.offset, m.code.Lines(), ih)
	o.Offset = m.offset
	right := fmt.Sprintf("%d lines", m.code.Lines())
	return pane.Render(s, title, m.code.Render(s, iw, ih, o), w, h, true, pane.WithRight(right))
}

func (m *Model) diffPane(s *styles.Styles, title string, lines []diffview.Line, w, h int, top string) string {
	iw, ih := pane.Inner(w, h)
	var head []string
	if top != "" {
		head = strings.Split(top, "\n")
	}
	dh := max(1, ih-len(head))
	m.offset = max(0, min(m.offset, diffview.Rows(lines, diffview.DefaultContext)-dh))
	body := diffview.Render(s, lines, iw, dh, diffview.Options{Offset: m.offset})
	if len(head) > 0 {
		for i := range head {
			head[i] = pane.Fit(head[i], iw)
		}
		body = strings.Join(head, "\n") + "\n" + body
	}
	return pane.Render(s, title, body, w, h, true, pane.WithExtra(diffview.Summary(lines)))
}

func (m *Model) editView(ctx module.Context, w, h int) string {
	s, ic := ctx.Styles(), ctx.Icons()
	iw, ih := pane.Inner(w, h)
	var top []string
	if m.invalid != nil {
		top = append(top, s.Danger.Render(ic.Cross+" "+m.invalid.String()))
	}
	if lossyInline(m.baseRaw) {
		top = append(top, s.Warn.Render(ic.Warn+" This policy uses tabs or CRLF line endings; the inline editor stores spaces. Use E ($EDITOR) to keep them."))
	}
	for i := range top {
		top[i] = pane.Fit(top[i], iw)
	}
	m.ta.SetWidth(iw)
	m.ta.SetHeight(max(1, ih-len(top)))
	body := m.ta.View()
	if len(top) > 0 {
		body = strings.Join(top, "\n") + "\n" + body
	}
	return pane.Render(s, "Editing", body, w, h, true, pane.WithExtra("unsaved"),
		pane.WithBottom(s.Muted.Render("ctrl+s check and review · esc stop editing (keeps the draft)")))
}

func (m *Model) invalidView(ctx module.Context, w, h int) string {
	s, ic := ctx.Styles(), ctx.Icons()
	p := m.invalid
	if p == nil {
		p = &problem{msg: "invalid"}
	}
	src := map[string]string{"server": "the server", "local": "a local check", "policy check": "headscale policy check", "schema": "the schema check"}[p.source]
	if src == "" {
		src = p.source
	}
	msg := ic.Cross + " " + p.String()
	if src != "" {
		msg += "  (" + src + ")"
	}
	iw, ih := pane.Inner(w, h)
	top := pane.Fit(s.Danger.Render(msg), iw)
	m.code.Set(s, lang, m.text())
	m.offset = codeview.ClampOffset(m.offset, m.code.Lines(), ih-1)
	code := m.code.Render(s, iw, ih-1, codeview.Options{Offset: m.offset, ErrLine: p.line})
	return pane.Render(s, "Invalid policy", top+"\n"+code, w, h, true, pane.WithExtra("nothing was applied"),
		pane.WithBottom(s.Muted.Render("e fix inline · E $EDITOR · esc back (the draft is kept)")))
}

func (m *Model) reviewView(ctx module.Context, w, h int) string {
	s, ic := ctx.Styles(), ctx.Icons()
	rv := m.review
	var top []string
	switch {
	case rv.change != nil:
		top = append(top, s.OK.Render(ic.Check+" Validated with headscale policy check on a temporary copy."))
	case rv.local:
		top = append(top, s.Warn.Render(ic.Warn+" Checked locally only: this server cannot check policies, so policy tests were not run."))
	case rv.tests > 0:
		top = append(top, s.OK.Render(fmt.Sprintf("%s Validated by the server, including %d policy test(s).", ic.Check, rv.tests)))
	default:
		top = append(top, s.OK.Render(ic.Check+" Validated by the server."))
	}
	for _, wn := range rv.warnings {
		top = append(top, s.Warn.Render(ic.Warn+" "+wn))
	}
	if rv.conflict != "" {
		top = append(top, s.Warn.Render(ic.Warn+" "+rv.conflict))
	}
	top = append(top, "")
	return m.diffPane(s, "Review changes", toView(rv.lines), w, h, strings.Join(top, "\n")) + ""
}
