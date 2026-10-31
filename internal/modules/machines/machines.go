// Package machines is the Machines page: a filterable, sortable table of every
// node with a detail pane and the per-node and bulk admin actions.
package machines

import (
	"context"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tonhe/headsup/internal/api"
	"github.com/tonhe/headsup/internal/clip"
	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/icons"
	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/modules/registry"
	"github.com/tonhe/headsup/internal/store"
	"github.com/tonhe/headsup/internal/ui/empty"
	"github.com/tonhe/headsup/internal/ui/filter"
	"github.com/tonhe/headsup/internal/ui/humanize"
	"github.com/tonhe/headsup/internal/ui/kv"
	"github.com/tonhe/headsup/internal/ui/pane"
	"github.com/tonhe/headsup/internal/ui/table"
)

func init() { registry.Register(func() module.Module { return New() }) }

// ExpiryWarn is how far ahead an expiry is highlighted.
const ExpiryWarn = 7 * 24 * time.Hour

// Model is the machines module.
type Model struct {
	tbl      *table.Model
	bar      *filter.Bar
	shown    int         // rows passing the filter
	all      []table.Row // every node's row, rebuilt when the store changes
	detail   bool
	hostCols bool
	ready    bool
}

// New returns the module.
func New() *Model { return &Model{detail: true} }

func (m *Model) ID() string                { return "machines" }
func (m *Model) Title() string             { return "Machines" }
func (m *Model) Icon(ic icons.Set) string  { return ic.Machine }
func (m *Model) Order() int                { return 20 }
func (m *Model) Modes() module.Modes       { return module.ModeAPIHost }
func (m *Model) Chord() string             { return "m" }
func (m *Model) Section() module.Section   { return module.SectionTailnet }
func (m *Model) Capturing() bool           { return m.bar != nil && m.bar.Active() }
func (m *Model) Badge(*store.Store) string { return "" }

func (m *Model) Init(ctx module.Context) tea.Cmd {
	m.hostCols = ctx.HostMode()
	m.tbl = table.New(ctx.Styles(), ctx.Icons(), columns(m.hostCols))
	m.tbl.SetMulti(true)
	m.tbl.SetFocused(true)
	m.tbl.SetSort("name", false)
	m.bar = filter.NewBar(ctx.Styles(), ctx.Icons())
	return nil
}

// rebuild re-reads every node from the store, then refilters.
func (m *Model) rebuild(ctx module.Context) {
	rows := newRows(ctx.Store(), ctx.Styles(), ctx.Icons(), ctx.Now(), ExpiryWarn)
	m.all = make([]table.Row, len(rows))
	for i, r := range rows {
		m.all[i] = r
	}
	m.refilter(ctx)
}

// refilter applies the filter bar's query to the cached rows. A filter
// keystroke only needs this, not a rebuild of every row (T170).
func (m *Model) refilter(ctx module.Context) {
	if m.all == nil {
		m.rebuild(ctx)
		return
	}
	all := m.all
	q := m.bar.Query()
	trows := all
	if !q.Empty() {
		trows = make([]table.Row, 0, len(all))
		for _, r := range all {
			if q.Match(r.(row)) {
				trows = append(trows, r)
			}
		}
	}
	m.shown = len(trows)
	m.tbl.SetRows(trows)
	m.bar.SetCounts(len(trows), len(all))
	m.ready = ctx.Store().Loaded&store.SliceNodes != 0
}

func (m *Model) Update(ctx module.Context, msg tea.Msg) (module.Module, tea.Cmd) {
	switch msg := msg.(type) {
	case module.NavigateMsg:
		switch a := msg.Args.(type) {
		case module.FilterArgs:
			m.bar.SetValue(a.Query)
			m.rebuild(ctx)
		case module.SelectArgs:
			m.bar.SetValue("")
			m.rebuild(ctx)
			m.tbl.SetCursorKey(strconv.FormatUint(a.ID, 10))
		}
		return m, nil
	case filter.ChangedMsg:
		m.refilter(ctx)
		return m, nil
	case tea.KeyMsg:
		if m.bar.Active() || msg.String() == "/" {
			cmd := m.bar.Update(msg)
			return m, cmd
		}
		if msg.String() == "esc" && m.bar.Value() != "" {
			return m, m.bar.Clear()
		}
		return m, m.tbl.Update(msg)
	case store.UpdatedMsg:
		m.rebuild(ctx)
	}
	return m, nil
}

func (m *Model) cursor() (api.Node, bool) {
	r, ok := m.tbl.Cursor()
	if !ok {
		return api.Node{}, false
	}
	return r.(row).n, true
}

// targetsNodes returns the multi-selection, or the cursor node.
func (m *Model) targetNodes(ctx module.Context) []api.Node {
	var out []api.Node
	for _, k := range m.tbl.Targets() {
		id, _ := strconv.ParseUint(k, 10, 64)
		if n, ok := ctx.Store().Node(id); ok {
			out = append(out, n)
		}
	}
	return out
}

func toTargets(ns []api.Node) []module.Target {
	out := make([]module.Target, len(ns))
	for i, n := range ns {
		out[i] = module.Target{Kind: "node", ID: strconv.FormatUint(n.ID, 10), Name: n.DisplayName()}
	}
	return out
}

func (m *Model) Hints() []module.Hint {
	if m.bar != nil && m.bar.Active() {
		return []module.Hint{{Key: "enter", Desc: "keep filter"}, {Key: "esc", Desc: "clear"}}
	}
	return []module.Hint{
		{Key: "/", Desc: "filter"}, {Key: "enter", Desc: "detail"}, {Key: "r", Desc: "rename"},
		{Key: "t", Desc: "tags"}, {Key: "a", Desc: "routes"}, {Key: "e", Desc: "expire"},
		{Key: "D", Desc: "delete"}, {Key: "space", Desc: "select"},
	}
}

// --- view ---------------------------------------------------------------

func (m *Model) View(ctx module.Context, r module.Rect) string {
	s := ctx.Styles()
	if m.tbl == nil {
		return ""
	}
	if !m.ready {
		return empty.Render(s, "Loading machines…", "", "", r.W, r.H)
	}
	if len(ctx.Store().Nodes) == 0 {
		return empty.Render(s, "No machines yet", "Add one from Add device (1 p) or with a pre-auth key.", "", r.W, r.H)
	}
	showBar := m.bar.Active() || m.bar.Value() != ""
	h := r.H
	if showBar {
		h--
	}
	n, hasCur := m.cursor()

	split := ""
	listW, listH := r.W, h
	switch {
	case m.detail && hasCur && r.W >= 140:
		listW = r.W * 3 / 5
		split = "right"
	case m.detail && hasCur && h >= 30:
		listH = h - 14
		split = "below"
	}

	title := fmt.Sprintf("Machines %s %d", ctx.Icons().Bullet, m.shown)
	if sel := len(m.tbl.Selected()); sel > 0 {
		title += fmt.Sprintf(" %s %d selected", ctx.Icons().Bullet, sel)
	}
	iw, ih := pane.Inner(listW, listH)
	m.tbl.SetSize(iw, ih)
	list := pane.Render(s, title, m.tbl.View(), listW, listH, true)

	var body string
	switch split {
	case "right":
		det := m.detailView(ctx, n, r.W-listW, h)
		body = lipgloss.JoinHorizontal(lipgloss.Top, list, det)
	case "below":
		det := m.detailView(ctx, n, r.W, h-listH)
		body = lipgloss.JoinVertical(lipgloss.Left, list, det)
	default:
		body = list
	}
	if !showBar {
		return body
	}
	return lipgloss.JoinVertical(lipgloss.Left, m.bar.View(r.W), body)
}

func (m *Model) detailView(ctx module.Context, n api.Node, w, h int) string {
	s, ic := ctx.Styles(), ctx.Icons()
	now := ctx.Now()
	var ips []string
	for _, ip := range n.IPAddresses {
		ips = append(ips, s.IP.Render(ip.String()))
	}
	expiry := "never"
	if !n.ExpiryDisabled() {
		expiry = n.Expiry.Format("2006-01-02 15:04") + " (" + humanize.In(*n.Expiry, now) + ")"
	}
	seen := humanize.Ago(n.LastSeen, now)
	if n.Online {
		seen = s.Online.Render("online now")
	}
	pairs := []kv.Pair{
		{Key: "Name", Value: n.DisplayName()},
		{Key: "Hostname", Value: n.Name},
		{Key: "User", Value: n.User.Name},
		{Key: "IPs", Value: strings.Join(ips, "  ")},
		{Key: "Tags", Value: orDash(strings.Join(n.Tags, " "))},
		{Key: "Last seen", Value: seen},
		{Key: "Expiry", Value: expiry},
		{Key: "Created", Value: n.CreatedAt.Format("2006-01-02")},
		{Key: "Registered", Value: string(n.RegisterMethod)},
		{Key: "Routes", Value: m.routesLine(ctx, n)},
	}
	if e, ok := ctx.Store().Extra(n.ID); ok {
		pairs = append(pairs,
			kv.Pair{Key: "OS", Value: strings.TrimSpace(e.OS + " " + e.OSVersion)},
			kv.Pair{Key: "Client", Value: e.ClientVersion},
			kv.Pair{Key: "DERP", Value: orDash(derpLabel(e))},
		)
		if len(e.Endpoints) > 0 {
			var eps []string
			for _, ep := range e.Endpoints {
				eps = append(eps, ep.String())
			}
			pairs = append(pairs, kv.Pair{Key: "Endpoints", Value: strings.Join(eps, " ")})
		}
	}
	pairs = append(pairs, kv.Pair{Key: "Node key", Value: s.Muted.Render(shortKey(n.NodeKey))})
	extra := ""
	if n.IsExitNode() {
		extra = ic.Exit + " exit node"
	}
	iw, _ := pane.Inner(w, h)
	return pane.Render(s, n.DisplayName(), kv.Render(s, pairs, iw), w, h, false, pane.WithExtra(extra))
}

func (m *Model) routesLine(ctx module.Context, n api.Node) string {
	s, ic := ctx.Styles(), ctx.Icons()
	if len(n.AvailableRoutes) == 0 {
		return "-"
	}
	var parts []string
	for _, p := range n.AvailableRoutes {
		label := p.String()
		if api.IsExitRoute(p) {
			label += " (exit)"
		}
		switch {
		case slices.Contains(n.ApprovedRoutes, p):
			if primary, known := ctx.Store().IsPrimary(n.ID, p.String()); known && primary {
				label += " " + ic.Primary
			}
			parts = append(parts, s.OK.Render(ic.Check+" "+label))
		default:
			parts = append(parts, s.Pending.Render(ic.Pending+" "+label+" pending"))
		}
	}
	return strings.Join(parts, "  ")
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func shortKey(k string) string {
	if i := strings.IndexByte(k, ':'); i >= 0 {
		k = k[i+1:]
	}
	if len(k) > 12 {
		return k[:12] + "…"
	}
	return k
}

// --- actions --------------------------------------------------------------

func result(id string, err error, msg string) tea.Msg {
	return module.ActionResult{ActionID: id, Err: err, Message: msg, Refresh: store.SliceNodes}
}

func call(f func(ctx context.Context) error) error {
	ctx, cancel := api.WithTimeout(context.Background())
	defer cancel()
	return f(ctx)
}

func (m *Model) Actions() []module.Action {
	hasCursor := func(module.Context) (bool, string) {
		if _, ok := m.cursor(); !ok {
			return false, "No machine selected."
		}
		return true, ""
	}
	cursorTarget := func(module.Context) []module.Target {
		if n, ok := m.cursor(); ok {
			return toTargets([]api.Node{n})
		}
		return nil
	}
	return []module.Action{
		{
			ID: "machines.detail", Title: "Toggle detail pane", Keys: []string{"enter"},
			Run: func(module.Context, module.Values) tea.Cmd { m.detail = !m.detail; return nil },
		},
		{
			ID: "machines.rename", Title: "Rename machine", Keys: []string{"r"}, Scope: module.ScopeSelection,
			Mutating: true, Enabled: hasCursor, Targets: cursorTarget,
			Params: func(module.Context) *module.FormSpec {
				n, _ := m.cursor()
				return &module.FormSpec{Title: "Rename " + n.DisplayName(), Submit: "Rename", Fields: []module.Field{{
					ID: "name", Label: "New name", Value: n.DisplayName(), Required: true,
					Help: "DNS label: lowercase a-z, 0-9 and -", Validate: ValidateName,
				}}}
			},
			Run: func(ctx module.Context, v module.Values) tea.Cmd {
				n, _ := m.cursor()
				b, name := ctx.Backend(), v["name"]
				return func() tea.Msg {
					err := call(func(c context.Context) error { _, err := b.RenameNode(c, n.ID, name); return err })
					return result("machines.rename", err, fmt.Sprintf("Renamed %s to %s.", n.DisplayName(), name))
				}
			},
		},
		{
			ID: "machines.tags", Title: "Edit tags", Keys: []string{"t"}, Scope: module.ScopeSelection,
			Mutating: true, Multi: true,
			Targets: func(ctx module.Context) []module.Target { return toTargets(m.targetNodes(ctx)) },
			Params: func(ctx module.Context) *module.FormSpec {
				ns := m.targetNodes(ctx)
				if len(ns) == 1 {
					return &module.FormSpec{Title: "Tags for " + ns[0].DisplayName(), Submit: "Save", Fields: []module.Field{{
						ID: "set", Label: "Tags", Kind: module.FieldTags, Value: strings.Join(ns[0].Tags, ", "),
						Help: "Comma-separated; replaces the node's tags", Validate: validTags,
					}}}
				}
				return &module.FormSpec{Title: fmt.Sprintf("Tags for %d machines", len(ns)), Submit: "Apply", Fields: []module.Field{
					{ID: "add", Label: "Add", Kind: module.FieldTags, Validate: validTags},
					{ID: "remove", Label: "Remove", Kind: module.FieldTags, Validate: validTags},
				}}
			},
			Run: func(ctx module.Context, v module.Values) tea.Cmd {
				ns := m.targetNodes(ctx)
				b := ctx.Backend()
				return func() tea.Msg {
					var set, add, rm []string
					var err error
					if s, ok := v["set"]; ok {
						if set, err = ParseTags(s); err != nil {
							return result("machines.tags", err, "")
						}
					} else {
						add, _ = ParseTags(v["add"])
						rm, _ = ParseTags(v["remove"])
					}
					failed := 0
					for _, n := range ns {
						tags := set
						if _, ok := v["set"]; !ok {
							tags = MergeTags(n.Tags, add, rm)
						}
						if e := call(func(c context.Context) error { _, err := b.SetTags(c, n.ID, tags); return err }); e != nil {
							err, failed = e, failed+1
						}
					}
					if err != nil && len(ns) > 1 {
						err = fmt.Errorf("%d of %d machines failed: %w", failed, len(ns), err)
					}
					return result("machines.tags", err, fmt.Sprintf("Updated tags on %d machine(s).", len(ns)))
				}
			},
		},
		{
			ID: "machines.routes", Title: "Approve routes", Keys: []string{"a"}, Scope: module.ScopeSelection,
			Mutating: true, Danger: module.DangerConfirm, Targets: cursorTarget,
			Consequence: "Approved routes are reachable by every client the policy allows.",
			Enabled: func(ctx module.Context) (bool, string) {
				n, ok := m.cursor()
				if !ok {
					return false, "No machine selected."
				}
				if len(n.AvailableRoutes) == 0 {
					return false, n.DisplayName() + " does not advertise any routes."
				}
				return true, ""
			},
			Params: func(module.Context) *module.FormSpec {
				n, _ := m.cursor()
				var fields []module.Field
				for i, p := range n.AvailableRoutes {
					label := p.String()
					if api.IsExitRoute(p) {
						label += " (exit node)"
					}
					fields = append(fields, module.Field{
						ID: "r" + strconv.Itoa(i), Label: label, Kind: module.FieldToggle,
						Value: strconv.FormatBool(slices.Contains(n.ApprovedRoutes, p)),
					})
				}
				return &module.FormSpec{Title: "Routes for " + n.DisplayName(), Submit: "Apply", Fields: fields}
			},
			Run: func(ctx module.Context, v module.Values) tea.Cmd {
				n, _ := m.cursor()
				b := ctx.Backend()
				var approved []netip.Prefix
				for i, p := range n.AvailableRoutes {
					if v["r"+strconv.Itoa(i)] == "true" {
						approved = append(approved, p)
					}
				}
				// Keep approvals for routes the node no longer advertises:
				// SetApprovedRoutes replaces the list and must not drop them silently.
				for _, p := range n.ApprovedRoutes {
					if !slices.Contains(n.AvailableRoutes, p) {
						approved = append(approved, p)
					}
				}
				return func() tea.Msg {
					err := call(func(c context.Context) error { _, err := b.SetApprovedRoutes(c, n.ID, approved); return err })
					return result("machines.routes", err, fmt.Sprintf("Routes updated for %s.", n.DisplayName()))
				}
			},
		},
		{
			ID: "machines.expire", Title: "Expire machine", Keys: []string{"e"}, Scope: module.ScopeSelection,
			Mutating: true, Multi: true, Danger: module.DangerConfirm,
			Consequence: "The machine must re-authenticate before it can reconnect.",
			Targets:     func(ctx module.Context) []module.Target { return toTargets(m.targetNodes(ctx)) },
			Run: func(ctx module.Context, _ module.Values) tea.Cmd {
				return m.each(ctx, "machines.expire", "Expired", func(c context.Context, b api.Backend, n api.Node) error {
					_, err := b.ExpireNode(c, n.ID, api.ExpireOpts{})
					return err
				})
			},
		},
		{
			ID: "machines.noexpire", Title: "Disable key expiry", Keys: []string{"E"}, Scope: module.ScopeSelection,
			Mutating: true, Multi: true, Danger: module.DangerConfirm, Cap: api.CapDisableExpiry,
			Consequence: "The machine's key will never expire.",
			Targets:     func(ctx module.Context) []module.Target { return toTargets(m.targetNodes(ctx)) },
			Run: func(ctx module.Context, _ module.Values) tea.Cmd {
				return m.each(ctx, "machines.noexpire", "Disabled expiry on", func(c context.Context, b api.Backend, n api.Node) error {
					_, err := b.ExpireNode(c, n.ID, api.ExpireOpts{DisableExpiry: true})
					return err
				})
			},
		},
		{
			ID: "machines.delete", Title: "Delete machine", Keys: []string{"D"}, Scope: module.ScopeSelection,
			Mutating: true, Multi: true, Danger: module.DangerCritical,
			Consequence: "The machine is removed from the tailnet and must register again to rejoin.",
			Targets:     func(ctx module.Context) []module.Target { return toTargets(m.targetNodes(ctx)) },
			Run: func(ctx module.Context, _ module.Values) tea.Cmd {
				return m.each(ctx, "machines.delete", "Deleted", func(c context.Context, b api.Backend, n api.Node) error {
					return b.DeleteNode(c, n.ID)
				})
			},
		},
		{
			ID: "machines.owner", Title: "Change owner (not supported by headscale)", Keys: []string{"O"}, Hidden: true,
			Enabled: func(module.Context) (bool, string) {
				return false, "headscale's API cannot move a machine to another user. Re-register it under the new user instead."
			},
		},
		{
			ID: "machines.copyip", Title: "Copy IP address", Keys: []string{"y"}, Enabled: hasCursor,
			Run: func(ctx module.Context, _ module.Values) tea.Cmd {
				n, _ := m.cursor()
				return copyCmd("machines.copyip", firstV4(n))
			},
		},
		{
			ID: "machines.copyname", Title: "Copy name", Keys: []string{"Y"}, Enabled: hasCursor,
			Run: func(ctx module.Context, _ module.Values) tea.Cmd {
				n, _ := m.cursor()
				return copyCmd("machines.copyname", n.DisplayName())
			},
		},
		{
			ID: "machines.ping", Title: "Ping from server", Keys: []string{"p"}, Needs: host.NeedDebug, Enabled: hasCursor,
			Run: func(ctx module.Context, _ module.Values) tea.Cmd {
				n, _ := m.cursor()
				h := ctx.Host()
				return func() tea.Msg {
					var rtt time.Duration
					err := call(func(c context.Context) (err error) { rtt, err = h.Debug().Ping(c, n.ID); return })
					return module.ActionResult{ActionID: "machines.ping", Err: err,
						Message: fmt.Sprintf("%s answered in %s.", n.DisplayName(), rtt.Round(time.Millisecond))}
				}
			},
		},
		{
			ID: "machines.sort", Title: "Sort by next column", Keys: []string{"s"},
			Run: func(module.Context, module.Values) tea.Cmd {
				cols := m.tbl.VisibleColumns()
				cur, _ := m.tbl.Sort()
				i := slices.Index(cols, cur)
				m.tbl.SortBy(cols[(i+1)%len(cols)])
				return nil
			},
		},
		{
			ID: "machines.sortrev", Title: "Reverse sort", Keys: []string{"S"},
			Run: func(module.Context, module.Values) tea.Cmd {
				col, desc := m.tbl.Sort()
				m.tbl.SetSort(col, !desc)
				return nil
			},
		},
	}
}

// each applies f to every target node and reports a combined result.
func (m *Model) each(ctx module.Context, id, verb string, f func(context.Context, api.Backend, api.Node) error) tea.Cmd {
	ns := m.targetNodes(ctx)
	b := ctx.Backend()
	m.tbl.ClearSelection()
	return func() tea.Msg {
		var err error
		failed := 0
		for _, n := range ns {
			if e := call(func(c context.Context) error { return f(c, b, n) }); e != nil {
				err, failed = e, failed+1
			}
		}
		if err != nil && len(ns) > 1 {
			err = fmt.Errorf("%d of %d machines failed: %w", failed, len(ns), err)
		}
		what := fmt.Sprintf("%d machines", len(ns))
		if len(ns) == 1 {
			what = ns[0].DisplayName()
		}
		return result(id, err, fmt.Sprintf("%s %s.", verb, what))
	}
}

func validTags(s string) error {
	_, err := ParseTags(s)
	return err
}

func copyCmd(id, text string) tea.Cmd {
	return func() tea.Msg {
		method, err := clip.Copy(text)
		msg := "Copied " + text
		if method == clip.MethodOSC52 {
			msg += " (via terminal)"
		}
		return module.ActionResult{ActionID: id, Err: err, Message: msg + "."}
	}
}
