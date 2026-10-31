// Package users is the Users page: every identity that owns machines and
// keys, with create, rename and delete, and jumps to a user's machines and
// keys. Deleting a user who still owns machines is blocked.
package users

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tonhe/headsup/internal/api"
	"github.com/tonhe/headsup/internal/clip"
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

// Model is the users module.
type Model struct {
	tbl   *table.Model
	bar   *filter.Bar
	rows  []row
	ready bool
	// blocked is set when a delete was refused because the user owns
	// machines; the view then explains why and offers "show machines".
	blocked *row
}

// New returns the module.
func New() *Model { return &Model{} }

func (m *Model) ID() string                { return "users" }
func (m *Model) Title() string             { return "Users" }
func (m *Model) Icon(ic icons.Set) string  { return ic.User }
func (m *Model) Order() int                { return 22 }
func (m *Model) Modes() module.Modes       { return module.ModeAPI }
func (m *Model) Chord() string             { return "u" }
func (m *Model) Capturing() bool           { return m.bar != nil && m.bar.Active() }
func (m *Model) Badge(*store.Store) string { return "" }

func (m *Model) Init(ctx module.Context) tea.Cmd {
	m.tbl = table.New(ctx.Styles(), ctx.Icons(), columns())
	m.tbl.SetMulti(false)
	m.tbl.SetFocused(true)
	m.tbl.SetSort("name", false)
	m.bar = filter.NewBar(ctx.Styles(), ctx.Icons())
	return nil
}

func (m *Model) rebuild(ctx module.Context) {
	all := newRows(ctx.Store(), ctx)
	q := m.bar.Query()
	m.rows = m.rows[:0]
	trows := make([]table.Row, 0, len(all))
	for _, r := range all {
		if q.Empty() || q.Match(r) {
			m.rows = append(m.rows, r)
			trows = append(trows, r)
		}
	}
	m.tbl.SetRows(trows)
	m.bar.SetCounts(len(trows), len(all))
	m.ready = ctx.Store().Loaded&store.SliceUsers != 0
	if m.blocked != nil {
		// Keep the notice only while the blocked user is still under the cursor.
		if cur, ok := m.cursorRow(); !ok || cur.u.ID != m.blocked.u.ID {
			m.blocked = nil
		}
	}
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
		m.rebuild(ctx)
		return m, nil
	case store.UpdatedMsg:
		m.rebuild(ctx)
	case tea.KeyMsg:
		if m.bar.Active() || msg.String() == "/" {
			return m, m.bar.Update(msg)
		}
		if msg.String() == "esc" {
			if m.blocked != nil {
				m.blocked = nil
				return m, nil
			}
			if m.bar.Value() != "" {
				return m, m.bar.Clear()
			}
		}
		cmd := m.tbl.Update(msg)
		if cur, ok := m.cursorRow(); m.blocked != nil && (!ok || cur.u.ID != m.blocked.u.ID) {
			m.blocked = nil
		}
		return m, cmd
	}
	return m, nil
}

func (m *Model) cursorRow() (row, bool) {
	if m.tbl == nil {
		return row{}, false
	}
	r, ok := m.tbl.Cursor()
	if !ok {
		return row{}, false
	}
	return r.(row), true
}

func (m *Model) cursor() (api.User, bool) {
	r, ok := m.cursorRow()
	return r.u, ok
}

func (m *Model) Hints() []module.Hint {
	if m.bar != nil && m.bar.Active() {
		return []module.Hint{{Key: "enter", Desc: "keep filter"}, {Key: "esc", Desc: "clear"}}
	}
	if m.blocked != nil {
		return []module.Hint{{Key: "enter", Desc: "show machines"}, {Key: "esc", Desc: "dismiss"}}
	}
	return []module.Hint{
		{Key: "/", Desc: "filter"}, {Key: "n", Desc: "new"}, {Key: "r", Desc: "rename"},
		{Key: "D", Desc: "delete"}, {Key: "enter", Desc: "machines"}, {Key: "K", Desc: "keys"},
	}
}

// UserQuery is the machines/keys filter that selects one user's items.
func UserQuery(name string) string {
	if strings.ContainsAny(name, " \t\"") {
		return `user:"` + strings.ReplaceAll(name, `"`, `\"`) + `"`
	}
	return "user:" + name
}

// ValidateName mirrors headscale's username rules: at least two characters,
// starting with a letter, using letters, digits, '-', '.', '_' and at most
// one '@'.
func ValidateName(s string) error {
	rs := []rune(s)
	switch {
	case len(rs) < 2:
		return fmt.Errorf("at least 2 characters")
	case !unicode.IsLetter(rs[0]):
		return fmt.Errorf("must start with a letter")
	case strings.Count(s, "@") > 1:
		return fmt.Errorf("at most one @")
	}
	for _, r := range rs {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("-._@", r) {
			return fmt.Errorf("%q is not allowed; use letters, digits, - . _ @", r)
		}
	}
	return nil
}

// --- view -------------------------------------------------------------------

func (m *Model) View(ctx module.Context, r module.Rect) string {
	s := ctx.Styles()
	if m.tbl == nil {
		return ""
	}
	if !m.ready {
		return empty.Render(s, "Loading users…", "", "", r.W, r.H)
	}
	if len(ctx.Store().Users) == 0 {
		return empty.Render(s, "No users yet", "Machines and keys belong to a user.", "press n to create one", r.W, r.H)
	}
	var top []string
	if m.bar.Active() || m.bar.Value() != "" {
		top = append(top, m.bar.View(r.W))
	}
	if m.blocked != nil {
		top = append(top, m.blockedLine(ctx, r.W))
	}
	h := r.H - len(top)
	u, hasCur := m.cursorRow()

	split := ""
	listW, listH := r.W, h
	switch {
	case hasCur && r.W >= 130:
		listW = r.W * 3 / 5
		split = "right"
	case hasCur && h >= 26:
		// Size the list to its rows so a small tailnet leaves room for detail.
		listH = min(max(len(m.rows)+3, 8), h-11)
		split = "below"
	}
	iw, ih := pane.Inner(listW, listH)
	m.tbl.SetSize(iw, ih)
	list := pane.Render(s, fmt.Sprintf("Users %s %d", ctx.Icons().Bullet, len(m.rows)), m.tbl.View(), listW, listH, true)

	body := list
	switch split {
	case "right":
		body = lipgloss.JoinHorizontal(lipgloss.Top, list, m.detailView(ctx, u, r.W-listW, h))
	case "below":
		body = lipgloss.JoinVertical(lipgloss.Left, list, m.detailView(ctx, u, r.W, h-listH))
	}
	return lipgloss.JoinVertical(lipgloss.Left, append(top, body)...)
}

func (m *Model) blockedLine(ctx module.Context, w int) string {
	s, ic := ctx.Styles(), ctx.Icons()
	b := m.blocked
	text := fmt.Sprintf("%s %s cannot be deleted: it owns %d machine(s).",
		ic.Warn, b.u.Name, b.nodes)
	keys := s.FooterKey.Render("enter") + s.Muted.Render(" show machines  ") + s.FooterKey.Render("esc") + s.Muted.Render(" dismiss")
	line := s.Warn.Render(text)
	if lipgloss.Width(line)+2+lipgloss.Width(keys) > w {
		line = table.Fit(line, max(0, w-lipgloss.Width(keys)-2), lipgloss.Left, ic.Ellipsis)
	}
	return table.Fit(line+"  "+keys, w, lipgloss.Left, ic.Ellipsis)
}

func (m *Model) detailView(ctx module.Context, r row, w, h int) string {
	s := ctx.Styles()
	u := r.u
	now := ctx.Now()
	nodes := ctx.Store().NodesOf(u.Name)
	names := make([]string, 0, len(nodes))
	for _, n := range nodes {
		names = append(names, n.DisplayName())
	}
	machines := strconv.Itoa(len(nodes))
	if len(names) > 0 {
		machines += "  " + s.Muted.Render(strings.Join(names, ", "))
	}
	created := "-"
	if !u.CreatedAt.IsZero() {
		created = u.CreatedAt.Format("2006-01-02") + " (" + humanize.Ago(u.CreatedAt, now) + ")"
	}
	pairs := []kv.Pair{
		{Key: "Name", Value: u.Name},
		{Key: "Display name", Value: orDash(u.DisplayName)},
		{Key: "Email", Value: orDash(u.Email)},
		{Key: "Provider", Value: providerLabel(u)},
		{Key: "Created", Value: created},
		{Key: "Machines", Value: machines},
		{Key: "Pre-auth keys", Value: strconv.Itoa(r.keys)},
	}
	if u.ProviderID != "" {
		pairs = append(pairs, kv.Pair{Key: "Provider ID", Value: s.Muted.Render(u.ProviderID)})
	}
	iw, _ := pane.Inner(w, h)
	return pane.Render(s, u.Name, kv.Render(s, pairs, iw), w, h, false, pane.WithExtra("ID "+strconv.FormatUint(u.ID, 10)))
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func providerLabel(u api.User) string {
	if u.Provider == "" {
		return "local"
	}
	return u.Provider
}

// --- actions ----------------------------------------------------------------

func call(f func(context.Context) error) error {
	ctx, cancel := api.WithTimeout(context.Background())
	defer cancel()
	return f(ctx)
}

func (m *Model) Actions() []module.Action {
	hasCursor := func(module.Context) (bool, string) {
		if _, ok := m.cursor(); !ok {
			return false, "No user selected."
		}
		return true, ""
	}
	target := func(module.Context) []module.Target {
		if u, ok := m.cursor(); ok {
			return []module.Target{{Kind: "user", ID: strconv.FormatUint(u.ID, 10), Name: u.Name}}
		}
		return nil
	}
	return []module.Action{
		{
			ID: "users.create", Title: "New user", Keys: []string{"n"}, Mutating: true,
			Params: func(module.Context) *module.FormSpec {
				return &module.FormSpec{Title: "New user", Submit: "Create", Fields: []module.Field{
					{ID: "name", Label: "Name", Required: true, Validate: ValidateName,
						Help: "Letters, digits, - . _ and one @; starts with a letter"},
					{ID: "display", Label: "Display name"},
					{ID: "email", Label: "Email"},
				}}
			},
			Run: func(ctx module.Context, v module.Values) tea.Cmd {
				b := ctx.Backend()
				nu := api.NewUser{Name: strings.TrimSpace(v["name"]), DisplayName: v["display"], Email: v["email"]}
				return func() tea.Msg {
					err := call(func(c context.Context) error { _, err := b.CreateUser(c, nu); return err })
					return module.ActionResult{ActionID: "users.create", Err: err, Refresh: store.SliceUsers,
						Message: "Created user " + nu.Name + "."}
				}
			},
		},
		{
			ID: "users.rename", Title: "Rename user", Keys: []string{"r"}, Scope: module.ScopeSelection,
			Mutating: true, Enabled: hasCursor, Targets: target,
			Params: func(module.Context) *module.FormSpec {
				u, _ := m.cursor()
				return &module.FormSpec{Title: "Rename " + u.Name, Submit: "Rename", Fields: []module.Field{{
					ID: "name", Label: "New name", Value: u.Name, Required: true, Validate: ValidateName,
					Help: "Machines and keys follow the user; policy rules naming it do not.",
				}}}
			},
			Run: func(ctx module.Context, v module.Values) tea.Cmd {
				u, _ := m.cursor()
				b, name := ctx.Backend(), strings.TrimSpace(v["name"])
				return func() tea.Msg {
					err := call(func(c context.Context) error { _, err := b.RenameUser(c, u.ID, name); return err })
					return module.ActionResult{ActionID: "users.rename", Err: err,
						Refresh: store.SliceUsers | store.SliceNodes | store.SlicePreAuthKeys,
						Message: fmt.Sprintf("Renamed %s to %s.", u.Name, name)}
				}
			},
		},
		m.deleteAction(hasCursor, target),
		{
			ID: "users.machines", Title: "Show this user's machines", Keys: []string{"enter"},
			Enabled: hasCursor,
			Run: func(ctx module.Context, _ module.Values) tea.Cmd {
				u, _ := m.cursor()
				m.blocked = nil
				return ctx.Navigate("machines", module.FilterArgs{Query: UserQuery(u.Name)})
			},
		},
		{
			// "k" is the table's cursor-up key, so keys use the capital.
			ID: "users.keys", Title: "Show this user's keys", Keys: []string{"K"},
			Enabled: hasCursor,
			Run: func(ctx module.Context, _ module.Values) tea.Cmd {
				u, _ := m.cursor()
				return ctx.Navigate("keys", module.FilterArgs{Query: UserQuery(u.Name)})
			},
		},
		{
			ID: "users.copyname", Title: "Copy user name", Keys: []string{"y"}, Enabled: hasCursor,
			Run: func(module.Context, module.Values) tea.Cmd {
				u, _ := m.cursor()
				return func() tea.Msg {
					_, err := clip.Copy(u.Name)
					return module.ActionResult{ActionID: "users.copyname", Err: err, Message: "Copied " + u.Name + "."}
				}
			},
		},
	}
}

// deleteAction is a critical delete for a user with no machines. For a user
// who still owns machines it becomes a harmless action that shows why the
// delete is blocked and offers "show machines" (headscale would refuse it).
func (m *Model) deleteAction(hasCursor func(module.Context) (bool, string), target func(module.Context) []module.Target) module.Action {
	a := module.Action{
		ID: "users.delete", Title: "Delete user", Keys: []string{"D"}, Scope: module.ScopeSelection,
		Enabled: hasCursor, Targets: target,
	}
	r, ok := m.cursorRow()
	if ok && r.nodes > 0 {
		a.Run = func(module.Context, module.Values) tea.Cmd {
			blocked := r
			m.blocked = &blocked
			return nil
		}
		return a
	}
	a.Mutating, a.Danger = true, module.DangerCritical
	a.Consequence = "The user is removed permanently."
	if ok && r.keys > 0 {
		a.Consequence = fmt.Sprintf("The user and their %d pre-auth key(s) are removed permanently.", r.keys)
	}
	a.Run = func(ctx module.Context, _ module.Values) tea.Cmd {
		u, _ := m.cursor()
		b := ctx.Backend()
		return func() tea.Msg {
			err := call(func(c context.Context) error { return b.DeleteUser(c, u.ID) })
			return module.ActionResult{ActionID: "users.delete", Err: err, Refresh: store.SliceUsers | store.SlicePreAuthKeys,
				Message: "Deleted user " + u.Name + "."}
		}
	}
	return a
}
