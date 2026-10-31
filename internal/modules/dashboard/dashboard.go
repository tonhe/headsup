// Package dashboard is the landing page: tailnet health at a glance, live
// server metrics in host mode, and a list of things that need attention.
package dashboard

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tonhe/headsup/internal/api"
	"github.com/tonhe/headsup/internal/icons"
	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/modules/registry"
	"github.com/tonhe/headsup/internal/store"
	"github.com/tonhe/headsup/internal/ui/badge"
	"github.com/tonhe/headsup/internal/ui/humanize"
	"github.com/tonhe/headsup/internal/ui/pane"
	"github.com/tonhe/headsup/internal/ui/sparkline"
	"github.com/tonhe/headsup/internal/ui/table"
)

func init() { registry.Register(func() module.Module { return New() }) }

const expiryWarn = 7 * 24 * time.Hour

// Issue is one thing on the dashboard that needs attention.
type Issue struct {
	Severity int // 0 info, 1 warn, 2 error
	Text     string
	Module   string
	Args     any
}

// Model is the dashboard module.
type Model struct {
	issues []Issue
	cursor int
}

// New returns the module.
func New() *Model { return &Model{} }

func (m *Model) ID() string                  { return "dashboard" }
func (m *Model) Title() string               { return "Dashboard" }
func (m *Model) Icon(ic icons.Set) string    { return ic.Dashboard }
func (m *Model) Order() int                  { return 10 }
func (m *Model) Modes() module.Modes         { return module.ModeAPIHost }
func (m *Model) Chord() string               { return "h" }
func (m *Model) Capturing() bool             { return false }
func (m *Model) Init(module.Context) tea.Cmd { return nil }

func (m *Model) Badge(st *store.Store) string {
	if n := len(m.issues); n > 0 {
		return fmt.Sprint(n)
	}
	return ""
}

func (m *Model) Hints() []module.Hint {
	return []module.Hint{{Key: "j/k", Desc: "move"}, {Key: "enter", Desc: "go to issue"}}
}

func (m *Model) Update(ctx module.Context, msg tea.Msg) (module.Module, tea.Cmd) {
	switch msg := msg.(type) {
	case store.UpdatedMsg:
		m.issues = Issues(ctx.Store(), ctx.Now(), expiryWarn)
		m.cursor = min(m.cursor, max(0, len(m.issues)-1))
	case tea.KeyMsg:
		switch msg.String() {
		case "j", "down":
			m.cursor = min(m.cursor+1, max(0, len(m.issues)-1))
		case "k", "up":
			m.cursor = max(m.cursor-1, 0)
		}
	}
	return m, nil
}

func (m *Model) Actions() []module.Action {
	return []module.Action{{
		ID: "dashboard.open", Title: "Go to issue", Keys: []string{"enter"},
		Enabled: func(module.Context) (bool, string) {
			if len(m.issues) == 0 {
				return false, "Nothing needs attention."
			}
			return true, ""
		},
		Run: func(ctx module.Context, _ module.Values) tea.Cmd {
			is := m.issues[m.cursor]
			return ctx.Navigate(is.Module, is.Args)
		},
	}}
}

// Issues derives the attention list from the store. Exported for tests.
func Issues(st *store.Store, now time.Time, warn time.Duration) []Issue {
	var out []Issue
	for _, n := range st.Nodes {
		if p := n.PendingRoutes(); len(p) > 0 {
			what := fmt.Sprintf("%d route(s)", len(p))
			if n.IsExitNode() && !n.ExitApproved() {
				what = "exit node use"
			}
			out = append(out, Issue{1, fmt.Sprintf("%s is waiting for approval of %s", n.DisplayName(), what), "machines", module.SelectArgs{ID: n.ID}})
		}
	}
	for _, r := range st.Registrations {
		name := r.Hostname
		if name == "" {
			name = r.AuthID
		}
		out = append(out, Issue{1, fmt.Sprintf("%s is waiting to join the tailnet", name), "pending", nil})
	}
	if len(st.Registrations) == 0 && st.RegCount > 0 {
		out = append(out, Issue{1, fmt.Sprintf("%d device(s) are waiting to log in", st.RegCount), "pending", nil})
	}
	for _, n := range st.Nodes {
		switch {
		case n.Expired(now):
			out = append(out, Issue{2, fmt.Sprintf("%s has expired and must re-authenticate", n.DisplayName()), "machines", module.SelectArgs{ID: n.ID}})
		case n.ExpiresSoon(now, warn):
			out = append(out, Issue{1, fmt.Sprintf("%s expires %s", n.DisplayName(), humanize.In(*n.Expiry, now)), "machines", module.SelectArgs{ID: n.ID}})
		}
	}
	for _, k := range st.PreAuthKeys {
		if k.State(now) == api.KeyActive && !k.Expiration.IsZero() && k.Expiration.Sub(now) < warn {
			owner := "tag-only"
			if k.User != nil {
				owner = k.User.Name
			}
			out = append(out, Issue{0, fmt.Sprintf("Pre-auth key for %s expires %s", owner, humanize.In(k.Expiration, now)), "pending", nil})
		}
	}
	for _, k := range st.APIKeys {
		if !k.Expiration.IsZero() && k.Expiration.After(now) && k.Expiration.Sub(now) < warn {
			out = append(out, Issue{1, fmt.Sprintf("API key %s expires %s", k.Prefix, humanize.In(k.Expiration, now)), "dashboard", nil})
		}
	}
	return out
}

// --- view -------------------------------------------------------------------

func (m *Model) View(ctx module.Context, r module.Rect) string {
	s, ic := ctx.Styles(), ctx.Icons()
	st := ctx.Store()
	now := ctx.Now()
	c := st.Counts(now, expiryWarn)
	ascii := ic.Name == "ascii"

	cardW := max(18, (r.W-2)/4)
	cardH := 6
	spark := func(vals []float64) string {
		if len(vals) < 2 {
			return s.Muted.Render("collecting…")
		}
		return sparkline.Render(s, vals, cardW-4, ascii)
	}
	big := func(v string) string { return s.Strong.Render(v) }

	onlineCard := pane.Render(s, "Machines",
		big(fmt.Sprintf("%d / %d", c.Online, c.Nodes))+s.Muted.Render(" online")+"\n"+spark(st.OnlineHistory.Values()),
		cardW, cardH, false)

	userLine := big(fmt.Sprint(c.Users)) + s.Muted.Render(" users")
	usersCard := pane.Render(s, "Users", userLine+"\n"+s.Muted.Render(fmt.Sprintf("%d exit node(s)", c.ExitNodes)), cardW, cardH, false)

	pendStyle := s.OK
	if c.PendingRoutes+c.PendingRegistrations > 0 {
		pendStyle = s.Pending
	}
	pendCard := pane.Render(s, "Waiting",
		pendStyle.Render(fmt.Sprintf("%d", c.PendingRoutes))+s.Muted.Render(" routes")+"\n"+
			pendStyle.Render(fmt.Sprintf("%d", c.PendingRegistrations))+s.Muted.Render(" registrations"),
		cardW, cardH, false)

	expStyle := s.OK
	if c.ExpiringNodes+c.ExpiringKeys > 0 {
		expStyle = s.Expiring
	}
	if c.ExpiredNodes > 0 {
		expStyle = s.Expired
	}
	expCard := pane.Render(s, "Expiry",
		expStyle.Render(fmt.Sprintf("%d", c.ExpiringNodes))+s.Muted.Render(" machines soon")+"\n"+
			expStyle.Render(fmt.Sprintf("%d", c.ExpiringKeys))+s.Muted.Render(" keys soon")+"\n"+
			s.Expired.Render(fmt.Sprintf("%d", c.ExpiredNodes))+s.Muted.Render(" expired"),
		r.W-3*cardW, cardH, false)

	cards := lipgloss.JoinHorizontal(lipgloss.Top, onlineCard, usersCard, pendCard, expCard)

	// Server row: version/health, plus live metrics in host mode.
	health := s.OK.Render(ic.Online + " healthy")
	if !st.Info.Healthy {
		health = s.Danger.Render(ic.Offline + " unhealthy")
	}
	if st.Stale {
		health = s.Warn.Render(ic.Warn + " unreachable, showing last data")
	}
	srvBody := fmt.Sprintf("headscale %s  %s", s.Strong.Render(orUnknown(st.Info.Version)), health)
	if !st.UpdatedAt.IsZero() {
		srvBody += "\n" + s.Muted.Render("updated "+humanize.Ago(st.UpdatedAt, now))
	}
	if st.Service != nil {
		p := badge.ServicePhaseOf(st.Service.State, st.Service.Running, "")
		srvBody += s.Muted.Render("  service ") + badge.Service(s, ic, p, p.String())
	}
	srvRowH := 4
	var srvRow string
	if ctx.HostMode() {
		half := r.W / 2
		srv := pane.Render(s, "Server", srvBody, half, srvRowH, false)
		rate := store.Rates(st.Metrics.Values())
		met := pane.Render(s, "Map responses / poll", spark(rate), r.W-half, srvRowH, false)
		srvRow = lipgloss.JoinHorizontal(lipgloss.Top, srv, met)
	} else {
		srvRow = pane.Render(s, "Server", srvBody, r.W, srvRowH, false)
	}

	issuesH := r.H - cardH - srvRowH
	issues := pane.Render(s, fmt.Sprintf("Needs attention %s %d", ic.Bullet, len(m.issues)),
		m.issuesView(ctx, r.W-4, issuesH-2), r.W, issuesH, true)

	return lipgloss.JoinVertical(lipgloss.Left, cards, srvRow, issues)
}

func (m *Model) issuesView(ctx module.Context, w, h int) string {
	s, ic := ctx.Styles(), ctx.Icons()
	if len(m.issues) == 0 {
		return s.OK.Render(ic.Check + " All clear: nothing needs attention.")
	}
	start := 0
	if m.cursor >= h {
		start = m.cursor - h + 1
	}
	var lines []string
	for i := start; i < len(m.issues) && len(lines) < h; i++ {
		is := m.issues[i]
		glyph := s.Muted.Render(ic.Bullet)
		switch is.Severity {
		case 1:
			glyph = s.Warn.Render(ic.Warn)
		case 2:
			glyph = s.Danger.Render(ic.Cross)
		}
		line := glyph + " " + is.Text
		if i == m.cursor {
			line = s.RowSel.Render(table.Fit(ic.Arrow+" "+is.Text, w, lipgloss.Left, ic.Ellipsis))
		} else {
			line = table.Fit(line, w, lipgloss.Left, ic.Ellipsis)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func orUnknown(v string) string {
	if v == "" {
		return "?"
	}
	return v
}
