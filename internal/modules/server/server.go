// Package server is the Server page: version, health, API flavour and the
// capability matrix, which says what this server and mode support, plus
// maintenance (backfill IPs). In host mode it adds the doctor, backup
// bundles with restore, and the upgrade assistant.
package server

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tonhe/headsup/internal/api"
	"github.com/tonhe/headsup/internal/config"
	"github.com/tonhe/headsup/internal/host/backup"
	"github.com/tonhe/headsup/internal/host/doctor"
	"github.com/tonhe/headsup/internal/host/upgrade"
	"github.com/tonhe/headsup/internal/icons"
	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/modules/registry"
	"github.com/tonhe/headsup/internal/store"
	"github.com/tonhe/headsup/internal/ui/kv"
	"github.com/tonhe/headsup/internal/ui/pane"
	"github.com/tonhe/headsup/internal/ui/table"
	"github.com/tonhe/headsup/internal/ui/tabs"
)

func init() { registry.Register(func() module.Module { return New() }) }

// Settings is optionally implemented by the app to hand this page the
// [host] configuration (backup directory, retention, doctor thresholds,
// update checks). Without it the built-in defaults apply.
type Settings interface {
	HostSettings() config.Host
}

// simulator is implemented by the in-memory demo host and backend.
type simulator interface{ Simulated() bool }

type tab int

const (
	tabCaps tab = iota
	tabDoctor
	tabBackups
	tabUpgrade
	tabCount
)

var tabNames = [...]string{"Capabilities", "Doctor", "Backups", "Upgrade"}
var tabIDs = [...]string{"caps", "doctor", "backups", "upgrade"}

// Model is the server module.
type Model struct {
	ic  icons.Set
	tab tab

	caps *table.Model

	doc      *table.Model
	findings []doctor.Finding
	docRan   bool
	docBusy  bool
	docAt    time.Time
	docFull  bool // the last run included the clock check

	bks        *table.Model
	bundles    []backup.Bundle
	bkErr      error
	bkBusy     string // "backing up", "restoring"
	last       *backup.Result
	restored   *backup.RestoreResult
	restoredID string
	run        *restoreRun

	plan   *upgrade.Plan
	upErr  error
	upBusy bool
	upTop  int

	// src overrides the release source (tests); nil picks GitHub, or a
	// built-in list on the simulated demo host.
	src upgrade.Source
	// probes overrides the doctor's probes (tests).
	probes *doctor.Probes
	// root overrides the bundle directory (tests).
	root string
	// arch overrides the CPU architecture used in upgrade commands (tests);
	// "" means this machine's.
	arch string
}

// New returns the module.
func New() *Model {
	m := &Model{ic: icons.Unicode}
	return m
}

func (m *Model) ID() string               { return "server" }
func (m *Model) Title() string            { return "Server" }
func (m *Model) Icon(ic icons.Set) string { return ic.Server }
func (m *Model) Order() int               { return 80 }
func (m *Model) Modes() module.Modes      { return module.ModeAPIHost }
func (m *Model) Chord() string            { return "S" }
func (m *Model) Section() module.Section  { return module.SectionServer }
func (m *Model) Capturing() bool          { return false }

// Badge is the number of doctor findings that need attention.
func (m *Model) Badge(*store.Store) string {
	if !m.docRan {
		return ""
	}
	w, e := doctor.Counts(m.findings)
	if w+e == 0 {
		return ""
	}
	return fmt.Sprint(w + e)
}

func (m *Model) ensureTables(ctx module.Context) {
	s := ctx.Styles()
	if m.caps == nil {
		m.caps = table.New(s, m.ic, capColumns())
		m.caps.SetFocused(true)
	}
	if m.doc == nil {
		m.doc = table.New(s, m.ic, doctorColumns())
		m.doc.SetFocused(true)
	}
	if m.bks == nil {
		m.bks = table.New(s, m.ic, backupColumns())
		m.bks.SetFocused(true)
	}
}

func (m *Model) Init(ctx module.Context) tea.Cmd {
	m.ic = ctx.Icons()
	m.ensureTables(ctx)
	m.refreshCaps(ctx)
	if ctx.Host() == nil {
		return nil
	}
	// A quick doctor pass for the badge; the clock check, which contacts an
	// outside server, waits for an explicit run (c).
	m.docBusy = true
	return tea.Batch(m.runDoctor(ctx, false), m.listBackups(ctx))
}

func (m *Model) Update(ctx module.Context, msg tea.Msg) (module.Module, tea.Cmd) {
	m.ensureTables(ctx)
	switch msg := msg.(type) {
	case store.UpdatedMsg:
		m.refreshCaps(ctx)
	case module.NavigateMsg:
		m.refreshCaps(ctx)
	case doctorMsg:
		return m, m.onDoctor(msg)
	case bundlesMsg:
		m.onBundles(msg)
	case backupMsg:
		return m, m.onBackup(msg)
	case restoreMsg:
		return m, m.onRestore(msg)
	case upgradeMsg:
		return m, m.onUpgrade(msg)
	case tea.KeyMsg:
		return m, m.key(ctx, msg)
	}
	return m, nil
}

func (m *Model) key(_ module.Context, k tea.KeyMsg) tea.Cmd {
	switch k.String() {
	case "tab", "right", "l":
		m.tab = (m.tab + 1) % tabCount
		return nil
	case "shift+tab", "left", "h":
		m.tab = (m.tab + tabCount - 1) % tabCount
		return nil
	}
	switch m.tab {
	case tabCaps:
		return m.caps.Update(k)
	case tabDoctor:
		return m.doc.Update(k)
	case tabBackups:
		return m.bks.Update(k)
	case tabUpgrade:
		switch k.String() {
		case "j", "down":
			m.upTop++
		case "k", "up":
			m.upTop = max(0, m.upTop-1)
		case "home":
			m.upTop = 0
		case "pgdown", "ctrl+d":
			m.upTop += 10
		case "pgup", "ctrl+u":
			m.upTop = max(0, m.upTop-10)
		}
	}
	return nil
}

func (m *Model) hostSettings(ctx module.Context) config.Host {
	if s, ok := ctx.(Settings); ok {
		return s.HostSettings()
	}
	return config.Default().Host
}

func simulated(ctx module.Context) bool {
	if h := ctx.Host(); h != nil {
		if s, ok := h.(simulator); ok && s.Simulated() {
			return true
		}
	}
	if b := ctx.Backend(); b != nil {
		if s, ok := b.(simulator); ok && s.Simulated() {
			return true
		}
	}
	return false
}

// --- actions ------------------------------------------------------------------

func (m *Model) Actions() []module.Action {
	acts := []module.Action{
		{
			ID: "server.doctor", Title: "Run the doctor", Keys: []string{"c"},
			Enabled: hostOnly,
			Run: func(ctx module.Context, _ module.Values) tea.Cmd {
				m.tab, m.docBusy = tabDoctor, true
				return m.runDoctor(ctx, true)
			},
		},
		{
			ID: "server.fix", Title: "Go to the fix for this finding", Keys: []string{"f"},
			Enabled: func(ctx module.Context) (bool, string) {
				f, ok := m.finding()
				if !ok || m.tab != tabDoctor {
					return false, "Select a doctor finding first (c runs the doctor)."
				}
				if f.Action == "" {
					return false, "This fix is a command to run yourself; y copies it."
				}
				return true, ""
			},
			Run: func(ctx module.Context, _ module.Values) tea.Cmd {
				f, _ := m.finding()
				return ctx.Navigate(f.Action, nil)
			},
		},
		{
			ID: "server.copyfix", Title: "Copy the fix command", Keys: []string{"y"},
			Enabled: func(module.Context) (bool, string) {
				if f, ok := m.finding(); !ok || m.tab != tabDoctor || f.Fix == "" {
					return false, "Select a doctor finding with a fix first."
				}
				return true, ""
			},
			Run: func(_ module.Context, _ module.Values) tea.Cmd {
				f, _ := m.finding()
				return copyCmd("server.copyfix", f.Fix)
			},
		},
		m.backupAction(),
		{
			ID: "server.backups", Title: "Show backups", Keys: []string{"B"},
			Run: func(ctx module.Context, _ module.Values) tea.Cmd {
				m.tab = tabBackups
				if ctx.Host() == nil {
					return nil
				}
				return m.listBackups(ctx)
			},
		},
		m.restoreAction(),
		{
			ID: "server.upgrade", Title: "Check for upgrades", Keys: []string{"U"},
			Run: func(ctx module.Context, _ module.Values) tea.Cmd {
				m.tab, m.upBusy, m.upTop = tabUpgrade, true, 0
				return m.checkUpgrade(ctx)
			},
		},
		{
			ID: "server.backfill", Title: "Backfill machine IPs", Keys: []string{"F"},
			Mutating: true, Danger: module.DangerCritical, Cap: api.CapBackfillIPs,
			Consequence: "Machines missing an address from a configured prefix get one. Existing addresses are kept.",
			Targets: func(ctx module.Context) []module.Target {
				n := serverName(ctx)
				return []module.Target{{Kind: "server", ID: n, Name: n}}
			},
			Run: func(ctx module.Context, _ module.Values) tea.Cmd {
				b := ctx.Backend()
				return func() tea.Msg {
					c, cancel := api.WithTimeout(bg())
					defer cancel()
					changes, err := b.BackfillIPs(c)
					msg := "Backfill done: no machine needed an address."
					if len(changes) > 0 {
						msg = fmt.Sprintf("Backfill done: %d changes.", len(changes))
					}
					return module.ActionResult{ActionID: "server.backfill", Err: err, Message: msg, Refresh: store.SliceNodes}
				}
			},
		},
		{
			ID: "server.openconfig", Title: "Open the local config in Server Settings", Keys: []string{"o"},
			Enabled: func(ctx module.Context) (bool, string) {
				if h := ctx.Host(); h == nil || h.Env().ConfigPath == "" {
					return false, "No headscale config was found on this machine (host mode only)."
				}
				return true, ""
			},
			Run: func(ctx module.Context, _ module.Values) tea.Cmd { return ctx.Navigate("serverconfig", nil) },
		},
	}
	return acts
}

func hostOnly(ctx module.Context) (bool, string) {
	if ctx.Host() == nil {
		return false, "Available when run on the headscale server (host mode)."
	}
	return true, ""
}

func serverName(ctx module.Context) string {
	if h := ctx.Host(); h != nil {
		if su := h.Env().ServerURL; su != "" {
			if u, err := url.Parse(su); err == nil && u.Hostname() != "" {
				return u.Hostname()
			}
		}
	}
	if u, err := url.Parse(ctx.ServerURL()); err == nil && u.Hostname() != "" {
		return u.Hostname()
	}
	if h := ctx.Host(); h != nil && h.Env().Hostname != "" {
		return h.Env().Hostname
	}
	return "headscale"
}

func (m *Model) Hints() []module.Hint {
	hs := []module.Hint{{Key: "tab", Desc: "next view"}}
	switch m.tab {
	case tabDoctor:
		hs = append(hs, module.Hint{Key: "c", Desc: "run doctor"}, module.Hint{Key: "f", Desc: "go to fix"}, module.Hint{Key: "y", Desc: "copy fix"})
	case tabBackups:
		hs = append(hs, module.Hint{Key: "b", Desc: "back up now"}, module.Hint{Key: "r", Desc: "restore files"})
	case tabUpgrade:
		hs = append(hs, module.Hint{Key: "U", Desc: "check"}, module.Hint{Key: "b", Desc: "back up first"}, module.Hint{Key: "j/k", Desc: "scroll"})
	default:
		hs = append(hs, module.Hint{Key: "c", Desc: "doctor"}, module.Hint{Key: "b", Desc: "backup"}, module.Hint{Key: "U", Desc: "upgrade"}, module.Hint{Key: "F", Desc: "backfill IPs"})
	}
	return hs
}

// --- view -------------------------------------------------------------------

func (m *Model) View(ctx module.Context, r module.Rect) string {
	m.ensureTables(ctx)
	s := ctx.Styles()
	if r.W < 20 || r.H < 8 {
		return ""
	}
	info := m.infoPairs(ctx)
	infoH := min(len(info)+2, max(4, r.H/3))
	iw, _ := pane.Inner(r.W, infoH)
	top := pane.Render(s, "Server", kv.Render(s, info, iw), r.W, infoH, false)

	items := make([]tabs.Item, tabCount)
	for i := range items {
		items[i] = tabs.Item{ID: tabIDs[i], Title: tabNames[i]}
	}
	if b := m.Badge(ctx.Store()); b != "" {
		items[tabDoctor].Badge = b
	}
	if n := len(m.bundles); n > 0 {
		items[tabBackups].Badge = fmt.Sprint(n)
	}
	bar := tabs.Render(s, m.ic, items, tabIDs[m.tab], r.W)

	h := r.H - infoH - 1
	var body string
	switch m.tab {
	case tabDoctor:
		body = m.doctorView(ctx, r.W, h)
	case tabBackups:
		body = m.backupsView(ctx, r.W, h)
	case tabUpgrade:
		body = m.upgradeView(ctx, r.W, h)
	default:
		body = m.capsView(ctx, r.W, h)
	}
	return lipgloss.JoinVertical(lipgloss.Left, top, bar, body)
}

func (m *Model) infoPairs(ctx module.Context) []kv.Pair {
	s, ic := ctx.Styles(), ctx.Icons()
	info := ctx.Store().Info
	ver := info.Version
	if ver == "" {
		ver = s.Muted.Render("unknown")
	} else if api.Supported(info.Version) {
		ver += "  " + s.OK.Render(ic.Check+" supported")
	} else {
		ver += "  " + s.Warn.Render(ic.Warn+" not a supported version (0.29.x, 0.30.x)")
	}
	health := s.Danger.Render(ic.Offline + " not answering")
	if info.Healthy {
		health = s.OK.Render(ic.Online + " healthy")
	}
	if st := ctx.Store(); st.Stale {
		health = s.Warn.Render(ic.Idle + " stale (reconnecting)")
	}
	mode := "remote mode"
	flavour := "REST"
	if u, err := url.Parse(ctx.ServerURL()); err == nil && u.Scheme != "" {
		flavour += " over " + strings.ToUpper(u.Scheme)
	}
	pairs := []kv.Pair{{Key: "Version", Value: ver}, {Key: "Health", Value: health}}
	if h := ctx.Host(); h != nil {
		env := h.Env()
		mode = "host mode"
		flavour = env.Transport.String()
		pairs = append(pairs, kv.Pair{Key: "API", Value: flavour + s.Muted.Render(" "+ic.Bullet+" "+mode)})
		su := env.ServerURL
		if su == "" {
			su = ctx.ServerURL()
		}
		pairs = append(pairs, kv.Pair{Key: "URL", Value: su})
		if env.ConfigPath != "" {
			pairs = append(pairs, kv.Pair{Key: "Config", Value: env.ConfigPath + s.Muted.Render("  (o opens Server Settings)")})
		} else {
			pairs = append(pairs, kv.Pair{Key: "Config", Value: s.Muted.Render("not found on this machine")})
		}
		return pairs
	}
	pairs = append(pairs, kv.Pair{Key: "API", Value: flavour + s.Muted.Render(" "+ic.Bullet+" "+mode)},
		kv.Pair{Key: "URL", Value: ctx.ServerURL()})
	return pairs
}

// hostView is shown on host-only tabs in remote mode.
func hostView(ctx module.Context, title, what string, w, h int) string {
	s := ctx.Styles()
	iw, _ := pane.Inner(w, h)
	lines := []string{
		s.Muted.Render(what + " needs host mode: run this tool on the headscale server itself."),
		"",
		s.Muted.Render("Host mode reads the config, database and service directly, so it can check and back up what the API cannot reach."),
	}
	for i, l := range lines {
		lines[i] = pane.Fit(l, iw)
	}
	return pane.Render(s, title, strings.Join(lines, "\n"), w, h, true)
}

// wrap breaks plain text into lines of at most w cells.
func wrap(text string, w int) []string {
	if w <= 0 {
		return nil
	}
	var out []string
	for _, para := range strings.Split(text, "\n") {
		line := ""
		var words []string
		for _, word := range strings.Fields(para) {
			// Hard-break words longer than a line (URLs, paths).
			for lipgloss.Width(word) > w {
				r := []rune(word)
				n := min(len(r), w)
				words = append(words, string(r[:n]))
				word = string(r[n:])
			}
			if word != "" {
				words = append(words, word)
			}
		}
		for _, word := range words {
			switch {
			case line == "":
				line = word
			case lipgloss.Width(line)+1+lipgloss.Width(word) <= w:
				line += " " + word
			default:
				out = append(out, line)
				line = word
			}
		}
		out = append(out, line)
	}
	return out
}
