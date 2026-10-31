// Package derp is the DERP page (host mode): the embedded DERP server
// settings (derp.server.*), the DERP map sources (derp.urls and
// derp.paths), a typed region and node editor for map files, and the live
// map the server is using (/debug/derp) with per-region machine preference
// and latency. Edits are staged, validated and diffed, then written as one
// confirmed change set; all of them take effect after a headscale restart.
package derp

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/host/changeset"
	"github.com/tonhe/headsup/internal/host/derpmap"
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

const ioTimeout = 30 * time.Second

// Section is a part of the page; tab cycles through them.
type Section int

const (
	SecServer Section = iota
	SecSources
	SecMaps
	SecLive
)

var sectionNames = []string{"Embedded server", "Map sources", "Map files", "Live map"}

// Config keys.
const (
	keyURLs  = "derp.urls"
	keyPaths = "derp.paths"
)

// serverFields are the embedded server settings, in form order.
var serverFields = []struct{ key, label string }{
	{"derp.server.enabled", "Enabled"},
	{"derp.server.region_id", "Region ID"},
	{"derp.server.region_code", "Region code"},
	{"derp.server.region_name", "Region name"},
	{"derp.server.stun_listen_addr", "STUN address"},
	{"derp.server.verify_clients", "Verify clients"},
	{"derp.server.automatically_add_embedded_derp_region", "Add to DERP map"},
	{"derp.server.ipv4", "Public IPv4"},
	{"derp.server.ipv6", "Public IPv6"},
}

// mapFile is one DERP map file listed in derp.paths.
type mapFile struct {
	path  string
	file  *host.File
	m     *derpmap.File
	err   error
	dirty bool
}

// mapRow is one line of the map files list.
type mapRow struct {
	file   int // index into files
	region int // region ID; 0 for the file header
	node   int // node index; -1 for a region (or file) row
}

type sourceRow struct {
	key   string // keyURLs or keyPaths
	value string
}

// Model is the DERP module.
type Model struct {
	loaded  bool
	loadErr error
	cfg     *host.File
	doc     *hsconfig.Doc
	files   []*mapFile

	sec     Section
	cursor  [4]int
	sources []sourceRow
	mrows   []mapRow
	checks  []host.Issue

	live    derpmap.Map
	liveErr error
	liveAt  time.Time

	changes  []*host.Change
	planSeq  int
	planning bool
	planErr  error

	diff    bool
	diffTop int
	lastH   int

	applied   []host.Applied
	restartAt time.Time
}

// New returns the module.
func New() *Model { return &Model{} }

func (m *Model) ID() string               { return "derp" }
func (m *Model) Title() string            { return "DERP" }
func (m *Model) Icon(ic icons.Set) string { return ic.DERP }
func (m *Model) Order() int               { return 65 }
func (m *Model) Modes() module.Modes      { return module.ModeHost }
func (m *Model) Chord() string            { return "D" }
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
	return tea.Batch(m.load(ctx), m.fetchLive(ctx))
}

// --- loading --------------------------------------------------------------------

type loadedMsg struct {
	cfg   *host.File
	doc   *hsconfig.Doc
	files []*mapFile
	err   error
}

type liveMsg struct {
	m   derpmap.Map
	err error
}

func (m *Model) load(ctx module.Context) tea.Cmd {
	h := ctx.Host()
	return func() tea.Msg {
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
		paths, _ := doc.List(keyPaths)
		for _, p := range paths {
			mf := &mapFile{path: p}
			if mf.file, mf.err = h.Open(host.FileDERPMap, p); mf.err == nil {
				mf.m, mf.err = derpmap.ParseFile(p, mf.file.Raw)
			}
			msg.files = append(msg.files, mf)
		}
		return msg
	}
}

// derpSource is implemented by debug clients that can fetch /debug/derp.
type derpSource interface {
	DERPMap(ctx context.Context) ([]byte, string, error)
}

func (m *Model) fetchLive(ctx module.Context) tea.Cmd {
	dbg := ctx.Host().Debug()
	return func() tea.Msg {
		src, ok := dbg.(derpSource)
		if !ok || dbg == nil || !dbg.Available() {
			return liveMsg{err: errors.New("the live map needs headscale's debug endpoint (metrics_listen_addr) and a running server")}
		}
		c, cancel := context.WithTimeout(context.Background(), ioTimeout)
		defer cancel()
		b, ct, err := src.DERPMap(c)
		if err != nil {
			return liveMsg{err: err}
		}
		lm, err := derpmap.ParseLive(b, ct)
		if err != nil {
			return liveMsg{err: fmt.Errorf("/debug/derp: %w", err)}
		}
		return liveMsg{m: lm}
	}
}

func (m *Model) dirty() bool { return len(m.items()) > 0 }

// items: new and changed map files first, then the config (which may list
// a new file in derp.paths).
func (m *Model) items() []changeset.Item {
	var out []changeset.Item
	for _, f := range m.files {
		if f.m == nil || f.file == nil || !f.dirty {
			continue
		}
		if b, err := f.m.Bytes(); err == nil && (f.file.New || string(b) != string(f.file.Raw)) {
			out = append(out, changeset.Item{File: f.file, Next: b})
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

func (m *Model) get(key string) string {
	if m.doc == nil {
		return ""
	}
	v, ok := m.doc.Get(key)
	if !ok {
		if f, ok := hsconfig.Lookup(key); ok && f.Default != nil {
			return fmt.Sprint(f.Default)
		}
	}
	return v
}

func (m *Model) embeddedID() (int, bool) {
	if m.get("derp.server.enabled") != "true" {
		return 0, false
	}
	id, err := strconv.Atoi(m.get("derp.server.region_id"))
	return id, err == nil
}

func (m *Model) rebuild() {
	m.sources = nil
	m.mrows = nil
	m.checks = nil
	if m.doc == nil {
		return
	}
	urls, _ := m.doc.List(keyURLs)
	paths, _ := m.doc.List(keyPaths)
	for _, u := range urls {
		m.sources = append(m.sources, sourceRow{keyURLs, u})
	}
	for _, p := range paths {
		m.sources = append(m.sources, sourceRow{keyPaths, p})
	}
	for i, f := range m.files {
		m.mrows = append(m.mrows, mapRow{file: i, node: -1})
		if f.m == nil {
			continue
		}
		for _, r := range f.m.Map.Regions {
			m.mrows = append(m.mrows, mapRow{file: i, region: r.RegionID, node: -1})
			for j := range r.Nodes {
				m.mrows = append(m.mrows, mapRow{file: i, region: r.RegionID, node: j})
			}
		}
		for _, is := range derpmap.Validate(f.m.Map) {
			is.Message = path.Base(f.path) + ": " + is.Message
			m.checks = append(m.checks, is)
		}
		if id, ok := m.embeddedID(); ok {
			m.checks = append(m.checks, derpmap.CheckEmbedded(f.m.Map, id)...)
		}
	}
	if len(urls) == 0 && len(paths) == 0 && m.get("derp.server.enabled") != "true" {
		m.checks = append(m.checks, host.Issue{Severity: host.SevError, Message: "no DERP source: add a map URL or file, or enable the embedded server", Source: "schema"})
	}
	if m.get("derp.server.enabled") == "true" {
		if su, _ := m.doc.Get("server_url"); su != "" && !strings.HasPrefix(su, "https://") {
			m.checks = append(m.checks, host.Issue{Severity: host.SevWarn, Message: "the embedded DERP server needs server_url to use https", Source: "schema"})
		}
	}
	for s, n := range []int{len(serverFields), len(m.sources), len(m.mrows), len(m.live.Regions)} {
		m.cursor[s] = max(0, min(m.cursor[s], n-1))
	}
}

// --- update -------------------------------------------------------------------

type appliedMsg struct {
	applied []host.Applied
	err     error
}

type rolledBackMsg struct{ err error }

type serviceMsg struct{ since time.Time }

type newFileCheckedMsg struct {
	path   string
	region derpmap.Region
	err    error
}

func (m *Model) Update(ctx module.Context, msg tea.Msg) (module.Module, tea.Cmd) {
	switch msg := msg.(type) {
	case loadedMsg:
		m.loaded, m.loadErr = true, msg.err
		m.cfg, m.doc, m.files = msg.cfg, msg.doc, msg.files
		m.changes, m.planErr, m.planning, m.diff = nil, nil, false, false
		m.planSeq++
		m.rebuild()
	case liveMsg:
		m.live, m.liveErr, m.liveAt = msg.m, msg.err, ctx.Now()
		m.rebuild()
	case planMsg:
		if msg.seq == m.planSeq {
			m.changes, m.planErr, m.planning = msg.changes, msg.err, false
		}
	case appliedMsg:
		res := module.ActionResult{ActionID: "derp.apply", Err: msg.err}
		if msg.err != nil {
			var cod *host.ErrChangedOnDisk
			if errors.As(msg.err, &cod) {
				res.Err = fmt.Errorf("%w. Press x to reload it (your staged edits are discarded), then make them again", msg.err)
			}
			return m, func() tea.Msg { return res }
		}
		m.applied = msg.applied
		m.restartAt = msg.applied[len(msg.applied)-1].At
		res.Message = "Saved. Restart headscale to use the new DERP settings (g l); the live map updates after it."
		return m, tea.Batch(m.load(ctx), func() tea.Msg { return res })
	case rolledBackMsg:
		res := module.ActionResult{ActionID: "derp.rollback", Err: msg.err, Message: "Rolled back the last DERP change."}
		if msg.err == nil {
			m.applied = nil
		}
		return m, tea.Batch(m.load(ctx), func() tea.Msg { return res })
	case newFileCheckedMsg:
		return m, m.finishNewFile(ctx, msg)
	case serviceMsg:
		if !msg.since.IsZero() && msg.since.After(m.restartAt) {
			m.restartAt = time.Time{}
			return m, m.fetchLive(ctx)
		}
	case store.UpdatedMsg:
		if !m.restartAt.IsZero() && ctx.Host() != nil {
			return m, m.checkRestart(ctx)
		}
	case module.NavigateMsg:
		if ctx.Host() == nil {
			return m, nil
		}
		cmds := []tea.Cmd{m.fetchLive(ctx)}
		if !m.dirty() {
			cmds = append(cmds, m.load(ctx))
		}
		return m, tea.Batch(cmds...)
	case tea.KeyMsg:
		m.key(msg)
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

func (m *Model) rowCount(s Section) int {
	switch s {
	case SecServer:
		return len(serverFields)
	case SecSources:
		return len(m.sources)
	case SecMaps:
		return len(m.mrows)
	}
	return len(m.live.Regions)
}

func (m *Model) key(k tea.KeyMsg) {
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
		}
		return
	}
	c := &m.cursor[m.sec]
	switch k.String() {
	case "tab", "right", "l":
		m.sec = (m.sec + 1) % 4
	case "shift+tab", "left", "h":
		m.sec = (m.sec + 3) % 4
	case "j", "down":
		*c = min(*c+1, max(0, m.rowCount(m.sec)-1))
	case "k", "up":
		*c = max(*c-1, 0)
	case "home":
		*c = 0
	case "end":
		*c = max(0, m.rowCount(m.sec)-1)
	}
}

// SetSection switches the visible section (tests, deep links).
func (m *Model) SetSection(s Section) { m.sec = s }

func (m *Model) Hints() []module.Hint {
	if m.diff {
		return []module.Hint{{Key: "j/k", Desc: "scroll"}, {Key: "esc", Desc: "close diff"}, {Key: "ctrl+s", Desc: "apply"}}
	}
	h := []module.Hint{{Key: "tab", Desc: "section"}}
	switch m.sec {
	case SecServer:
		h = append(h, module.Hint{Key: "enter", Desc: "edit"})
	case SecSources:
		h = append(h, module.Hint{Key: "n", Desc: "add"}, module.Hint{Key: "enter", Desc: "edit"}, module.Hint{Key: "D", Desc: "remove"})
	case SecMaps:
		h = append(h, module.Hint{Key: "n", Desc: "add region/node"}, module.Hint{Key: "enter", Desc: "edit"}, module.Hint{Key: "D", Desc: "delete"}, module.Hint{Key: "N", Desc: "new file"})
	case SecLive:
		h = append(h, module.Hint{Key: "r", Desc: "refresh"})
	}
	if m.dirty() {
		h = append(h, module.Hint{Key: "d", Desc: "diff"}, module.Hint{Key: "ctrl+s", Desc: "apply"}, module.Hint{Key: "x", Desc: "discard"})
	} else if len(m.applied) > 0 {
		h = append(h, module.Hint{Key: "u", Desc: "roll back"})
	}
	return h
}

// --- view ---------------------------------------------------------------------

func (m *Model) View(ctx module.Context, r module.Rect) string {
	s := ctx.Styles()
	m.lastH = r.H
	switch {
	case ctx.Host() == nil:
		return empty.Render(s, "DERP needs host mode", "Run on the headscale server to edit DERP settings.", "", r.W, r.H)
	case !m.loaded:
		return empty.Render(s, "Loading DERP settings…", "", "", r.W, r.H)
	case m.loadErr != nil:
		return empty.Render(s, "Could not read the config", m.loadErr.Error(), "", r.W, r.H)
	}
	if m.diff {
		return m.diffView(ctx, r)
	}
	tabs := m.tabs(ctx, r.W)
	pendH := 0
	if m.dirty() || m.planErr != nil {
		pendH = max(0, min(max(6, r.H/3), r.H-8))
	}
	mainH := max(3, r.H-1-pendH)
	var body string
	switch m.sec {
	case SecServer:
		body = m.serverPane(ctx, r.W, mainH)
	case SecSources:
		body = m.sourcesPane(ctx, r.W, mainH)
	case SecMaps:
		body = m.mapsPane(ctx, r.W, mainH)
	default:
		body = m.livePane(ctx, r.W, mainH)
	}
	parts := []string{tabs, body}
	if pendH > 0 {
		parts = append(parts, m.pendingPane(ctx, r.W, pendH))
	}
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func (m *Model) tabs(ctx module.Context, w int) string {
	s := ctx.Styles()
	var parts []string
	for i, n := range sectionNames {
		if Section(i) == m.sec {
			parts = append(parts, s.SidebarActive.Render(" "+n+" "))
		} else {
			parts = append(parts, s.SidebarItem.Render(" "+n+" "))
		}
	}
	return table.Fit(strings.Join(parts, s.Muted.Render("│")), w, lipgloss.Left, ctx.Icons().Ellipsis)
}

func (m *Model) list(ctx module.Context, lines []string, cur, w, h int) string {
	s := ctx.Styles()
	top := max(0, cur-h+1)
	var out []string
	for i := top; i < len(lines) && len(out) < h; i++ {
		l := table.Fit(lines[i], w, lipgloss.Left, ctx.Icons().Ellipsis)
		if i == cur {
			l = s.RowSel.Render(table.Fit(stripStyle(lines[i]), w, lipgloss.Left, ctx.Icons().Ellipsis))
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

func (m *Model) serverPane(ctx module.Context, w, h int) string {
	s := ctx.Styles()
	iw, ih := pane.Inner(w, h)
	lines := make([]string, 0, len(serverFields))
	for _, f := range serverFields {
		v := m.get(f.key)
		if v == "" {
			v = s.Muted.Render("(not set)")
		}
		lines = append(lines, s.Label.Render(fmt.Sprintf("%-16s", f.label))+" "+v)
	}
	body := m.list(ctx, lines, m.cursor[SecServer], iw, ih)
	if m.get("derp.server.enabled") != "true" && ih > len(lines)+1 {
		body += "\n\n" + s.Muted.Render("The embedded server is off; clients use the regions from the map sources.")
	}
	return pane.Render(s, "Embedded DERP server", body, w, h, true, pane.WithRight(effectChip(s, host.EffectRestart)))
}

func (m *Model) sourcesPane(ctx module.Context, w, h int) string {
	s := ctx.Styles()
	iw, ih := pane.Inner(w, h)
	var lines []string
	for _, src := range m.sources {
		kind := "URL "
		if src.key == keyPaths {
			kind = "file"
		}
		lines = append(lines, s.Label.Render(kind)+" "+src.value)
	}
	body := m.list(ctx, lines, m.cursor[SecSources], iw, ih)
	if len(lines) == 0 {
		body = s.Muted.Render("No DERP map sources. Press n to add a URL or a file.")
	}
	return pane.Render(s, "DERP map sources", body, w, h, true, pane.WithRight(effectChip(s, host.EffectRestart)))
}

func (m *Model) mapsPane(ctx module.Context, w, h int) string {
	s, ic := ctx.Styles(), ctx.Icons()
	iw, ih := pane.Inner(w, h)
	if len(m.files) == 0 {
		return pane.Render(s, "DERP map files", s.Muted.Render("No DERP map files in derp.paths. Press N to create one with a custom region."),
			w, h, true, pane.WithRight(effectChip(s, host.EffectRestart)))
	}
	var lines []string
	for _, mr := range m.mrows {
		f := m.files[mr.file]
		switch {
		case mr.region == 0:
			l := s.Strong.Render(f.path)
			switch {
			case f.err != nil:
				l += " " + s.Danger.Render(ic.Cross+" "+f.err.Error())
			case f.file != nil && f.file.New:
				l += " " + s.Accent.Render("(new, "+f.m.Format.String()+")")
			default:
				l += " " + s.Muted.Render("("+f.m.Format.String()+")")
			}
			lines = append(lines, l)
		case mr.node < 0:
			rg, _ := f.m.Map.Region(mr.region)
			lines = append(lines, fmt.Sprintf("  %s %-4d %-10s %s", ic.Arrow, rg.RegionID, rg.RegionCode, s.Muted.Render(rg.RegionName)))
		default:
			rg, _ := f.m.Map.Region(mr.region)
			n := rg.Nodes[mr.node]
			extra := []string{}
			if n.IPv4 != "" {
				extra = append(extra, n.IPv4)
			}
			if n.IPv6 != "" {
				extra = append(extra, n.IPv6)
			}
			if n.DERPPort != 0 {
				extra = append(extra, fmt.Sprintf("derp:%d", n.DERPPort))
			}
			if n.STUNPort != 0 {
				extra = append(extra, fmt.Sprintf("stun:%d", n.STUNPort))
			}
			lines = append(lines, fmt.Sprintf("      %-10s %s %s", n.Name, n.HostName, s.Muted.Render(strings.Join(extra, " "))))
		}
	}
	return pane.Render(s, "DERP map files", m.list(ctx, lines, m.cursor[SecMaps], iw, ih), w, h, true,
		pane.WithRight(effectChip(s, host.EffectRestart)))
}

func (m *Model) livePane(ctx module.Context, w, h int) string {
	s, ic := ctx.Styles(), ctx.Icons()
	iw, ih := pane.Inner(w, h)
	if m.liveErr != nil {
		return pane.Render(s, "Live DERP map", s.Muted.Render(m.liveErr.Error()), w, h, true)
	}
	prefs := derpmap.Preferences(ctx.Store().Extras)
	source := map[int]string{}
	for _, f := range m.files {
		if f.m != nil {
			for _, r := range f.m.Map.Regions {
				source[r.RegionID] = path.Base(f.path)
			}
		}
	}
	if id, ok := m.embeddedID(); ok {
		source[id] = "embedded"
	}
	hdr := fmt.Sprintf("%-4s %-9s %-18s %-5s %-12s %-6s %s", "ID", "CODE", "NAME", "NODES", "SOURCE", "PREFER", "LATENCY")
	lines := []string{s.TableHeader.Render(table.Fit(hdr, iw, lipgloss.Left, ""))}
	var rows []string
	for _, r := range m.live.Regions {
		src := source[r.RegionID]
		if src == "" {
			src = "url"
		}
		p := prefs[r.RegionID]
		pref, lat := "-", "-"
		if p.Machines > 0 {
			pref = strconv.Itoa(p.Machines)
		}
		if p.Samples > 0 {
			lat = fmt.Sprintf("%.0f ms", p.MedianMS)
			if iw >= 90 {
				lat += fmt.Sprintf(" (best %.0f, %d machines)", p.BestMS, p.Samples)
			}
		}
		rows = append(rows, fmt.Sprintf("%-4d %-9s %-18s %-5d %-12s %-6s %s", r.RegionID, r.RegionCode,
			table.Fit(r.RegionName, 18, lipgloss.Left, ic.Ellipsis), len(r.Nodes), table.Fit(src, 12, lipgloss.Left, ic.Ellipsis), pref, lat))
	}
	body := strings.Join(lines, "\n")
	if len(rows) == 0 {
		body += "\n" + s.Muted.Render("The server reports no DERP regions.")
	} else {
		body += "\n" + m.list(ctx, rows, m.cursor[SecLive], iw, ih-1)
	}
	note := ""
	if !m.restartAt.IsZero() {
		note = "saved changes show here after a restart"
	}
	if len(ctx.Store().Extras) == 0 {
		note = strings.TrimPrefix(note+"; ", "; ") + "machine preference needs database access"
	}
	return pane.Render(s, "Live DERP map", body, w, h, true, pane.WithBottom(note))
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
	_, ih := pane.Inner(w, h)
	if len(lines) > ih && ih > 0 {
		lines = append(lines[:ih-1], s.Muted.Render(fmt.Sprintf("%s %d more lines: press d for the full diff", ic.Ellipsis, len(lines)-ih+1)))
	}
	return pane.Render(s, fmt.Sprintf("Pending changes %s %d file(s)", ic.Bullet, len(m.changes)), strings.Join(lines, "\n"), w, h, false,
		pane.WithBottom("d diff · ctrl+s apply · x discard"))
}

func (m *Model) diffView(ctx module.Context, r module.Rect) string {
	s, ic := ctx.Styles(), ctx.Icons()
	lines := renderChanges(s, ic, m.changes, m.checks, 3)
	_, ih := pane.Inner(r.W, r.H)
	m.diffTop = max(0, min(m.diffTop, len(lines)-ih))
	end := min(len(lines), m.diffTop+ih)
	return pane.Render(s, "Diff", strings.Join(lines[m.diffTop:end], "\n"), r.W, r.H, true,
		pane.WithBottom("j/k scroll · esc close · ctrl+s apply"))
}

// stripStyle drops ANSI styling so a selected row renders in one style.
func stripStyle(s string) string {
	var b strings.Builder
	esc := false
	for _, r := range s {
		switch {
		case r == '\x1b':
			esc = true
		case esc && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'):
			esc = false
		case !esc:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// --- editing helpers ------------------------------------------------------------

func (m *Model) curSource() (sourceRow, bool) {
	c := m.cursor[SecSources]
	if c >= 0 && c < len(m.sources) {
		return m.sources[c], true
	}
	return sourceRow{}, false
}

func (m *Model) curMap() (mapRow, *mapFile, bool) {
	c := m.cursor[SecMaps]
	if c < 0 || c >= len(m.mrows) {
		return mapRow{}, nil, false
	}
	mr := m.mrows[c]
	return mr, m.files[mr.file], true
}

func checkSourceURL(s string) error {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return fmt.Errorf("%q is not an http(s) URL", s)
	}
	return nil
}

// checkAbs checks a DERP map path on the headscale host (always POSIX).
func checkAbs(s string) error {
	if !path.IsAbs(s) {
		return fmt.Errorf("%q must be an absolute path", s)
	}
	switch strings.ToLower(path.Ext(s)) {
	case ".json", ".hujson", ".yaml", ".yml":
		return nil
	}
	return fmt.Errorf("%q must end in .json, .hujson, .yaml or .yml", s)
}

func intIn(lo, hi int) func(string) error {
	return func(s string) error {
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil || n < lo || n > hi {
			return fmt.Errorf("must be a number from %d to %d", lo, hi)
		}
		return nil
	}
}

func atoi(s string) int { n, _ := strconv.Atoi(strings.TrimSpace(s)); return n }

func fieldCheck(key string) func(string) error {
	return func(v string) error {
		if f, ok := hsconfig.Lookup(key); ok {
			return hsconfig.CheckValue(f, v)
		}
		return nil
	}
}

func (m *Model) serverForm() *module.FormSpec {
	var fs []module.Field
	for _, f := range serverFields {
		cf, _ := hsconfig.Lookup(f.key)
		fd := module.Field{ID: f.key, Label: f.label, Value: m.get(f.key), Help: cf.Help, Validate: fieldCheck(f.key)}
		if cf.Type == hsconfig.TypeBool {
			fd.Kind = module.FieldToggle
		}
		fs = append(fs, fd)
	}
	return &module.FormSpec{Title: "Embedded DERP server", Submit: "Stage", Fields: fs}
}

func (m *Model) stageServer(v module.Values) error {
	for _, f := range serverFields {
		nv, ok := v[f.key]
		if !ok || nv == m.get(f.key) {
			continue
		}
		if nv == "" {
			if err := m.doc.Delete(f.key); err != nil {
				return err
			}
			continue
		}
		if err := m.doc.Set(f.key, nv); err != nil {
			return err
		}
	}
	return nil
}

func (m *Model) setSources(key string, list []string) error {
	return m.doc.SetList(key, list)
}

func nodeFields(n derpmap.Node) []module.Field {
	port := func(p int) string {
		if p == 0 {
			return ""
		}
		return strconv.Itoa(p)
	}
	return []module.Field{
		{ID: "node", Label: "Node name", Value: n.Name, Required: true, Placeholder: "900a"},
		{ID: "hostname", Label: "Hostname", Value: n.HostName, Required: true, Placeholder: "derp.example.com", Help: "TLS name clients connect to"},
		{ID: "ipv4", Label: "IPv4", Value: n.IPv4, Help: "Optional; \"none\" disables IPv4"},
		{ID: "ipv6", Label: "IPv6", Value: n.IPv6, Help: "Optional; \"none\" disables IPv6"},
		{ID: "derpport", Label: "DERP port", Value: port(n.DERPPort), Validate: intIn(0, 65535), Help: "Empty means 443"},
		{ID: "stunport", Label: "STUN port", Value: port(n.STUNPort), Validate: intIn(-1, 65535), Help: "Empty means 3478; -1 disables STUN"},
		{ID: "stunonly", Label: "STUN only", Kind: module.FieldToggle, Value: strconv.FormatBool(n.STUNOnly)},
		{ID: "insecure", Label: "Skip TLS checks", Kind: module.FieldToggle, Value: strconv.FormatBool(n.InsecureForTests), Help: "Tests only"},
	}
}

func nodeFrom(v module.Values, region int) derpmap.Node {
	return derpmap.Node{Name: strings.TrimSpace(v["node"]), RegionID: region, HostName: strings.TrimSpace(v["hostname"]),
		IPv4: strings.TrimSpace(v["ipv4"]), IPv6: strings.TrimSpace(v["ipv6"]), DERPPort: atoi(v["derpport"]), STUNPort: atoi(v["stunport"]),
		STUNOnly: v["stunonly"] == "true", InsecureForTests: v["insecure"] == "true"}
}

func regionFields(r derpmap.Region) []module.Field {
	id := ""
	if r.RegionID != 0 {
		id = strconv.Itoa(r.RegionID)
	}
	return []module.Field{
		{ID: "id", Label: "Region ID", Value: id, Required: true, Validate: intIn(1, 999), Help: "Custom regions use 900-999 by convention"},
		{ID: "code", Label: "Region code", Value: r.RegionCode, Required: true, Placeholder: "lab", Help: "Short, unique; shown by tailscale netcheck"},
		{ID: "name", Label: "Region name", Value: r.RegionName, Placeholder: "Home lab"},
	}
}

func regionFrom(v module.Values) derpmap.Region {
	return derpmap.Region{RegionID: atoi(v["id"]), RegionCode: strings.TrimSpace(v["code"]), RegionName: strings.TrimSpace(v["name"])}
}

func failed(id string, err error) tea.Cmd {
	return func() tea.Msg { return module.ActionResult{ActionID: id, Err: err} }
}

func ok(id, msg string) tea.Cmd {
	return func() tea.Msg {
		return module.ActionResult{ActionID: id, Message: msg + " Review with d, apply with ctrl+s (needs a restart)."}
	}
}

// putRegion validates r against the file's map and stores it.
func (m *Model) putRegion(f *mapFile, old int, r derpmap.Region) error {
	if err := f.m.Map.CheckNewRegion(r, old); err != nil {
		return err
	}
	if err := f.m.ReplaceRegion(old, r); err != nil {
		return err
	}
	f.dirty = true
	return nil
}

func nodeErr(n derpmap.Node, region int) error {
	probe := derpmap.Region{RegionID: region, RegionCode: "x", Nodes: []derpmap.Node{n}}
	for _, is := range derpmap.Validate(derpmap.Map{Regions: []derpmap.Region{probe}}) {
		if is.Severity == host.SevError {
			return errors.New(is.Message)
		}
	}
	return nil
}

// --- actions --------------------------------------------------------------------

func (m *Model) ready(ctx module.Context) (bool, string) {
	switch {
	case ctx.Host() == nil:
		return false, "DERP editing needs host mode."
	case !m.loaded:
		return false, "Still loading."
	case m.loadErr != nil:
		return false, m.loadErr.Error()
	case ctx.ReadOnly():
		return false, "This profile is read-only."
	case m.diff:
		return false, "Close the diff first (esc)."
	}
	return true, ""
}

func (m *Model) Actions() []module.Action {
	return []module.Action{
		{
			ID: "derp.edit", Title: "Edit", Keys: []string{"enter", "e"}, Mutating: true, Needs: host.NeedConfigW,
			Enabled: func(ctx module.Context) (bool, string) {
				if ok, why := m.ready(ctx); !ok {
					return ok, why
				}
				switch m.sec {
				case SecSources:
					if _, ok := m.curSource(); !ok {
						return false, "No source selected. Press n to add one."
					}
				case SecMaps:
					mr, f, ok := m.curMap()
					if !ok {
						return false, "No map file. Press N to create one."
					}
					if f.m == nil {
						return false, "This file could not be read."
					}
					if mr.region == 0 {
						return false, "Select a region or node to edit; n adds a region."
					}
				case SecLive:
					return false, "The live map is read-only; edit the map files or sources."
				}
				return true, ""
			},
			Params: func(module.Context) *module.FormSpec {
				switch m.sec {
				case SecSources:
					src, _ := m.curSource()
					return &module.FormSpec{Title: "Edit DERP map source", Submit: "Stage", Fields: []module.Field{
						{ID: "value", Label: "Source", Value: src.value, Required: true, Validate: sourceCheck(src.key)}}}
				case SecMaps:
					mr, f, _ := m.curMap()
					rg, _ := f.m.Map.Region(mr.region)
					if mr.node < 0 {
						return &module.FormSpec{Title: fmt.Sprintf("Edit region %d", rg.RegionID), Submit: "Stage", Fields: regionFields(rg)}
					}
					return &module.FormSpec{Title: fmt.Sprintf("Edit node in region %d", rg.RegionID), Submit: "Stage", Fields: nodeFields(rg.Nodes[mr.node])}
				}
				return m.serverForm()
			},
			Run: func(ctx module.Context, v module.Values) tea.Cmd {
				switch m.sec {
				case SecSources:
					src, _ := m.curSource()
					list, _ := m.doc.List(src.key)
					if i := slices.Index(list, src.value); i >= 0 {
						list[i] = strings.TrimSpace(v["value"])
					}
					if err := m.setSources(src.key, list); err != nil {
						return failed("derp.edit", err)
					}
					return tea.Batch(m.staged(ctx), ok("derp.edit", "Staged the source change."))
				case SecMaps:
					mr, f, _ := m.curMap()
					rg, _ := f.m.Map.Region(mr.region)
					if mr.node < 0 {
						nr := regionFrom(v)
						nr.Nodes = rg.Nodes
						for i := range nr.Nodes {
							if nr.Nodes[i].RegionID != 0 {
								nr.Nodes[i].RegionID = nr.RegionID
							}
						}
						if err := m.putRegion(f, rg.RegionID, nr); err != nil {
							return failed("derp.edit", err)
						}
						return tea.Batch(m.staged(ctx), ok("derp.edit", fmt.Sprintf("Staged region %d.", nr.RegionID)))
					}
					n := nodeFrom(v, rg.Nodes[mr.node].RegionID)
					if err := nodeErr(n, rg.RegionID); err != nil {
						return failed("derp.edit", err)
					}
					rg.Nodes = slices.Clone(rg.Nodes)
					rg.Nodes[mr.node] = n
					if err := m.putRegion(f, rg.RegionID, rg); err != nil {
						return failed("derp.edit", err)
					}
					return tea.Batch(m.staged(ctx), ok("derp.edit", "Staged node "+n.Name+"."))
				}
				if err := m.stageServer(v); err != nil {
					return failed("derp.edit", err)
				}
				return tea.Batch(m.staged(ctx), ok("derp.edit", "Staged the embedded server settings."))
			},
		},
		{
			ID: "derp.add", Title: "Add source / region / node", Keys: []string{"n"}, Mutating: true, Needs: host.NeedConfigW,
			Enabled: func(ctx module.Context) (bool, string) {
				if ok, why := m.ready(ctx); !ok {
					return ok, why
				}
				switch m.sec {
				case SecServer, SecLive:
					return false, "Nothing to add here."
				case SecMaps:
					_, f, ok := m.curMap()
					if !ok {
						return false, "No map file. Press N to create one."
					}
					if f.m == nil {
						return false, "This file could not be read."
					}
				}
				return true, ""
			},
			Params: func(module.Context) *module.FormSpec {
				if m.sec == SecSources {
					return &module.FormSpec{Title: "Add DERP map source", Submit: "Stage", Fields: []module.Field{
						{ID: "kind", Label: "Kind", Kind: module.FieldSelect, Options: []string{"URL", "file"}, Value: "URL"},
						{ID: "value", Label: "URL or path", Required: true, Placeholder: "https://… or /etc/headscale/derp.yaml",
							Help: "A file added here must exist; N in Map files creates one"}}}
				}
				mr, f, _ := m.curMap()
				if mr.region == 0 {
					next := nextRegionID(f.m.Map)
					return &module.FormSpec{Title: "Add region to " + path.Base(f.path), Submit: "Stage",
						Fields: append(regionFields(derpmap.Region{RegionID: next}), nodeFields(derpmap.Node{Name: fmt.Sprintf("%da", next)})...)}
				}
				rg, _ := f.m.Map.Region(mr.region)
				return &module.FormSpec{Title: fmt.Sprintf("Add node to region %d", rg.RegionID), Submit: "Stage",
					Fields: nodeFields(derpmap.Node{Name: fmt.Sprintf("%d%c", rg.RegionID, 'a'+len(rg.Nodes)%26)})}
			},
			Run: func(ctx module.Context, v module.Values) tea.Cmd {
				if m.sec == SecSources {
					key, check := keyURLs, checkSourceURL
					if v["kind"] == "file" {
						key, check = keyPaths, checkAbs
					}
					val := strings.TrimSpace(v["value"])
					if err := check(val); err != nil {
						return failed("derp.add", err)
					}
					list, _ := m.doc.List(key)
					if slices.Contains(list, val) {
						return failed("derp.add", fmt.Errorf("%s is already listed", val))
					}
					if err := m.setSources(key, append(list, val)); err != nil {
						return failed("derp.add", err)
					}
					return tea.Batch(m.staged(ctx), ok("derp.add", "Staged the new source "+val+"."))
				}
				mr, f, _ := m.curMap()
				if mr.region == 0 {
					r := regionFrom(v)
					n := nodeFrom(v, r.RegionID)
					r.Nodes = []derpmap.Node{n}
					if err := m.putRegion(f, 0, r); err != nil {
						return failed("derp.add", err)
					}
					return tea.Batch(m.staged(ctx), ok("derp.add", fmt.Sprintf("Staged region %d (%s).", r.RegionID, r.RegionCode)))
				}
				rg, _ := f.m.Map.Region(mr.region)
				n := nodeFrom(v, rg.RegionID)
				if err := nodeErr(n, rg.RegionID); err != nil {
					return failed("derp.add", err)
				}
				rg.Nodes = append(slices.Clone(rg.Nodes), n)
				if err := m.putRegion(f, rg.RegionID, rg); err != nil {
					return failed("derp.add", err)
				}
				return tea.Batch(m.staged(ctx), ok("derp.add", "Staged node "+n.Name+"."))
			},
		},
		{
			// Staging only; the write is confirmed at apply.
			ID: "derp.delete", Title: "Remove source / region / node", Keys: []string{"D"}, Needs: host.NeedConfigW,
			Enabled: func(ctx module.Context) (bool, string) {
				if ok, why := m.ready(ctx); !ok {
					return ok, why
				}
				switch m.sec {
				case SecSources:
					if _, ok := m.curSource(); !ok {
						return false, "No source selected."
					}
					return true, ""
				case SecMaps:
					mr, f, ok := m.curMap()
					if !ok || mr.region == 0 || f.m == nil {
						return false, "Select a region or node. To stop using a file, remove it under Map sources."
					}
					rg, _ := f.m.Map.Region(mr.region)
					if mr.node >= 0 && len(rg.Nodes) == 1 {
						return false, "A region needs at least one node; delete the region instead."
					}
					return true, ""
				}
				return false, "Nothing to delete here."
			},
			Run: func(ctx module.Context, _ module.Values) tea.Cmd {
				if m.sec == SecSources {
					src, _ := m.curSource()
					list, _ := m.doc.List(src.key)
					list = slices.DeleteFunc(list, func(s string) bool { return s == src.value })
					if err := m.setSources(src.key, list); err != nil {
						return failed("derp.delete", err)
					}
					if src.key == keyPaths {
						m.files = slices.DeleteFunc(m.files, func(f *mapFile) bool { return f.path == src.value })
					}
					return tea.Batch(m.staged(ctx), ok("derp.delete", "Staged removing "+src.value+"."))
				}
				mr, f, _ := m.curMap()
				rg, _ := f.m.Map.Region(mr.region)
				if mr.node < 0 {
					if err := f.m.DeleteRegion(rg.RegionID); err != nil {
						return failed("derp.delete", err)
					}
					f.dirty = true
					return tea.Batch(m.staged(ctx), ok("derp.delete", fmt.Sprintf("Staged deleting region %d.", rg.RegionID)))
				}
				name := rg.Nodes[mr.node].Name
				rg.Nodes = slices.Delete(slices.Clone(rg.Nodes), mr.node, mr.node+1)
				if err := f.m.PutRegion(rg); err != nil {
					return failed("derp.delete", err)
				}
				f.dirty = true
				return tea.Batch(m.staged(ctx), ok("derp.delete", "Staged deleting node "+name+"."))
			},
		},
		m.newFileAction(),
		{
			ID: "derp.refresh", Title: "Refresh the live map", Keys: []string{"r"},
			Enabled: func(ctx module.Context) (bool, string) {
				if ctx.Host() == nil {
					return false, "Needs host mode."
				}
				return true, ""
			},
			Run: func(ctx module.Context, _ module.Values) tea.Cmd { return m.fetchLive(ctx) },
		},
		{
			ID: "derp.diff", Title: "Show diff", Keys: []string{"d"},
			Enabled: func(module.Context) (bool, string) {
				if !m.dirty() {
					return false, "Nothing staged."
				}
				return true, ""
			},
			Run: func(module.Context, module.Values) tea.Cmd { m.diff, m.diffTop = !m.diff, 0; return nil },
		},
		m.applyAction(),
		{
			ID: "derp.discard", Title: "Discard staged changes", Keys: []string{"x"}, Danger: module.DangerConfirm,
			Consequence: "Staged DERP edits are dropped and the files are re-read from disk.",
			Enabled: func(ctx module.Context) (bool, string) {
				if ctx.Host() == nil {
					return false, "Needs host mode."
				}
				if !m.dirty() && m.loadErr == nil {
					return false, "Nothing staged."
				}
				return true, ""
			},
			Run: func(ctx module.Context, _ module.Values) tea.Cmd {
				m.diff = false
				return tea.Batch(m.load(ctx), func() tea.Msg {
					return module.ActionResult{ActionID: "derp.discard", Message: "Discarded staged DERP changes."}
				})
			},
		},
		{
			ID: "derp.rollback", Title: "Roll back the last DERP change", Keys: []string{"u"}, Mutating: true, Danger: module.DangerConfirm,
			Consequence: "Restores the files as they were before the last apply; a new map file is removed.",
			Targets: func(module.Context) []module.Target {
				out := make([]module.Target, 0, len(m.applied))
				for _, a := range m.applied {
					out = append(out, module.Target{Kind: "file", ID: a.File.Path, Name: a.File.Path})
				}
				return out
			},
			Enabled: func(module.Context) (bool, string) {
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
}

func sourceCheck(key string) func(string) error {
	if key == keyPaths {
		return checkAbs
	}
	return checkSourceURL
}

func nextRegionID(m derpmap.Map) int {
	for id := 900; id <= 999; id++ {
		if _, ok := m.Region(id); !ok {
			return id
		}
	}
	return 0
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

func (m *Model) applyAction() module.Action {
	cs := m.changes
	var names []string
	need := host.NeedConfigW
	for _, c := range cs {
		n := c.File.Path
		if c.File.New {
			n += " (new)"
		}
		names = append(names, n)
	}
	return module.Action{
		ID: "derp.apply", Title: "Apply DERP changes", Keys: []string{"ctrl+s"}, Mutating: true, Needs: need,
		Danger:      module.Danger(max(changeset.Danger(cs), host.DangerConfirm)),
		Consequence: "Writes " + strings.Join(names, ", ") + ". DERP changes take effect after headscale restarts; clients then pick up the new map. A backup is kept; u rolls back.",
		Targets: func(module.Context) []module.Target {
			out := make([]module.Target, 0, len(m.changes))
			for _, c := range m.changes {
				out = append(out, module.Target{Kind: "file", ID: c.File.Path, Name: c.File.Path})
			}
			return out
		},
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
				return appliedMsg{applied: as, err: err}
			}
		},
	}
}
