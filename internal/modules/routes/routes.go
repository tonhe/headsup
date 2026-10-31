// Package routes is the tailnet-wide Routes page: every advertised subnet
// route grouped by prefix (HA sets), overlap highlighting, a separate exit
// node section, and approve, revoke and approve-all. In host mode the
// server's own route state marks each HA set's primary and failovers.
package routes

import (
	"context"
	"fmt"
	"net/netip"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tonhe/headsup/internal/api"
	"github.com/tonhe/headsup/internal/icons"
	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/modules/registry"
	"github.com/tonhe/headsup/internal/store"
	"github.com/tonhe/headsup/internal/ui/empty"
	"github.com/tonhe/headsup/internal/ui/pane"
	"github.com/tonhe/headsup/internal/ui/styles"
	"github.com/tonhe/headsup/internal/ui/table"
)

func init() { registry.Register(func() module.Module { return New() }) }

// Model is the routes module.
type Model struct {
	subTbl    *table.Model
	exitTbl   *table.Model
	focusExit bool
	data      entries
	ready     bool
	hostMode  bool
}

// New returns the module.
func New() *Model { return &Model{} }

func (m *Model) ID() string               { return "routes" }
func (m *Model) Title() string            { return "Routes" }
func (m *Model) Icon(ic icons.Set) string { return ic.Route }
func (m *Model) Order() int               { return 24 }
func (m *Model) Modes() module.Modes      { return module.ModeAPIHost }
func (m *Model) Chord() string            { return "r" }
func (m *Model) Capturing() bool          { return false }

// Badge is the number of route approvals waiting (T116).
func (m *Model) Badge(st *store.Store) string {
	if n := PendingCount(st); n > 0 {
		return strconv.Itoa(n)
	}
	return ""
}

func newTable(ctx module.Context, cols []table.Column) *table.Model {
	t := table.New(ctx.Styles(), ctx.Icons(), cols)
	t.SetMulti(false)
	// Rows are grouped by prefix; re-sorting would break the HA groups.
	t.Keys.SortNext, t.Keys.SortReverse = key.Binding{}, key.Binding{}
	return t
}

func (m *Model) Init(ctx module.Context) tea.Cmd {
	m.subTbl = newTable(ctx, subnetColumns())
	m.exitTbl = newTable(ctx, exitColumns())
	m.setFocus(false)
	return nil
}

func (m *Model) setFocus(exit bool) {
	m.focusExit = exit
	m.subTbl.SetFocused(!exit)
	m.exitTbl.SetFocused(exit)
}

func (m *Model) rebuild(ctx module.Context) {
	m.hostMode = ctx.HostMode()
	m.data = buildEntries(ctx.Store(), m.hostMode)
	toRows := func(es []entry) []table.Row {
		out := make([]table.Row, len(es))
		for i, e := range es {
			out[i] = row{e: e, s: ctx.Styles(), ic: ctx.Icons(), host: m.hostMode}
		}
		return out
	}
	m.subTbl.SetRows(toRows(m.data.subnets))
	m.exitTbl.SetRows(toRows(m.data.exits))
	m.ready = ctx.Store().Loaded&store.SliceNodes != 0
	if m.focusExit && len(m.data.exits) == 0 {
		m.setFocus(false)
	}
}

func (m *Model) Update(ctx module.Context, msg tea.Msg) (module.Module, tea.Cmd) {
	switch msg := msg.(type) {
	case store.UpdatedMsg:
		m.rebuild(ctx)
	case module.NavigateMsg:
		m.rebuild(ctx)
	case tea.KeyMsg:
		if msg.String() == "tab" || msg.String() == "shift+tab" {
			m.setFocus(!m.focusExit && len(m.data.exits) > 0)
			return m, nil
		}
		return m, m.active().Update(msg)
	}
	return m, nil
}

func (m *Model) active() *table.Model {
	if m.focusExit {
		return m.exitTbl
	}
	return m.subTbl
}

func (m *Model) cursor() (entry, bool) {
	if m.subTbl == nil {
		return entry{}, false
	}
	r, ok := m.active().Cursor()
	if !ok {
		return entry{}, false
	}
	return r.(row).e, true
}

func (m *Model) Hints() []module.Hint {
	return []module.Hint{
		{Key: "a", Desc: "approve"}, {Key: "x", Desc: "revoke"}, {Key: "A", Desc: "approve all for machine"},
		{Key: "tab", Desc: "subnets/exit nodes"}, {Key: "enter", Desc: "machine"},
	}
}

// --- view -------------------------------------------------------------------

func (m *Model) View(ctx module.Context, r module.Rect) string {
	s, ic := ctx.Styles(), ctx.Icons()
	if m.subTbl == nil {
		return ""
	}
	if !m.ready {
		return empty.Render(s, "Loading routes…", "", "", r.W, r.H)
	}
	if len(m.data.subnets) == 0 && len(m.data.exits) == 0 {
		return empty.Render(s, "No routes advertised",
			"Machines offer subnets with tailscale up --advertise-routes, and exit nodes with --advertise-exit-node.", "", r.W, r.H)
	}
	exitH := 4
	if n := len(m.data.exits); n > 0 {
		exitH = min(n+3, max(5, r.H/3))
	}
	subH := r.H - exitH

	sub := m.section(s, ic, "Subnet routes", m.subTbl, m.data.subnets, !m.focusExit, r.W, subH,
		"No subnet routes advertised.")
	exit := m.section(s, ic, "Exit nodes", m.exitTbl, m.data.exits, m.focusExit, r.W, exitH,
		"No exit nodes. A machine offers one with tailscale up --advertise-exit-node.")
	return lipgloss.JoinVertical(lipgloss.Left, sub, exit)
}

func (m *Model) section(s *styles.Styles, ic icons.Set, title string, t *table.Model, es []entry, focused bool, w, h int, none string) string {
	pend := 0
	for _, e := range es {
		if e.state == statePending {
			pend++
		}
	}
	title = fmt.Sprintf("%s %s %d", title, ic.Bullet, len(es))
	var opts []pane.Opt
	if pend > 0 {
		opts = append(opts, pane.WithExtra(s.Pending.Render(fmt.Sprintf("%d pending", pend))))
	}
	iw, ih := pane.Inner(w, h)
	body := s.Muted.Render(none)
	if len(es) > 0 {
		t.SetSize(iw, ih)
		body = t.View()
	}
	return pane.Render(s, title, body, w, h, focused, opts...)
}

// --- actions ----------------------------------------------------------------

func call(f func(context.Context) error) error {
	ctx, cancel := api.WithTimeout(context.Background())
	defer cancel()
	return f(ctx)
}

func label(e entry) string {
	if e.exit {
		return e.node.DisplayName() + " exit node"
	}
	return e.node.DisplayName() + " " + e.prefix.String()
}

func (m *Model) setRoutes(ctx module.Context, id string, n api.Node, routes []netip.Prefix, msg string) tea.Cmd {
	b := ctx.Backend()
	return func() tea.Msg {
		err := call(func(c context.Context) error { _, err := b.SetApprovedRoutes(c, n.ID, routes); return err })
		return module.ActionResult{ActionID: id, Err: err, Refresh: store.SliceNodes | store.SliceHost, Message: msg}
	}
}

func (m *Model) Actions() []module.Action {
	target := func(module.Context) []module.Target {
		if e, ok := m.cursor(); ok {
			return []module.Target{{Kind: "route", ID: e.key(), Name: label(e)}}
		}
		return nil
	}
	nodeTarget := func(module.Context) []module.Target {
		if e, ok := m.cursor(); ok {
			return []module.Target{{Kind: "node", ID: strconv.FormatUint(e.node.ID, 10), Name: e.node.DisplayName()}}
		}
		return nil
	}
	approveWhat := "Approved routes are reachable by every client the policy allows."
	if e, ok := m.cursor(); ok && e.exit {
		approveWhat = "Clients the policy allows can send all their internet traffic through this machine."
	}
	return []module.Action{
		{
			ID: "routes.approve", Title: "Approve route", Keys: []string{"a"}, Scope: module.ScopeSelection,
			Mutating: true, Danger: module.DangerConfirm, Targets: target, Consequence: approveWhat,
			Enabled: func(module.Context) (bool, string) {
				e, ok := m.cursor()
				switch {
				case !ok:
					return false, "No route selected."
				case e.state != statePending:
					return false, label(e) + " is already approved."
				}
				return true, ""
			},
			Run: func(ctx module.Context, _ module.Values) tea.Cmd {
				e, _ := m.cursor()
				// SetApprovedRoutes replaces the list: send every existing
				// approval plus this one, and nothing else (T114).
				return m.setRoutes(ctx, "routes.approve", e.node, withAdded(e.node.ApprovedRoutes, e.routes()), "Approved "+label(e)+".")
			},
		},
		{
			ID: "routes.revoke", Title: "Revoke route", Keys: []string{"x"}, Scope: module.ScopeSelection,
			Mutating: true, Danger: module.DangerConfirm, Targets: target,
			Consequence: "Clients stop reaching this route through this machine.",
			Enabled: func(module.Context) (bool, string) {
				e, ok := m.cursor()
				switch {
				case !ok:
					return false, "No route selected."
				case e.state == statePending:
					return false, label(e) + " is not approved."
				}
				return true, ""
			},
			Run: func(ctx module.Context, _ module.Values) tea.Cmd {
				e, _ := m.cursor()
				return m.setRoutes(ctx, "routes.revoke", e.node, without(e.node.ApprovedRoutes, e.routes()), "Revoked "+label(e)+".")
			},
		},
		{
			ID: "routes.approveall", Title: "Approve all routes for this machine", Keys: []string{"A"},
			Scope: module.ScopeSelection, Mutating: true, Danger: module.DangerConfirm, Targets: nodeTarget,
			Consequence: "Every route and exit node this machine advertises becomes reachable.",
			Enabled: func(module.Context) (bool, string) {
				e, ok := m.cursor()
				switch {
				case !ok:
					return false, "No route selected."
				case len(e.node.PendingRoutes()) == 0:
					return false, e.node.DisplayName() + " has no routes waiting."
				}
				return true, ""
			},
			Run: func(ctx module.Context, _ module.Values) tea.Cmd {
				e, _ := m.cursor()
				pend := e.node.PendingRoutes()
				return m.setRoutes(ctx, "routes.approveall", e.node, withAdded(e.node.ApprovedRoutes, pend),
					fmt.Sprintf("Approved %d route(s) on %s.", len(pend), e.node.DisplayName()))
			},
		},
		{
			ID: "routes.machine", Title: "Open the machine", Keys: []string{"enter"},
			Enabled: func(module.Context) (bool, string) {
				if _, ok := m.cursor(); !ok {
					return false, "No route selected."
				}
				return true, ""
			},
			Run: func(ctx module.Context, _ module.Values) tea.Cmd {
				e, _ := m.cursor()
				return ctx.Navigate("machines", module.SelectArgs{ID: e.node.ID})
			},
		},
	}
}

// --- rows -------------------------------------------------------------------

func subnetColumns() []table.Column {
	return []table.Column{
		{Key: "status", Title: "", Width: 1},
		{Key: "prefix", Title: "PREFIX", Width: 18, Priority: 0},
		{Key: "machine", Title: "MACHINE", Width: 16, MinWidth: 8, Flex: true, Priority: 0},
		{Key: "user", Title: "USER", Width: 8, Priority: 4},
		{Key: "state", Title: "STATE", Width: 10, Priority: 1},
		{Key: "ha", Title: "HA", Width: 10, Priority: 2},
		{Key: "note", Title: "NOTE", Width: 22, Flex: true, Priority: 3},
	}
}

func exitColumns() []table.Column {
	return []table.Column{
		{Key: "status", Title: "", Width: 1},
		{Key: "machine", Title: "MACHINE", Width: 18, MinWidth: 8, Flex: true, Priority: 0},
		{Key: "user", Title: "USER", Width: 8, Priority: 3},
		{Key: "state", Title: "STATE", Width: 10, Priority: 1},
		{Key: "note", Title: "NOTE", Width: 22, Flex: true, Priority: 2},
	}
}

type row struct {
	e    entry
	s    *styles.Styles
	ic   icons.Set
	host bool
}

func (r row) Key() string { return r.e.key() }

func (r row) Cell(col string) string {
	e, s, ic := r.e, r.s, r.ic
	switch col {
	case "status":
		if e.node.Online {
			return s.Online.Render(ic.Online)
		}
		return s.Offline.Render(ic.Offline)
	case "prefix":
		if !e.first {
			return ""
		}
		if len(e.overlaps) > 0 {
			return s.Warn.Render(e.prefix.String())
		}
		return s.IP.Render(e.prefix.String())
	case "machine":
		return e.node.DisplayName()
	case "user":
		return e.node.User.Name
	case "state":
		switch e.state {
		case stateServed:
			return s.OK.Render(ic.Check + " served")
		case stateApproved:
			return s.Muted.Render(ic.Check + " approved")
		default:
			return s.Pending.Render(ic.Pending + " pending")
		}
	case "ha":
		switch e.role {
		case rolePrimary:
			return s.Accent.Render(ic.Primary + " primary")
		case roleFailover:
			return "failover"
		case roleStandby:
			return s.Muted.Render("standby")
		}
		if e.haSize > 1 {
			return fmt.Sprintf("HA %s %d", ic.Bullet, e.haSize)
		}
		return s.Muted.Render("-")
	case "note":
		var notes []string
		if len(e.overlaps) > 0 && e.first {
			ps := make([]string, len(e.overlaps))
			for i, p := range e.overlaps {
				ps[i] = p.String()
			}
			notes = append(notes, s.Warn.Render(ic.Warn+" overlaps "+strings.Join(ps, ", ")))
		}
		if !e.node.Online {
			notes = append(notes, s.Muted.Render("machine offline"))
		}
		if e.exit && len(e.routes()) < len(exitRoutes) {
			notes = append(notes, s.Muted.Render("advertises only "+e.routes()[0].String()))
		}
		return strings.Join(notes, "  ")
	}
	return ""
}

func (r row) SortValue(col string) string { return r.e.key() }
