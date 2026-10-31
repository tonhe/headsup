// Package dns is the DNS page (host mode): MagicDNS, base domain, local DNS
// override, global and split nameservers, search domains, and the extra
// records table. Edits are staged, validated and diffed, then written as one
// confirmed change set through the managed-file pipeline. Records kept in
// the extra-records file apply live; every other DNS setting needs a
// headscale restart, and the view says which is which.
package dns

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/host/changeset"
	"github.com/tonhe/headsup/internal/host/dnsrec"
	"github.com/tonhe/headsup/internal/host/hsconfig"
	"github.com/tonhe/headsup/internal/icons"
	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/modules/registry"
	"github.com/tonhe/headsup/internal/store"
	"github.com/tonhe/headsup/internal/ui/empty"
	"github.com/tonhe/headsup/internal/ui/pane"
	"github.com/tonhe/headsup/internal/ui/table"
)

func init() { registry.Register(func() module.Module { return New() }) }

// ioTimeout bounds every host call the module makes.
const ioTimeout = 30 * time.Second

type focus int

const (
	focusSettings focus = iota
	focusRecords
)

type rowKind int

const (
	rowMagic rowKind = iota
	rowBase
	rowOverride
	rowGlobal
	rowSplit
	rowSearch
	rowRecordsFile
)

type row struct {
	kind   rowKind
	label  string
	value  string
	domain string // rowSplit
}

// Model is the DNS module.
type Model struct {
	loaded  bool
	loadErr error

	cfg      *host.File
	doc      *hsconfig.Doc
	recF     *host.File   // the records file (nil when records are inline or none)
	recs     *dnsrec.File // its working copy
	recErr   error        // the configured records file could not be read
	recDirty bool

	focus  focus
	row    int
	rec    int
	rows   []row
	checks []host.Issue

	changes  []*host.Change
	planSeq  int
	planning bool
	planErr  error

	diff    bool
	diffTop int

	applied   []host.Applied
	restartAt time.Time // a restart is needed for changes written at this time
	serverURL string
	lastH     int
}

// New returns the module.
func New() *Model { return &Model{} }

func (m *Model) ID() string               { return "dns" }
func (m *Model) Title() string            { return "DNS" }
func (m *Model) Icon(ic icons.Set) string { return ic.DNS }
func (m *Model) Order() int               { return 112 }
func (m *Model) Modes() module.Modes      { return module.ModeHost }
func (m *Model) Chord() string            { return "d" }
func (m *Model) Capturing() bool          { return false }

func (m *Model) Badge(*store.Store) string {
	if !m.restartAt.IsZero() {
		return "restart"
	}
	return ""
}

func (m *Model) Init(ctx module.Context) tea.Cmd {
	if ctx.Host() == nil {
		return nil
	}
	return m.load(ctx)
}

// --- loading ------------------------------------------------------------------

type loadedMsg struct {
	cfg    *host.File
	doc    *hsconfig.Doc
	recF   *host.File
	recs   *dnsrec.File
	recErr error
	err    error
}

func (m *Model) load(ctx module.Context) tea.Cmd {
	h := ctx.Host()
	return func() tea.Msg { return loadFiles(h) }
}

func loadFiles(h host.Host) loadedMsg {
	env := h.Env()
	cfg, err := h.Open(host.FileConfig, env.ConfigPath)
	if err != nil {
		return loadedMsg{err: err}
	}
	doc, err := hsconfig.Parse(cfg.Raw)
	if err != nil {
		return loadedMsg{err: fmt.Errorf("%s: %w", cfg.Path, err)}
	}
	msg := loadedMsg{cfg: cfg, doc: doc}
	if p := dnsrec.FromDoc(doc).ExtraRecordsPath; p != "" {
		f, err := h.Open(host.FileExtraRecords, p)
		if err != nil {
			msg.recErr = err
			return msg
		}
		recs, err := dnsrec.ParseFile(p, f.Raw)
		if err != nil {
			msg.recErr = err
			return msg
		}
		msg.recF, msg.recs = f, recs
	}
	return msg
}

func (m *Model) dirty() bool { return len(m.items()) > 0 }

// Unsaved implements module.Unsaved: staged edits not yet applied.
func (m *Model) Unsaved() bool { return m.dirty() }

// items is the pending change set: the records file first (it may be new,
// and the config may point at it), then the config.
func (m *Model) items() []changeset.Item {
	var out []changeset.Item
	if m.recF != nil && m.recs != nil && m.recDirty {
		if b, err := m.recs.Bytes(); err == nil && (m.recF.New || string(b) != string(m.recF.Raw)) {
			out = append(out, changeset.Item{File: m.recF, Next: b})
		}
	}
	if m.cfg != nil && m.doc != nil {
		if b := m.doc.Bytes(); string(b) != string(m.cfg.Raw) {
			out = append(out, changeset.Item{File: m.cfg, Next: b})
		}
	}
	return out
}

type planMsg struct {
	seq     int
	changes []*host.Change
	err     error
}

// staged recomputes the view model and the checks, then re-plans the
// change set (validation and diff run off the UI goroutine).
func (m *Model) staged(ctx module.Context) tea.Cmd {
	m.rebuild()
	m.planSeq++
	seq := m.planSeq
	items := m.items()
	if len(items) == 0 {
		m.changes, m.planning, m.planErr = nil, false, nil
		return nil
	}
	m.planning = true
	h := ctx.Host()
	return func() tea.Msg {
		c, cancel := context.WithTimeout(context.Background(), ioTimeout)
		defer cancel()
		cs, err := changeset.Plan(c, h, items)
		return planMsg{seq: seq, changes: cs, err: err}
	}
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func orNone(s, none string) string {
	if s == "" {
		return none
	}
	return s
}

func listOr(xs []string, none string) string {
	if len(xs) == 0 {
		return none
	}
	return strings.Join(xs, ", ")
}

func (m *Model) config() dnsrec.Config {
	if m.doc == nil {
		return dnsrec.Config{}
	}
	return dnsrec.FromDoc(m.doc)
}

// records returns the records being edited and whether they live in a file.
func (m *Model) records() ([]dnsrec.Record, bool) {
	if m.recs != nil {
		return m.recs.Records, true
	}
	return m.config().ExtraRecords, false
}

func (m *Model) rebuild() {
	if m.doc == nil {
		m.rows, m.checks = nil, nil
		return
	}
	c := m.config()
	m.rows = []row{
		{kind: rowMagic, label: "MagicDNS", value: onOff(c.MagicDNS)},
		{kind: rowBase, label: "Base domain", value: orNone(c.BaseDomain, "(not set)")},
		{kind: rowOverride, label: "Override local DNS", value: onOff(c.OverrideLocalDNS)},
		{kind: rowGlobal, label: "Global nameservers", value: listOr(c.Global, "(none)")},
	}
	if len(c.SplitOrder) == 0 {
		m.rows = append(m.rows, row{kind: rowSplit, label: "Split DNS", value: "(none; n adds a domain)"})
	}
	for _, d := range c.SplitOrder {
		m.rows = append(m.rows, row{kind: rowSplit, label: "Split DNS", domain: d, value: d + " → " + listOr(c.Split[d], "(no servers)")})
	}
	m.rows = append(m.rows, row{kind: rowSearch, label: "Search domains", value: listOr(c.Search, "(none)")})
	rf := "inline in " + m.cfg.Path
	switch {
	case c.ExtraRecordsPath != "" && m.recs != nil:
		rf = c.ExtraRecordsPath + " (" + m.recs.Format.String() + ")"
	case c.ExtraRecordsPath != "":
		rf = c.ExtraRecordsPath + " (unreadable)"
	case !c.HasInline():
		rf = "(none; M creates a file)"
	}
	m.rows = append(m.rows, row{kind: rowRecordsFile, label: "Records file", value: rf})
	m.row = min(m.row, len(m.rows)-1)
	recs, _ := m.records()
	m.rec = max(0, min(m.rec, len(recs)-1))

	// Checks headscale would make at startup that the config schema does
	// not cover (it already checks base_domain vs server_url and the
	// inline/path exclusivity).
	m.checks = nil
	excl := dnsrec.CheckExclusive(true, "x").Error()
	for _, i := range c.Validate("") {
		if i.Message != excl {
			m.checks = append(m.checks, i)
		}
	}
	if m.recs != nil {
		m.checks = append(m.checks, dnsrec.ValidateRecords(m.recs.Records)...)
	}
}

// --- update -------------------------------------------------------------------

type appliedMsg struct {
	applied []host.Applied
	effect  host.Effect
	err     error
	changes []*host.Change
}

type rolledBackMsg struct{ err error }

type serviceMsg struct{ since time.Time }

func (m *Model) Update(ctx module.Context, msg tea.Msg) (module.Module, tea.Cmd) {
	switch msg := msg.(type) {
	case loadedMsg:
		m.loaded, m.loadErr = true, msg.err
		m.cfg, m.doc, m.recF, m.recs, m.recErr, m.recDirty = msg.cfg, msg.doc, msg.recF, msg.recs, msg.recErr, false
		m.changes, m.planErr, m.planning, m.diff = nil, nil, false, false
		m.planSeq++
		m.rebuild()
		return m, nil
	case planMsg:
		if msg.seq == m.planSeq {
			m.changes, m.planErr, m.planning = msg.changes, msg.err, false
		}
		return m, nil
	case appliedMsg:
		res := module.ActionResult{ActionID: "dns.apply", Err: msg.err}
		if msg.err != nil {
			var cod *host.ErrChangedOnDisk
			if errors.As(msg.err, &cod) {
				res.Err = fmt.Errorf("%w. Press x to reload it (your staged edits are discarded), then make them again", msg.err)
			}
			return m, func() tea.Msg { return res }
		}
		m.applied = msg.applied
		res.Files = module.WritesOf(msg.changes, msg.applied)
		res.Message = "Saved. Records apply now."
		if msg.effect == host.EffectRestart {
			m.restartAt = msg.applied[len(msg.applied)-1].At
			res.Message = "Saved. Restart headscale to apply the DNS settings (g l)."
		}
		return m, tea.Batch(m.load(ctx), func() tea.Msg { return res })
	case rolledBackMsg:
		res := module.ActionResult{ActionID: "dns.rollback", Err: msg.err, Message: "Rolled back the last DNS change."}
		if msg.err == nil {
			m.applied = nil
		}
		return m, tea.Batch(m.load(ctx), func() tea.Msg { return res })
	case moveCheckedMsg:
		return m, m.finishMove(ctx, msg)
	case serviceMsg:
		if !msg.since.IsZero() && msg.since.After(m.restartAt) {
			m.restartAt = time.Time{}
		}
		return m, nil
	case store.UpdatedMsg:
		if !m.restartAt.IsZero() && ctx.Host() != nil {
			return m, m.checkRestart(ctx)
		}
		return m, nil
	case module.NavigateMsg:
		if ctx.Host() != nil && !m.dirty() {
			return m, m.load(ctx)
		}
		return m, nil
	case tea.KeyMsg:
		return m, m.key(msg)
	}
	return m, nil
}

func (m *Model) checkRestart(ctx module.Context) tea.Cmd {
	svc := ctx.Host().Service()
	if svc == nil {
		return nil
	}
	return func() tea.Msg {
		c, cancel := context.WithTimeout(context.Background(), ioTimeout)
		defer cancel()
		st, err := svc.Status(c)
		if err != nil || !st.Running {
			return serviceMsg{}
		}
		return serviceMsg{since: st.Since}
	}
}

func (m *Model) key(k tea.KeyMsg) tea.Cmd {
	if m.diff {
		switch k.String() {
		case "esc", "q":
			m.diff = false
		case "j", "down":
			m.diffTop++
		case "k", "up":
			m.diffTop = max(0, m.diffTop-1)
		case "pgdown", " ":
			m.diffTop += max(1, m.lastH-4)
		case "pgup":
			m.diffTop = max(0, m.diffTop-max(1, m.lastH-4))
		case "home":
			m.diffTop = 0
		}
		return nil
	}
	recs, _ := m.records()
	switch k.String() {
	case "tab", "shift+tab", "left", "right", "h", "l":
		if m.focus == focusSettings {
			m.focus = focusRecords
		} else {
			m.focus = focusSettings
		}
	case "j", "down":
		if m.focus == focusSettings {
			m.row = min(m.row+1, max(0, len(m.rows)-1))
		} else {
			m.rec = min(m.rec+1, max(0, len(recs)-1))
		}
	case "k", "up":
		if m.focus == focusSettings {
			m.row = max(m.row-1, 0)
		} else {
			m.rec = max(m.rec-1, 0)
		}
	case "home":
		m.row, m.rec = 0, 0
	case "end":
		m.row, m.rec = max(0, len(m.rows)-1), max(0, len(recs)-1)
	}
	return nil
}

func (m *Model) Hints() []module.Hint {
	switch {
	case m.diff:
		return []module.Hint{{Key: "j/k", Desc: "scroll"}, {Key: "esc", Desc: "close diff"}, {Key: "ctrl+s", Desc: "apply"}}
	case m.focus == focusRecords:
		h := []module.Hint{{Key: "tab", Desc: "settings"}, {Key: "n", Desc: "add record"}, {Key: "enter", Desc: "edit"}, {Key: "D", Desc: "delete"}}
		return append(h, m.applyHints()...)
	}
	h := []module.Hint{{Key: "tab", Desc: "records"}, {Key: "enter", Desc: "edit"}, {Key: "n", Desc: "add split domain"}}
	if rw, ok := m.currentRow(); ok && rw.kind == rowSplit && rw.domain != "" {
		h = append(h, module.Hint{Key: "D", Desc: "remove domain"})
	}
	return append(h, m.applyHints()...)
}

func (m *Model) applyHints() []module.Hint {
	if m.dirty() {
		return []module.Hint{{Key: "d", Desc: "diff"}, {Key: "ctrl+s", Desc: "apply"}, {Key: "x", Desc: "discard"}}
	}
	if len(m.applied) > 0 {
		return []module.Hint{{Key: "u", Desc: "roll back"}}
	}
	return nil
}

// --- view ---------------------------------------------------------------------

func (m *Model) View(ctx module.Context, r module.Rect) string {
	s, ic := ctx.Styles(), ctx.Icons()
	m.lastH = r.H
	switch {
	case ctx.Host() == nil:
		return empty.Render(s, "DNS needs host mode", "Run on the headscale server to edit DNS settings.", "", r.W, r.H)
	case !m.loaded:
		return empty.Render(s, "Loading DNS settings…", "", "", r.W, r.H)
	case m.loadErr != nil:
		return empty.Render(s, "Could not read the config", m.loadErr.Error(), "", r.W, r.H)
	}
	if m.diff {
		return m.diffView(ctx, r)
	}
	pending := m.dirty() || m.planErr != nil
	setH := min(len(m.rows)+2, max(5, r.H/2))
	pendH := 0
	if pending {
		pendH = min(max(6, r.H/3), r.H-setH-5)
		pendH = max(pendH, 0)
	}
	recH := max(3, r.H-setH-pendH)

	iw := r.W - 4
	settings := pane.Render(s, "DNS settings", m.settingsBody(ctx, iw, setH-2), r.W, setH, m.focus == focusSettings,
		pane.WithRight(effectChip(s, host.EffectRestart)))
	parts := []string{settings, m.recordsPane(ctx, r.W, recH)}
	if pendH > 0 {
		parts = append(parts, m.pendingPane(ctx, r.W, pendH))
	}
	_ = ic
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func (m *Model) settingsBody(ctx module.Context, w, h int) string {
	s := ctx.Styles()
	lw := 20
	top := max(0, m.row-h+1)
	var lines []string
	for i := top; i < len(m.rows) && len(lines) < h; i++ {
		rw := m.rows[i]
		label := rw.label
		if rw.kind == rowSplit && i > 0 && m.rows[i-1].kind == rowSplit {
			label = ""
		}
		line := s.Label.Render(fmt.Sprintf("%-*s", lw, label)) + " " + rw.value
		line = table.Fit(line, w, lipgloss.Left, ctx.Icons().Ellipsis)
		if i == m.row && m.focus == focusSettings {
			line = s.RowSel.Render(table.Fit(fmt.Sprintf("%-*s", lw, label)+" "+rw.value, w, lipgloss.Left, ctx.Icons().Ellipsis))
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func (m *Model) recordsPane(ctx module.Context, w, h int) string {
	s, ic := ctx.Styles(), ctx.Icons()
	recs, inFile := m.records()
	eff := host.EffectRestart
	where := "inline in config"
	if inFile {
		eff = host.EffectLive
		where = m.recs.Path
	}
	title := fmt.Sprintf("Extra records %s %d", ic.Bullet, len(recs))
	iw, ih := pane.Inner(w, h)
	var lines []string
	switch {
	case m.recErr != nil:
		lines = append(lines, s.Danger.Render(ic.Cross+" "+m.recErr.Error()))
		where = "unreadable"
	case len(recs) == 0:
		lines = append(lines, s.Muted.Render("No extra records. Press n to add one."))
		if !inFile {
			lines = append(lines, s.Muted.Render("Records in their own file apply without a restart: press M to create one."))
		}
	default:
		nw := min(max(20, iw/2), iw-28)
		lines = append(lines, s.TableHeader.Render(table.Fit(fmt.Sprintf("%-*s %-5s %s", nw, "NAME", "TYPE", "VALUE"), iw, lipgloss.Left, "")))
		top := max(0, m.rec-(ih-2))
		for i := top; i < len(recs) && len(lines) < ih; i++ {
			rc := recs[i]
			line := table.Fit(fmt.Sprintf("%-*s %-5s %s", nw, table.Fit(rc.Name, nw, lipgloss.Left, ic.Ellipsis), rc.Type, rc.Value), iw, lipgloss.Left, ic.Ellipsis)
			if i == m.rec && m.focus == focusRecords {
				line = s.RowSel.Render(line)
			} else if rc.Validate() != nil {
				line = s.Danger.Render(line)
			}
			lines = append(lines, line)
		}
		if !inFile && len(lines) < ih {
			lines = append(lines, "", s.Muted.Render("Inline records need a restart. M moves them to a file that applies live."))
		}
	}
	return pane.Render(s, title, strings.Join(lines, "\n"), w, h, m.focus == focusRecords,
		pane.WithExtra(s.Muted.Render(where)), pane.WithRight(effectChip(s, eff)))
}

func (m *Model) pendingPane(ctx module.Context, w, h int) string {
	s, ic := ctx.Styles(), ctx.Icons()
	var lines []string
	switch {
	case m.planErr != nil:
		lines = []string{s.Danger.Render(ic.Cross + " " + m.planErr.Error())}
	case m.planning:
		lines = []string{s.Muted.Render("Validating…")}
	default:
		lines = renderChanges(s, ic, m.changes, m.checks, 1)
	}
	title := "Pending changes"
	if n := len(m.changes); n > 0 {
		title = fmt.Sprintf("Pending changes %s %d file(s)", ic.Bullet, n)
	}
	_, ih := pane.Inner(w, h)
	if len(lines) > ih {
		lines = append(lines[:ih-1], s.Muted.Render(fmt.Sprintf("%s %d more lines: press d for the full diff", ic.Ellipsis, len(lines)-ih+1)))
	}
	return pane.Render(s, title, strings.Join(lines, "\n"), w, h, false,
		pane.WithBottom("d diff · ctrl+s apply · x discard"))
}

func (m *Model) diffView(ctx module.Context, r module.Rect) string {
	s, ic := ctx.Styles(), ctx.Icons()
	lines := renderChanges(s, ic, m.changes, m.checks, 3)
	if len(m.changes) == 0 {
		lines = append(lines, s.Muted.Render("No pending changes."))
	}
	_, ih := pane.Inner(r.W, r.H)
	m.diffTop = max(0, min(m.diffTop, len(lines)-ih))
	end := min(len(lines), m.diffTop+ih)
	return pane.Render(s, "Diff", strings.Join(lines[m.diffTop:end], "\n"), r.W, r.H, true,
		pane.WithBottom("j/k scroll · esc close · ctrl+s apply"))
}

// --- actions --------------------------------------------------------------------

func (m *Model) ready(ctx module.Context) (bool, string) {
	switch {
	case ctx.Host() == nil:
		return false, "DNS editing needs host mode."
	case !m.loaded:
		return false, "Still loading."
	case m.loadErr != nil:
		return false, m.loadErr.Error()
	}
	return true, ""
}

func (m *Model) editable(ctx module.Context) (bool, string) {
	if ok, why := m.ready(ctx); !ok {
		return ok, why
	}
	if ctx.ReadOnly() {
		return false, "This profile is read-only."
	}
	if m.diff {
		return false, "Close the diff first (esc)."
	}
	return true, ""
}

func (m *Model) recordsNeed() host.Need {
	if m.recs != nil {
		return host.NeedRecordsW
	}
	return host.NeedConfigW
}

func (m *Model) currentRow() (row, bool) {
	if m.row >= 0 && m.row < len(m.rows) {
		return m.rows[m.row], true
	}
	return row{}, false
}

func (m *Model) currentRecord() (dnsrec.Record, bool) {
	recs, _ := m.records()
	if m.rec >= 0 && m.rec < len(recs) {
		return recs[m.rec], true
	}
	return dnsrec.Record{}, false
}

func splitList(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' })
}

func eachValid(fn func(string) error) func(string) error {
	return func(s string) error {
		for _, x := range splitList(s) {
			if err := fn(x); err != nil {
				return err
			}
		}
		return nil
	}
}

func staged(id, msg string) tea.Cmd {
	return func() tea.Msg {
		return module.ActionResult{ActionID: id, Message: msg + " Review with d, apply with ctrl+s."}
	}
}

func failed(id string, err error) tea.Cmd {
	return func() tea.Msg { return module.ActionResult{ActionID: id, Err: err} }
}

func recordForm(title string, r dnsrec.Record) *module.FormSpec {
	types := slices.Clone(dnsrec.Types)
	if r.Type == "AAAA" {
		types = []string{"AAAA", "A"}
	}
	return &module.FormSpec{Title: title, Submit: "Stage", Fields: []module.Field{
		{ID: "name", Label: "Name", Value: r.Name, Required: true, Placeholder: "grafana.tail.example.com", Validate: dnsrec.ValidName,
			Help: "Fully qualified name the tailnet resolves"},
		{ID: "type", Label: "Type", Kind: module.FieldSelect, Options: types, Value: types[0]},
		{ID: "value", Label: "Address", Value: r.Value, Required: true, Placeholder: "100.64.0.5",
			Help: "IPv4 for A, IPv6 for AAAA"},
	}}
}

func recordFrom(v module.Values) (dnsrec.Record, error) {
	r := dnsrec.Record{Name: strings.TrimSpace(v["name"]), Type: v["type"], Value: strings.TrimSpace(v["value"])}
	return r, r.Validate()
}

func (m *Model) settingForm(rw row) *module.FormSpec {
	c := m.config()
	switch rw.kind {
	case rowMagic:
		return &module.FormSpec{Title: "MagicDNS", Submit: "Stage", Fields: []module.Field{
			{ID: "value", Label: "MagicDNS", Kind: module.FieldToggle, Value: fmt.Sprint(c.MagicDNS), Help: "Give every machine a name under the base domain. Needs a restart."}}}
	case rowOverride:
		return &module.FormSpec{Title: "Override local DNS", Submit: "Stage", Fields: []module.Field{
			{ID: "value", Label: "Override", Kind: module.FieldToggle, Value: fmt.Sprint(c.OverrideLocalDNS), Help: "Clients use the tailnet's nameservers instead of their own. Needs a restart."}}}
	case rowBase:
		return &module.FormSpec{Title: "Base domain", Submit: "Stage", Fields: []module.Field{
			{ID: "value", Label: "Base domain", Value: c.BaseDomain, Validate: dnsrec.ValidName, Placeholder: "tail.example.com",
				Help: "Must differ from the server_url host. Needs a restart."}}}
	case rowGlobal:
		return &module.FormSpec{Title: "Global nameservers", Submit: "Stage", Fields: []module.Field{
			{ID: "value", Label: "Nameservers", Value: strings.Join(c.Global, ", "), Validate: eachValid(dnsrec.ValidNameserver),
				Placeholder: "1.1.1.1, https://dns.nextdns.io/abc", Help: "IPs or DoH URLs, comma separated. Needs a restart."}}}
	case rowSearch:
		return &module.FormSpec{Title: "Search domains", Submit: "Stage", Fields: []module.Field{
			{ID: "value", Label: "Domains", Value: strings.Join(c.Search, ", "), Validate: eachValid(dnsrec.ValidName),
				Help: "Comma separated. Needs a restart."}}}
	case rowSplit:
		return m.splitForm(rw.domain, c.Split[rw.domain])
	}
	return nil
}

func (m *Model) splitForm(domain string, servers []string) *module.FormSpec {
	title := "Split DNS domain"
	if domain == "" {
		title = "Add split DNS domain"
	}
	help := "Queries for the domain go to these servers. Needs a restart."
	if domain != "" {
		help += " Leave empty to remove this domain."
	}
	return &module.FormSpec{Title: title, Submit: "Stage", Fields: []module.Field{
		{ID: "domain", Label: "Domain", Value: domain, Required: true, Validate: dnsrec.ValidName, Placeholder: "corp.example.com"},
		{ID: "servers", Label: "Nameservers", Value: strings.Join(servers, ", "), Required: domain == "", Validate: eachValid(dnsrec.ValidNameserver),
			Placeholder: "10.0.0.53", Help: help},
	}}
}

// removeSplit stages removing domain from split DNS by rewriting the whole
// split map without it. Unlike deleting the key, this also works when the
// map is written inline ({a: [..]}), and leaves "{}" once it is empty.
func (m *Model) removeSplit(domain string) error {
	key := strings.Join(splitSegs("")[:3], ".")
	es, _ := m.doc.Map(key)
	keep := es[:0:0]
	for _, e := range es {
		if e.Key != domain {
			keep = append(keep, e)
		}
	}
	if len(keep) == len(es) {
		return nil
	}
	return m.doc.SetMap(key, keep)
}

func splitSegs(domain string) []string {
	return []string{"dns", "nameservers", "split", domain}
}

// applySetting stages a settings edit into the working config.
func (m *Model) applySetting(rw row, v module.Values) error {
	switch rw.kind {
	case rowMagic:
		return m.doc.Set(dnsrec.KeyMagicDNS, v["value"])
	case rowOverride:
		return m.doc.Set(dnsrec.KeyOverride, v["value"])
	case rowBase:
		return m.doc.Set(dnsrec.KeyBaseDomain, v["value"])
	case rowGlobal:
		return m.doc.SetList(dnsrec.KeyGlobal, splitList(v["value"]))
	case rowSearch:
		return m.doc.SetList(dnsrec.KeySearch, splitList(v["value"]))
	case rowSplit:
		dom := strings.TrimSuffix(strings.TrimSpace(v["domain"]), ".")
		servers := splitList(v["servers"])
		if rw.domain != "" && (rw.domain != dom || len(servers) == 0) {
			if err := m.removeSplit(rw.domain); err != nil {
				return err
			}
		}
		if len(servers) == 0 {
			return nil // cleared: the domain is removed
		}
		return m.doc.SetListPath(splitSegs(dom), servers)
	}
	return errors.New("this row is not editable here")
}

func (m *Model) Actions() []module.Action {
	acts := []module.Action{
		{
			ID: "dns.edit", Title: "Edit", Keys: []string{"enter", "e"}, Mutating: true, Needs: m.editNeed(),
			Enabled: func(ctx module.Context) (bool, string) {
				if ok, why := m.editable(ctx); !ok {
					return ok, why
				}
				if m.focus == focusRecords {
					if _, ok := m.currentRecord(); !ok {
						return false, "No record selected. Press n to add one."
					}
					if m.recErr != nil {
						return false, "The records file could not be read."
					}
					return true, ""
				}
				if rw, ok := m.currentRow(); ok && rw.kind == rowRecordsFile {
					return false, "The records file is set by moving inline records (M) or in Server Settings (g s)."
				}
				return true, ""
			},
			Params: func(module.Context) *module.FormSpec {
				if m.focus == focusRecords {
					r, _ := m.currentRecord()
					return recordForm("Edit record", r)
				}
				rw, _ := m.currentRow()
				return m.settingForm(rw)
			},
			Run: func(ctx module.Context, v module.Values) tea.Cmd {
				if m.focus == focusRecords {
					r, err := recordFrom(v)
					if err != nil {
						return failed("dns.edit", err)
					}
					if err := m.setRecord(m.rec, &r); err != nil {
						return failed("dns.edit", err)
					}
					return tea.Batch(m.staged(ctx), staged("dns.edit", "Staged the record "+r.Name+"."))
				}
				rw, _ := m.currentRow()
				if err := m.applySetting(rw, v); err != nil {
					return failed("dns.edit", err)
				}
				if rw.kind == rowSplit && rw.domain != "" && len(splitList(v["servers"])) == 0 {
					return tea.Batch(m.staged(ctx), staged("dns.edit", "Staged removing split DNS for "+rw.domain+" (needs a restart)."))
				}
				return tea.Batch(m.staged(ctx), staged("dns.edit", "Staged "+rw.label+" (needs a restart)."))
			},
		},
		{
			ID: "dns.add", Title: "Add record / split domain", Keys: []string{"n"}, Mutating: true, Needs: m.addNeed(),
			Enabled: func(ctx module.Context) (bool, string) {
				if ok, why := m.editable(ctx); !ok {
					return ok, why
				}
				if m.focus == focusRecords && m.recErr != nil {
					return false, "The records file could not be read."
				}
				return true, ""
			},
			Params: func(module.Context) *module.FormSpec {
				if m.focus == focusRecords {
					return recordForm("Add record", dnsrec.Record{Type: "A"})
				}
				return m.splitForm("", nil)
			},
			Run: func(ctx module.Context, v module.Values) tea.Cmd {
				if m.focus == focusRecords {
					r, err := recordFrom(v)
					if err != nil {
						return failed("dns.add", err)
					}
					if err := m.setRecord(-1, &r); err != nil {
						return failed("dns.add", err)
					}
					recs, _ := m.records()
					m.rec = len(recs) - 1
					return tea.Batch(m.staged(ctx), staged("dns.add", "Staged the new record "+r.Name+"."))
				}
				if err := m.applySetting(row{kind: rowSplit}, v); err != nil {
					return failed("dns.add", err)
				}
				return tea.Batch(m.staged(ctx), staged("dns.add", "Staged split DNS for "+v["domain"]+" (needs a restart)."))
			},
		},
		{
			// Staging only: the write is confirmed at apply.
			ID: "dns.delete", Title: "Delete record / remove split domain", Keys: []string{"D"}, Needs: m.addNeed(),
			Enabled: func(ctx module.Context) (bool, string) {
				if ok, why := m.editable(ctx); !ok {
					return ok, why
				}
				if m.focus == focusRecords {
					if _, ok := m.currentRecord(); !ok {
						return false, "No record selected."
					}
					return true, ""
				}
				if rw, ok := m.currentRow(); !ok || rw.kind != rowSplit || rw.domain == "" {
					return false, "Only records and split DNS domains can be deleted."
				}
				return true, ""
			},
			Run: func(ctx module.Context, _ module.Values) tea.Cmd {
				if m.focus == focusRecords {
					r, _ := m.currentRecord()
					if err := m.setRecord(m.rec, nil); err != nil {
						return failed("dns.delete", err)
					}
					return tea.Batch(m.staged(ctx), staged("dns.delete", "Staged deleting "+r.Name+"."))
				}
				rw, _ := m.currentRow()
				if err := m.removeSplit(rw.domain); err != nil {
					return failed("dns.delete", err)
				}
				return tea.Batch(m.staged(ctx), staged("dns.delete", "Staged removing split DNS for "+rw.domain+"."))
			},
		},
		m.moveAction(),
		{
			ID: "dns.diff", Title: "Show diff", Keys: []string{"d"},
			Enabled: func(ctx module.Context) (bool, string) {
				if !m.dirty() {
					return false, "Nothing staged."
				}
				return true, ""
			},
			Run: func(module.Context, module.Values) tea.Cmd { m.diff, m.diffTop = !m.diff, 0; return nil },
		},
		m.applyAction(),
		{
			ID: "dns.discard", Title: "Discard staged changes", Keys: []string{"x"}, Danger: module.DangerConfirm,
			Consequence: "Staged DNS edits are dropped and the files are re-read from disk.",
			Enabled: func(ctx module.Context) (bool, string) {
				if ok, why := m.ready(ctx); !ok && m.loadErr == nil {
					return ok, why
				}
				if !m.dirty() && m.loadErr == nil && m.recErr == nil {
					return false, "Nothing staged."
				}
				return true, ""
			},
			Run: func(ctx module.Context, _ module.Values) tea.Cmd {
				m.diff = false
				return tea.Batch(m.load(ctx), func() tea.Msg {
					return module.ActionResult{ActionID: "dns.discard", Message: "Discarded staged DNS changes."}
				})
			},
		},
		{
			ID: "dns.rollback", Title: "Roll back the last DNS change", Keys: []string{"u"}, Mutating: true, Danger: module.DangerConfirm,
			Consequence: "Restores the files as they were before the last apply.",
			Targets:     func(module.Context) []module.Target { return appliedTargets(m.applied) },
			Enabled: func(ctx module.Context) (bool, string) {
				if len(m.applied) == 0 {
					return false, "Nothing to roll back."
				}
				if m.dirty() {
					return false, "Apply or discard the staged changes first."
				}
				return true, ""
			},
			Run: func(ctx module.Context, _ module.Values) tea.Cmd {
				h, as := ctx.Host(), m.applied
				return func() tea.Msg {
					c, cancel := context.WithTimeout(context.Background(), ioTimeout)
					defer cancel()
					return rolledBackMsg{err: changeset.Rollback(c, h, as)}
				}
			},
		},
	}
	return acts
}

func (m *Model) editNeed() host.Need {
	if m.focus == focusRecords {
		return m.recordsNeed()
	}
	return host.NeedConfigW
}

func (m *Model) addNeed() host.Need { return m.editNeed() }

// setRecord replaces record i (i < 0 appends; r == nil deletes) in the
// records file, or inline in the config when there is no file.
func (m *Model) setRecord(i int, r *dnsrec.Record) error {
	if m.recs != nil {
		var err error
		switch {
		case r == nil:
			err = m.recs.Delete(i)
		case i < 0:
			err = m.recs.Add(*r)
		default:
			err = m.recs.Set(i, *r)
		}
		if err == nil {
			m.recDirty = true
		}
		return err
	}
	recs := slices.Clone(m.config().ExtraRecords)
	switch {
	case r == nil:
		if i < 0 || i >= len(recs) {
			return fmt.Errorf("no record %d", i+1)
		}
		recs = slices.Delete(recs, i, i+1)
	case i < 0:
		recs = append(recs, *r)
	default:
		if i >= len(recs) {
			return fmt.Errorf("no record %d", i+1)
		}
		recs[i] = *r
	}
	return dnsrec.SetInline(m.doc, recs)
}

func changeTargets(cs []*host.Change) []module.Target {
	out := make([]module.Target, 0, len(cs))
	for _, c := range cs {
		out = append(out, module.Target{Kind: "file", ID: c.File.Path, Name: c.File.Path})
	}
	return out
}

func appliedTargets(as []host.Applied) []module.Target {
	out := make([]module.Target, 0, len(as))
	for _, a := range as {
		out = append(out, module.Target{Kind: "file", ID: a.File.Path, Name: a.File.Path})
	}
	return out
}

func errorCount(is []host.Issue) int {
	n := 0
	for _, i := range is {
		if i.Severity == host.SevError {
			n++
		}
	}
	return n
}

// consequence explains what applying the change set does.
func consequence(cs []*host.Change) string {
	var live, restart []string
	for _, c := range cs {
		name := c.File.Path
		if c.File.New {
			name += " (new)"
		}
		if c.Effect == host.EffectLive {
			live = append(live, name)
		} else {
			restart = append(restart, name)
		}
	}
	var parts []string
	if len(live) > 0 {
		parts = append(parts, "Records in "+strings.Join(live, ", ")+" apply immediately.")
	}
	if len(restart) > 0 {
		parts = append(parts, "Changes to "+strings.Join(restart, ", ")+" take effect after headscale restarts.")
	}
	parts = append(parts, "A backup is kept; u rolls back.")
	return strings.Join(parts, " ")
}

func (m *Model) applyAction() module.Action {
	cs := m.changes
	danger := module.Danger(max(changeset.Danger(cs), host.DangerConfirm))
	var need host.Need
	for _, c := range cs {
		if c.File.Kind == host.FileExtraRecords {
			need |= host.NeedRecordsW
		} else {
			need |= host.NeedConfigW
		}
	}
	return module.Action{
		ID: "dns.apply", Title: "Apply DNS changes", Keys: []string{"ctrl+s"}, Mutating: true, Danger: danger, Needs: need,
		Consequence: consequence(cs),
		Targets:     func(module.Context) []module.Target { return changeTargets(m.changes) },
		Enabled: func(ctx module.Context) (bool, string) {
			switch {
			case !m.dirty():
				return false, "Nothing staged."
			case m.planning:
				return false, "Still validating the change."
			case m.planErr != nil:
				return false, m.planErr.Error()
			case !changeset.Valid(m.changes) || errorCount(m.checks) > 0:
				return false, "Fix the errors shown under Pending changes first."
			}
			return true, ""
		},
		Run: func(ctx module.Context, _ module.Values) tea.Cmd {
			h, cs := ctx.Host(), m.changes
			m.diff = false
			return func() tea.Msg {
				c, cancel := context.WithTimeout(context.Background(), ioTimeout)
				defer cancel()
				as, err := changeset.Apply(c, h, cs)
				return appliedMsg{applied: as, changes: cs, effect: changeset.Effect(cs), err: err}
			}
		},
	}
}
