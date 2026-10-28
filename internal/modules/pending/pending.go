// Package pending is "Add device": the queue of devices waiting to join,
// registration by pasted key or URL, and pre-auth key creation with a
// one-time reveal of the key, the `tailscale up` command, and a QR code.
package pending

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tonhe/fathom/internal/api"
	"github.com/tonhe/fathom/internal/clip"
	"github.com/tonhe/fathom/internal/icons"
	"github.com/tonhe/fathom/internal/module"
	"github.com/tonhe/fathom/internal/modules/registry"
	"github.com/tonhe/fathom/internal/store"
	"github.com/tonhe/fathom/internal/ui/form"
	"github.com/tonhe/fathom/internal/ui/humanize"
	"github.com/tonhe/fathom/internal/ui/pane"
	"github.com/tonhe/fathom/internal/ui/qr"
	"github.com/tonhe/fathom/internal/ui/table"
)

func init() { registry.Register(func() module.Module { return New() }) }

// Entry is one device waiting to join.
type Entry struct {
	AuthID      string
	Hostname    string
	RequestedAt time.Time
	FromServer  bool // seen in the server's registration cache (host mode)
}

// reveal holds a freshly created pre-auth key. It lives only in memory and
// only until the admin closes it: after that the key is shown masked.
type reveal struct {
	key    api.Secret
	user   string
	showQR bool
}

// Model is the pending module.
type Model struct {
	pasted []Entry
	queue  []Entry
	cursor int
	reveal *reveal
}

// New returns the module.
func New() *Model { return &Model{} }

func (m *Model) ID() string                  { return "pending" }
func (m *Model) Title() string               { return "Add device" }
func (m *Model) Icon(ic icons.Set) string    { return ic.Pending }
func (m *Model) Order() int                  { return 30 }
func (m *Model) Modes() module.Modes         { return module.ModeAPIHost }
func (m *Model) Chord() string               { return "p" }
func (m *Model) Capturing() bool             { return false }
func (m *Model) Init(module.Context) tea.Cmd { return nil }

func (m *Model) Badge(st *store.Store) string {
	if n := max(len(m.queue), st.RegCount); n > 0 {
		return strconv.Itoa(n)
	}
	return ""
}

func (m *Model) Hints() []module.Hint {
	if m.reveal != nil {
		return []module.Hint{{Key: "c", Desc: "copy command"}, {Key: "y", Desc: "copy key"}, {Key: "Q", Desc: "QR"}, {Key: "esc", Desc: "done"}}
	}
	return []module.Hint{{Key: "n", Desc: "new pre-auth key"}, {Key: "p", Desc: "paste key/URL"},
		{Key: "a", Desc: "approve"}, {Key: "x", Desc: "reject"}}
}

// mergeQueue combines server-cache entries with pasted ones, by AuthID.
func mergeQueue(server []Entry, pasted []Entry) []Entry {
	out := append([]Entry(nil), server...)
	for _, p := range pasted {
		if !slices.ContainsFunc(out, func(e Entry) bool { return e.AuthID == p.AuthID }) {
			out = append(out, p)
		}
	}
	return out
}

func (m *Model) rebuild(st *store.Store) {
	var server []Entry
	for _, r := range st.Registrations {
		server = append(server, Entry{AuthID: r.AuthID, Hostname: r.Hostname, RequestedAt: r.RequestedAt, FromServer: true})
	}
	m.queue = mergeQueue(server, m.pasted)
	m.cursor = min(m.cursor, max(0, len(m.queue)-1))
}

type keyCreatedMsg struct {
	key  api.PreAuthKey
	user string
	err  error
}

type queueDoneMsg struct{ authID string }

func (m *Model) Update(ctx module.Context, msg tea.Msg) (module.Module, tea.Cmd) {
	switch msg := msg.(type) {
	case store.UpdatedMsg:
		m.rebuild(ctx.Store())
	case keyCreatedMsg:
		res := module.ActionResult{ActionID: "pending.newkey", Err: msg.err, Refresh: store.SlicePreAuthKeys, Silent: true}
		if msg.err == nil {
			m.reveal = &reveal{key: msg.key.Key, user: msg.user}
		}
		return m, func() tea.Msg { return res }
	case queueDoneMsg:
		m.pasted = slices.DeleteFunc(m.pasted, func(e Entry) bool { return e.AuthID == msg.authID })
		m.rebuild(ctx.Store())
	case tea.KeyMsg:
		if m.reveal != nil {
			if msg.String() == "esc" || msg.String() == "enter" {
				m.reveal = nil
			}
			return m, nil
		}
		switch msg.String() {
		case "j", "down":
			m.cursor = min(m.cursor+1, max(0, len(m.queue)-1))
		case "k", "up":
			m.cursor = max(m.cursor-1, 0)
		}
	}
	return m, nil
}

func (m *Model) selected() (Entry, bool) {
	if m.cursor < len(m.queue) {
		return m.queue[m.cursor], true
	}
	return Entry{}, false
}

func userOptions(st *store.Store) []string {
	out := make([]string, 0, len(st.Users))
	for _, u := range st.Users {
		out = append(out, u.Name)
	}
	slices.Sort(out)
	return out
}

func call(f func(context.Context) error) error {
	ctx, cancel := api.WithTimeout(context.Background())
	defer cancel()
	return f(ctx)
}

func (m *Model) Actions() []module.Action {
	needUsers := func(ctx module.Context) (bool, string) {
		if len(ctx.Store().Users) == 0 {
			return false, "Create a user first (headscale users create NAME)."
		}
		return true, ""
	}
	inQueue := func(module.Context) (bool, string) {
		if m.reveal != nil {
			return false, "Close the key first."
		}
		if _, ok := m.selected(); !ok {
			return false, "No device is waiting."
		}
		return true, ""
	}
	entryTarget := func(module.Context) []module.Target {
		e, _ := m.selected()
		name := e.Hostname
		if name == "" {
			name = e.AuthID
		}
		return []module.Target{{Kind: "registration", ID: e.AuthID, Name: name}}
	}
	return []module.Action{
		{
			ID: "pending.newkey", Title: "New pre-auth key", Keys: []string{"n"}, Mutating: true, Enabled: needUsers,
			Params: func(ctx module.Context) *module.FormSpec {
				users := userOptions(ctx.Store())
				return &module.FormSpec{Title: "New pre-auth key", Submit: "Create", Fields: []module.Field{
					{ID: "user", Label: "User", Kind: module.FieldSelect, Options: users, Value: users[0]},
					{ID: "reusable", Label: "Reusable", Kind: module.FieldToggle, Value: "false", Help: "Can register more than one device"},
					{ID: "ephemeral", Label: "Ephemeral", Kind: module.FieldToggle, Value: "false", Help: "Devices are removed when they go offline"},
					{ID: "expiration", Label: "Expires in", Kind: module.FieldDuration, Value: "24h", Required: true},
					{ID: "tags", Label: "Tags", Kind: module.FieldTags, Help: "Optional, e.g. server, prod"},
				}}
			},
			Run: func(ctx module.Context, v module.Values) tea.Cmd {
				b := ctx.Backend()
				return func() tea.Msg {
					d, err := form.ParseDuration(v["expiration"])
					if err != nil {
						return keyCreatedMsg{err: err}
					}
					var tags []string
					for _, t := range strings.Split(form.NormalizeTags(v["tags"]), ",") {
						if t = strings.TrimSpace(t); t != "" {
							tags = append(tags, t)
						}
					}
					var k api.PreAuthKey
					err = call(func(c context.Context) (err error) {
						k, err = b.CreatePreAuthKey(c, api.NewPreAuthKey{
							User: v["user"], Reusable: v["reusable"] == "true", Ephemeral: v["ephemeral"] == "true",
							Expiration: time.Now().Add(d), ACLTags: tags,
						})
						return
					})
					return keyCreatedMsg{key: k, user: v["user"], err: err}
				}
			},
		},
		{
			ID: "pending.paste", Title: "Register by key or URL", Keys: []string{"p"}, Mutating: true, Enabled: needUsers,
			Params: func(ctx module.Context) *module.FormSpec {
				users := userOptions(ctx.Store())
				return &module.FormSpec{Title: "Register a device", Submit: "Register", Fields: []module.Field{
					{ID: "text", Label: "URL or key", Required: true, Placeholder: "https://…/register/… or the key",
						Help:     "Paste what `tailscale up` printed on the device",
						Validate: func(s string) error { _, err := Parse(s); return err }},
					{ID: "user", Label: "Owner", Kind: module.FieldSelect, Options: users, Value: users[0]},
				}}
			},
			Run: func(ctx module.Context, v module.Values) tea.Cmd {
				b := ctx.Backend()
				p, err := Parse(v["text"])
				user := v["user"]
				return func() tea.Msg {
					if err != nil {
						return module.ActionResult{ActionID: "pending.paste", Err: err}
					}
					switch p.Kind {
					case KindAuthID:
						if !b.Capabilities().Has(api.CapAuthApproval) {
							return module.ActionResult{ActionID: "pending.paste", Err: api.Errf(api.KindUnsupported, "this server does not support web-auth approval")}
						}
						err = call(func(c context.Context) error { return b.AuthRegister(c, p.Value, user) })
					default:
						err = call(func(c context.Context) error { _, err := b.RegisterNode(c, user, p.Value); return err })
					}
					return module.ActionResult{ActionID: "pending.paste", Err: err, Refresh: store.SliceNodes,
						Message: "Registered the device to " + user + "."}
				}
			},
		},
		{
			ID: "pending.approve", Title: "Approve device", Keys: []string{"a"}, Mutating: true, Cap: api.CapAuthApproval,
			Scope: module.ScopeSelection, Enabled: inQueue, Targets: entryTarget,
			Params: func(ctx module.Context) *module.FormSpec {
				users := userOptions(ctx.Store())
				e, _ := m.selected()
				return &module.FormSpec{Title: "Approve " + firstNonEmpty(e.Hostname, e.AuthID), Submit: "Approve", Fields: []module.Field{
					{ID: "user", Label: "Owner", Kind: module.FieldSelect, Options: users, Value: firstOr(users)},
				}}
			},
			Run: func(ctx module.Context, v module.Values) tea.Cmd {
				b := ctx.Backend()
				e, _ := m.selected()
				user := v["user"]
				return tea.Sequence(func() tea.Msg {
					err := call(func(c context.Context) error { return b.AuthRegister(c, e.AuthID, user) })
					return module.ActionResult{ActionID: "pending.approve", Err: err, Refresh: store.SliceNodes | store.SliceHost,
						Message: fmt.Sprintf("Approved %s as %s.", firstNonEmpty(e.Hostname, e.AuthID), user)}
				}, func() tea.Msg { return queueDoneMsg{e.AuthID} })
			},
		},
		{
			ID: "pending.reject", Title: "Reject device", Keys: []string{"x"}, Mutating: true, Danger: module.DangerConfirm,
			Cap: api.CapAuthApproval, Scope: module.ScopeSelection, Enabled: inQueue, Targets: entryTarget,
			Consequence: "The device's login request is refused; it must start again.",
			Run: func(ctx module.Context, _ module.Values) tea.Cmd {
				b := ctx.Backend()
				e, _ := m.selected()
				return tea.Sequence(func() tea.Msg {
					err := call(func(c context.Context) error { return b.AuthReject(c, e.AuthID) })
					return module.ActionResult{ActionID: "pending.reject", Err: err, Refresh: store.SliceHost,
						Message: "Rejected " + firstNonEmpty(e.Hostname, e.AuthID) + "."}
				}, func() tea.Msg { return queueDoneMsg{e.AuthID} })
			},
		},
		{
			ID: "pending.queue", Title: "Add a web-login request to the queue", Keys: []string{"P"},
			Cap: api.CapAuthApproval,
			Params: func(module.Context) *module.FormSpec {
				return &module.FormSpec{Title: "Queue a login request", Submit: "Add", Fields: []module.Field{
					{ID: "text", Label: "Auth URL or ID", Required: true,
						Validate: func(s string) error { _, err := Parse(s); return err }},
				}}
			},
			Run: func(ctx module.Context, v module.Values) tea.Cmd {
				p, err := Parse(v["text"])
				if err == nil {
					m.pasted = append(m.pasted, Entry{AuthID: p.Value, RequestedAt: ctx.Now()})
					m.rebuild(ctx.Store())
				}
				return func() tea.Msg {
					return module.ActionResult{ActionID: "pending.queue", Err: err, Message: "Added to the queue."}
				}
			},
		},
		{
			ID: "pending.copycmd", Title: "Copy tailscale up command", Keys: []string{"c"}, Hidden: true,
			Enabled: m.revealing,
			Run: func(ctx module.Context, _ module.Values) tea.Cmd {
				return copyCmd("pending.copycmd", TailscaleUpCommand(ctx.ServerURL(), m.reveal.key.Reveal()), "command")
			},
		},
		{
			ID: "pending.copykey", Title: "Copy key", Keys: []string{"y"}, Hidden: true, Enabled: m.revealing,
			Run: func(module.Context, module.Values) tea.Cmd {
				return copyCmd("pending.copykey", m.reveal.key.Reveal(), "key")
			},
		},
		{
			ID: "pending.qr", Title: "Show QR code", Keys: []string{"Q"}, Hidden: true, Enabled: m.revealing,
			Run: func(module.Context, module.Values) tea.Cmd { m.reveal.showQR = !m.reveal.showQR; return nil },
		},
	}
}

func (m *Model) revealing(module.Context) (bool, string) {
	if m.reveal == nil {
		return false, "Create a key first (n)."
	}
	return true, ""
}

func copyCmd(id, text, what string) tea.Cmd {
	return func() tea.Msg {
		_, err := clip.Copy(text)
		return module.ActionResult{ActionID: id, Err: err, Message: "Copied the " + what + "."}
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func firstOr(xs []string) string {
	if len(xs) > 0 {
		return xs[0]
	}
	return ""
}

// --- view -------------------------------------------------------------------

func (m *Model) View(ctx module.Context, r module.Rect) string {
	if m.reveal != nil {
		return m.revealView(ctx, r)
	}
	s, ic := ctx.Styles(), ctx.Icons()
	now := ctx.Now()
	queueH := max(6, r.H/2)

	var lines []string
	if len(m.queue) == 0 {
		hint := "Press p to register a device by the URL or key it printed, or n for a pre-auth key."
		if n := ctx.Store().RegCount; n > 0 {
			// headscale counts pending web logins but does not say which they are.
			lines = append(lines, s.Pending.Render(fmt.Sprintf("%s headscale reports %d device(s) waiting to log in.", ic.Pending, n)),
				s.Muted.Render("It does not say which: paste the URL the device printed with p, or queue it with P."), "")
		} else {
			lines = append(lines, s.Muted.Render("No devices waiting."), "")
		}
		lines = append(lines, s.Muted.Render(hint))
	}
	w := r.W - 4
	for i, e := range m.queue {
		name := firstNonEmpty(e.Hostname, "(unknown host)")
		src := "pasted"
		if e.FromServer {
			src = "web login"
		}
		line := fmt.Sprintf("%s %-24s %-14s %s  %s", ic.Pending, name, src, humanize.Ago(e.RequestedAt, now), s.Muted.Render(e.AuthID))
		if i == m.cursor {
			line = s.RowSel.Render(table.Fit(line, w, lipgloss.Left, ic.Ellipsis))
		} else {
			line = table.Fit(line, w, lipgloss.Left, ic.Ellipsis)
		}
		lines = append(lines, line)
	}
	queue := pane.Render(s, fmt.Sprintf("Waiting to join %s %d", ic.Bullet, len(m.queue)), strings.Join(lines, "\n"), r.W, queueH, true)

	keys := pane.Render(s, "Pre-auth keys", m.keysView(ctx, w, r.H-queueH-2), r.W, r.H-queueH, false)
	return lipgloss.JoinVertical(lipgloss.Left, queue, keys)
}

func (m *Model) keysView(ctx module.Context, w, h int) string {
	s, ic := ctx.Styles(), ctx.Icons()
	now := ctx.Now()
	ks := append([]api.PreAuthKey(nil), ctx.Store().PreAuthKeys...)
	slices.SortFunc(ks, func(a, b api.PreAuthKey) int { return b.CreatedAt.Compare(a.CreatedAt) })
	if len(ks) == 0 {
		return s.Muted.Render("No pre-auth keys. Press n to create one.")
	}
	var lines []string
	for _, k := range ks {
		if len(lines) >= h {
			break
		}
		owner := "tag-only"
		if k.User != nil {
			owner = k.User.Name
		}
		var flags []string
		if k.Reusable {
			flags = append(flags, "reusable")
		}
		if k.Ephemeral {
			flags = append(flags, "ephemeral")
		}
		state := k.State(now)
		st := s.OK
		switch state {
		case api.KeyUsed:
			st = s.Muted
		case api.KeyExpired:
			st = s.Expired
		}
		line := fmt.Sprintf("%s %-22s %-10s %-20s %-8s expires %s %s",
			st.Render(ic.Key), k.Key.Masked(), owner, strings.Join(flags, ","), st.Render(state.String()),
			humanize.In(k.Expiration, now), s.Tag.Render(strings.Join(k.ACLTags, " ")))
		lines = append(lines, table.Fit(line, w, lipgloss.Left, ic.Ellipsis))
	}
	return strings.Join(lines, "\n")
}

func (m *Model) revealView(ctx module.Context, r module.Rect) string {
	s, ic := ctx.Styles(), ctx.Icons()
	key := m.reveal.key.Reveal()
	cmd := TailscaleUpCommand(ctx.ServerURL(), key)
	body := []string{
		s.Warn.Render(ic.Warn + " This is the only time the full key is shown. Copy it now."),
		"",
		s.Muted.Render("Key for " + m.reveal.user + ":"),
		s.Strong.Render(key),
		"",
		s.Muted.Render("On the device, run:"),
		s.Accent.Render(cmd),
		"",
		s.Muted.Render("c copy command · y copy key · Q QR code · esc done"),
	}
	if m.reveal.showQR {
		if q, err := qr.Render(cmd); err == nil && qr.Fits(cmd, r.W-4, r.H-len(body)-3) {
			body = append(body, "", q)
		} else {
			body = append(body, "", s.Muted.Render("The terminal is too small for the QR code."))
		}
	}
	return pane.Render(s, "New pre-auth key", strings.Join(body, "\n"), r.W, r.H, true)
}
