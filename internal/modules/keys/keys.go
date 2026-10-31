// Package keys is the Keys page: pre-auth keys and API keys on two tabs, with
// create (the secret is shown once), expire and delete. API keys are
// addressed by prefix, and expiring or deleting the key this app connects
// with warns that it locks the app out.
package keys

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

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
	"github.com/tonhe/headsup/internal/ui/form"
	"github.com/tonhe/headsup/internal/ui/pane"
	"github.com/tonhe/headsup/internal/ui/qr"
	"github.com/tonhe/headsup/internal/ui/table"
	"github.com/tonhe/headsup/internal/ui/tabs"
)

func init() { registry.Register(func() module.Module { return New() }) }

// ExpiryWarn is how far ahead an expiring key is counted in the badge.
const ExpiryWarn = 7 * 24 * time.Hour

// SelfKeyer is implemented by a module.Context, or by its Backend, that
// knows the API key the app connects with. The keys module uses it to warn
// before the admin expires or deletes that key and locks the app out.
type SelfKeyer interface{ SelfAPIKey() api.Secret }

const (
	tabPreAuth = iota
	tabAPI
)

// reveal holds a freshly created secret, shown once and only in memory.
type reveal struct {
	key    api.Secret
	user   string
	api    bool
	showQR bool
}

// Model is the keys module.
type Model struct {
	tab        int
	preTbl     *table.Model
	apiTbl     *table.Model
	bar        *filter.Bar
	reveal     *reveal
	selfPrefix string
	now        time.Time
	ready      bool
	preCount   int
	apiCount   int
}

// New returns the module.
func New() *Model { return &Model{} }

func (m *Model) ID() string               { return "keys" }
func (m *Model) Title() string            { return "Keys" }
func (m *Model) Icon(ic icons.Set) string { return ic.Key }
func (m *Model) Order() int               { return 40 }
func (m *Model) Modes() module.Modes      { return module.ModeAPI }
func (m *Model) Chord() string            { return "k" }
func (m *Model) Section() module.Section  { return module.SectionTailnet }
func (m *Model) Capturing() bool          { return m.bar != nil && m.bar.Active() }

// Badge is the number of usable keys that expire within ExpiryWarn (T120).
func (m *Model) Badge(st *store.Store) string {
	now := m.now
	if now.IsZero() {
		now = st.UpdatedAt
	}
	if n := st.Counts(now, ExpiryWarn).ExpiringKeys; n > 0 {
		return strconv.Itoa(n)
	}
	return ""
}

func (m *Model) Init(ctx module.Context) tea.Cmd {
	m.preTbl = table.New(ctx.Styles(), ctx.Icons(), preAuthColumns())
	m.preTbl.SetMulti(false)
	m.preTbl.SetFocused(true)
	m.preTbl.SetSort("created", true)
	m.apiTbl = table.New(ctx.Styles(), ctx.Icons(), apiColumns())
	m.apiTbl.SetMulti(false)
	m.apiTbl.SetFocused(true)
	m.apiTbl.SetSort("created", true)
	m.bar = filter.NewBar(ctx.Styles(), ctx.Icons())
	m.selfPrefix = selfPrefix(ctx)
	return nil
}

func selfPrefix(ctx module.Context) string {
	if sk, ok := ctx.(SelfKeyer); ok {
		if p := PrefixOf(sk.SelfAPIKey().Reveal()); p != "" {
			return p
		}
	}
	if b := ctx.Backend(); b != nil {
		if sk, ok := b.(SelfKeyer); ok {
			return PrefixOf(sk.SelfAPIKey().Reveal())
		}
	}
	return ""
}

// PrefixOf extracts the identifying prefix from a full API key: the 12
// characters after "hskey-api-" in the current format (the prefix alphabet
// includes '-', so it is cut by length), or the part before '.' in the
// legacy "prefix.secret" format. It returns "" for anything else.
func PrefixOf(secret string) string {
	const newFmt, newLen = "hskey-api-", 12
	if rest, ok := strings.CutPrefix(secret, newFmt); ok {
		if len(rest) > newLen {
			return rest[:newLen]
		}
		return ""
	}
	if i := strings.IndexByte(secret, '.'); i > 0 {
		return secret[:i]
	}
	return ""
}

// IsSelf reports whether a listed API key prefix is the app's own key.
func IsSelf(listed, self string) bool {
	listed = strings.TrimPrefix(listed, "hskey-api-")
	return listed != "" && self != "" && listed == self
}

func (m *Model) rebuild(ctx module.Context) {
	st := ctx.Store()
	m.now = ctx.Now()
	m.selfPrefix = selfPrefix(ctx)
	s, ic := ctx.Styles(), ctx.Icons()
	q := m.bar.Query()
	var pre, apis []table.Row
	for _, k := range st.PreAuthKeys {
		r := preRow{k: k, s: s, ic: ic, now: m.now}
		if q.Empty() || q.Match(r) {
			pre = append(pre, r)
		}
	}
	for _, k := range st.APIKeys {
		r := apiRow{k: k, s: s, ic: ic, now: m.now, self: IsSelf(k.Prefix, m.selfPrefix)}
		if q.Empty() || q.Match(r) {
			apis = append(apis, r)
		}
	}
	m.preTbl.SetRows(pre)
	m.apiTbl.SetRows(apis)
	m.preCount, m.apiCount = len(st.PreAuthKeys), len(st.APIKeys)
	total := m.preCount
	shown := len(pre)
	if m.tab == tabAPI {
		total, shown = m.apiCount, len(apis)
	}
	m.bar.SetCounts(shown, total)
	m.ready = st.Loaded&(store.SlicePreAuthKeys|store.SliceAPIKeys) != 0
}

type keyCreatedMsg struct {
	key  api.Secret
	user string
	api  bool
	err  error
}

func (m *Model) Update(ctx module.Context, msg tea.Msg) (module.Module, tea.Cmd) {
	switch msg := msg.(type) {
	case store.UpdatedMsg:
		m.rebuild(ctx)
	case module.NavigateMsg:
		if a, ok := msg.Args.(module.FilterArgs); ok {
			m.tab = tabPreAuth
			m.bar.SetValue(a.Query)
		}
		m.rebuild(ctx)
	case filter.ChangedMsg:
		m.rebuild(ctx)
	case keyCreatedMsg:
		sl := store.SlicePreAuthKeys
		id := "keys.new"
		if msg.api {
			sl = store.SliceAPIKeys
		}
		if msg.err == nil {
			m.reveal = &reveal{key: msg.key, user: msg.user, api: msg.api}
		}
		res := module.ActionResult{ActionID: id, Err: msg.err, Refresh: sl, Silent: true}
		return m, func() tea.Msg { return res }
	case tea.KeyMsg:
		if m.reveal != nil {
			if k := msg.String(); k == "esc" || k == "enter" {
				m.reveal = nil
			}
			return m, nil
		}
		if m.bar.Active() || msg.String() == "/" {
			return m, m.bar.Update(msg)
		}
		switch msg.String() {
		case "tab", "shift+tab":
			m.tab = 1 - m.tab
			m.rebuild(ctx)
			return m, nil
		case "esc":
			if m.bar.Value() != "" {
				return m, m.bar.Clear()
			}
		}
		return m, m.table().Update(msg)
	}
	return m, nil
}

func (m *Model) table() *table.Model {
	if m.tab == tabAPI {
		return m.apiTbl
	}
	return m.preTbl
}

func (m *Model) curPre() (api.PreAuthKey, bool) {
	if m.preTbl == nil || m.tab != tabPreAuth {
		return api.PreAuthKey{}, false
	}
	r, ok := m.preTbl.Cursor()
	if !ok {
		return api.PreAuthKey{}, false
	}
	return r.(preRow).k, true
}

func (m *Model) curAPI() (api.APIKey, bool) {
	if m.apiTbl == nil || m.tab != tabAPI {
		return api.APIKey{}, false
	}
	r, ok := m.apiTbl.Cursor()
	if !ok {
		return api.APIKey{}, false
	}
	return r.(apiRow).k, true
}

// curSelf reports whether the cursor is on the app's own API key.
func (m *Model) curSelf() bool {
	k, ok := m.curAPI()
	return ok && IsSelf(k.Prefix, m.selfPrefix)
}

func (m *Model) Hints() []module.Hint {
	switch {
	case m.reveal != nil && m.reveal.api:
		return []module.Hint{{Key: "y", Desc: "copy key"}, {Key: "esc", Desc: "done"}}
	case m.reveal != nil:
		return []module.Hint{{Key: "c", Desc: "copy command"}, {Key: "y", Desc: "copy key"}, {Key: "Q", Desc: "QR"}, {Key: "esc", Desc: "done"}}
	case m.bar != nil && m.bar.Active():
		return []module.Hint{{Key: "enter", Desc: "keep filter"}, {Key: "esc", Desc: "clear"}}
	}
	return []module.Hint{
		{Key: "tab", Desc: "pre-auth/API"}, {Key: "/", Desc: "filter"}, {Key: "n", Desc: "new"},
		{Key: "e", Desc: "expire"}, {Key: "D", Desc: "delete"},
	}
}

// --- view -------------------------------------------------------------------

func (m *Model) View(ctx module.Context, r module.Rect) string {
	s, ic := ctx.Styles(), ctx.Icons()
	if m.preTbl == nil {
		return ""
	}
	if m.reveal != nil {
		return m.revealView(ctx, r)
	}
	if !m.ready {
		return empty.Render(s, "Loading keys…", "", "", r.W, r.H)
	}
	active := "preauth"
	if m.tab == tabAPI {
		active = "api"
	}
	items := []tabs.Item{
		{ID: "preauth", Title: "Pre-auth keys", Icon: ic.Key, Badge: strconv.Itoa(m.preCount)},
		{ID: "api", Title: "API keys", Icon: ic.Lock, Badge: strconv.Itoa(m.apiCount)},
	}
	top := []string{tabs.Render(s, ic, items, active, r.W)}
	if m.bar.Active() || m.bar.Value() != "" {
		top = append(top, m.bar.View(r.W))
	}
	h := r.H - len(top)

	t := m.table()
	var body, title string
	var opts []pane.Opt
	if m.tab == tabPreAuth {
		title = fmt.Sprintf("Pre-auth keys %s %d", ic.Bullet, t.Len())
		opts = append(opts, pane.WithBottom("n new · e expire · D delete"))
	} else {
		title = fmt.Sprintf("API keys %s %d", ic.Bullet, t.Len())
		if m.selfPrefix != "" {
			opts = append(opts, pane.WithExtra(s.Muted.Render("this app uses "+m.selfPrefix)))
		}
		opts = append(opts, pane.WithBottom("keys are addressed by prefix"))
	}
	iw, ih := pane.Inner(r.W, h)
	switch {
	case t.Len() > 0:
		t.SetSize(iw, ih)
		body = t.View()
	case m.bar.Value() != "":
		body = s.Muted.Render("No keys match the filter. Press esc to clear it.")
	case m.tab == tabPreAuth:
		body = s.Muted.Render("No pre-auth keys. Press n to create one.")
	default:
		body = s.Muted.Render("No API keys. Press n to create one.")
	}
	list := pane.Render(s, title, body, r.W, h, true, opts...)
	return lipgloss.JoinVertical(lipgloss.Left, append(top, list)...)
}

// TailscaleUpCommand is the command a device runs to join with key.
func TailscaleUpCommand(serverURL, key string) string {
	return "tailscale up --login-server=" + serverURL + " --authkey=" + key
}

func (m *Model) revealView(ctx module.Context, r module.Rect) string {
	s, ic := ctx.Styles(), ctx.Icons()
	key := m.reveal.key.Reveal()
	// Long keys and commands wrap rather than being clipped by the pane.
	iw, _ := pane.Inner(r.W, r.H)
	wrap := func(st lipgloss.Style, text string) string { return st.Width(iw).Render(text) }
	if m.reveal.api {
		body := []string{
			s.Warn.Render(ic.Warn + " This is the only time the full API key is shown. Copy it now."),
			"",
			wrap(s.Strong, key),
			"",
			s.Muted.Render("Prefix " + PrefixOf(key) + " identifies it in the list."),
			"",
			s.Muted.Render("y copy key · esc done"),
		}
		return pane.Render(s, "New API key", strings.Join(body, "\n"), r.W, r.H, true)
	}
	cmd := TailscaleUpCommand(ctx.ServerURL(), key)
	body := []string{
		s.Warn.Render(ic.Warn + " This is the only time the full key is shown. Copy it now."),
		"",
		s.Muted.Render("Key for " + m.reveal.user + ":"),
		wrap(s.Strong, key),
		"",
		s.Muted.Render("On the device, run:"),
		wrap(s.Accent, cmd),
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

// --- actions ----------------------------------------------------------------

func call(f func(context.Context) error) error {
	ctx, cancel := api.WithTimeout(context.Background())
	defer cancel()
	return f(ctx)
}

func userOptions(st *store.Store) []string {
	out := make([]string, 0, len(st.Users))
	for _, u := range st.Users {
		out = append(out, u.Name)
	}
	slices.Sort(out)
	return out
}

func preName(k api.PreAuthKey) string {
	owner := "tag-only"
	if k.User != nil {
		owner = k.User.Name
	}
	return k.Key.Masked() + " (" + owner + ")"
}

const lockout = "This is the API key this app connects with. Once it stops working, this app is locked out " +
	"until you configure a new key (create one first with n)."

func (m *Model) Actions() []module.Action {
	notRevealing := func() (bool, string) {
		if m.reveal != nil {
			return false, "Close the new key first (esc)."
		}
		return true, ""
	}
	target := func(module.Context) []module.Target {
		if k, ok := m.curPre(); ok {
			return []module.Target{{Kind: "preauthkey", ID: strconv.FormatUint(k.ID, 10), Name: preName(k)}}
		}
		if k, ok := m.curAPI(); ok {
			return []module.Target{{Kind: "apikey", ID: k.Prefix, Name: k.Prefix}}
		}
		return nil
	}
	hasKey := func(ctx module.Context) (bool, string) {
		if ok, why := notRevealing(); !ok {
			return ok, why
		}
		_, p := m.curPre()
		_, a := m.curAPI()
		if !p && !a {
			return false, "No key selected."
		}
		return true, ""
	}
	revealing := func(apiOK bool) func(module.Context) (bool, string) {
		return func(module.Context) (bool, string) {
			switch {
			case m.reveal == nil:
				return false, "The full key is only shown when it is created (n)."
			case m.reveal.api && !apiOK:
				return false, "API keys have no tailscale up command."
			}
			return true, ""
		}
	}

	expire := module.Action{
		ID: "keys.expire", Title: "Expire key", Keys: []string{"e"}, Scope: module.ScopeSelection,
		Mutating: true, Danger: module.DangerConfirm, Targets: target,
		Consequence: "The key stops working immediately.",
		Enabled: func(ctx module.Context) (bool, string) {
			if ok, why := hasKey(ctx); !ok {
				return ok, why
			}
			now := ctx.Now()
			if k, ok := m.curPre(); ok && expiredAt(k.Expiration, now) {
				return false, "That key has already expired."
			}
			if k, ok := m.curAPI(); ok && expiredAt(k.Expiration, now) {
				return false, "That key has already expired."
			}
			return true, ""
		},
		Run: func(ctx module.Context, _ module.Values) tea.Cmd {
			b := ctx.Backend()
			if k, ok := m.curPre(); ok {
				return func() tea.Msg {
					err := call(func(c context.Context) error { return b.ExpirePreAuthKey(c, k.ID) })
					return module.ActionResult{ActionID: "keys.expire", Err: err, Refresh: store.SlicePreAuthKeys,
						Message: "Expired pre-auth key " + preName(k) + "."}
				}
			}
			k, _ := m.curAPI()
			return func() tea.Msg {
				err := call(func(c context.Context) error { return b.ExpireAPIKey(c, k.Prefix) })
				return module.ActionResult{ActionID: "keys.expire", Err: err, Refresh: store.SliceAPIKeys,
					Message: "Expired API key " + k.Prefix + "."}
			}
		},
	}
	del := module.Action{
		ID: "keys.delete", Title: "Delete key", Keys: []string{"D"}, Scope: module.ScopeSelection,
		Mutating: true, Danger: module.DangerCritical, Targets: target, Enabled: hasKey,
		Cap: api.CapPreAuthKeyDelete, Consequence: "The key is removed permanently.",
		Run: func(ctx module.Context, _ module.Values) tea.Cmd {
			b := ctx.Backend()
			if k, ok := m.curPre(); ok {
				return func() tea.Msg {
					err := call(func(c context.Context) error { return b.DeletePreAuthKey(c, k.ID) })
					return module.ActionResult{ActionID: "keys.delete", Err: err, Refresh: store.SlicePreAuthKeys,
						Message: "Deleted pre-auth key " + preName(k) + "."}
				}
			}
			k, _ := m.curAPI()
			return func() tea.Msg {
				err := call(func(c context.Context) error { return b.DeleteAPIKey(c, k.Prefix) })
				return module.ActionResult{ActionID: "keys.delete", Err: err, Refresh: store.SliceAPIKeys,
					Message: "Deleted API key " + k.Prefix + "."}
			}
		},
	}
	if m.tab == tabAPI {
		expire.Title, del.Title = "Expire API key", "Delete API key"
		del.Cap = api.CapAPIKeyDelete
	}
	if m.curSelf() {
		// T118: the self-lockout warning. Critical makes the admin type the prefix.
		expire.Danger = module.DangerCritical
		expire.Title = "Expire the API key this app uses"
		expire.Consequence = lockout
		del.Title = "Delete the API key this app uses"
		del.Consequence = lockout
	}

	return []module.Action{
		m.newAction(notRevealing),
		expire,
		del,
		{
			ID: "keys.copycmd", Title: "Copy tailscale up command", Keys: []string{"c"}, Enabled: revealing(false),
			Run: func(ctx module.Context, _ module.Values) tea.Cmd {
				return copyCmd("keys.copycmd", TailscaleUpCommand(ctx.ServerURL(), m.reveal.key.Reveal()), "command")
			},
		},
		{
			ID: "keys.copykey", Title: "Copy new key", Keys: []string{"y"}, Hidden: true, Enabled: revealing(true),
			Run: func(module.Context, module.Values) tea.Cmd {
				return copyCmd("keys.copykey", m.reveal.key.Reveal(), "key")
			},
		},
		{
			// "q" quits the app, so the QR toggle is on the capital, as on Add device.
			ID: "keys.qr", Title: "Show QR code", Keys: []string{"Q"}, Hidden: true, Enabled: revealing(false),
			Run: func(module.Context, module.Values) tea.Cmd { m.reveal.showQR = !m.reveal.showQR; return nil },
		},
	}
}

func (m *Model) newAction(notRevealing func() (bool, string)) module.Action {
	if m.tab == tabAPI {
		return module.Action{
			ID: "keys.new", Title: "New API key", Keys: []string{"n"}, Mutating: true,
			Enabled: func(module.Context) (bool, string) { return notRevealing() },
			Params: func(module.Context) *module.FormSpec {
				return &module.FormSpec{Title: "New API key", Submit: "Create", Fields: []module.Field{
					{ID: "expiration", Label: "Expires in", Kind: module.FieldDuration, Value: "90d", Required: true,
						Help: "The key is shown once, right after it is created."},
				}}
			},
			Run: func(ctx module.Context, v module.Values) tea.Cmd {
				b, now := ctx.Backend(), ctx.Now()
				return func() tea.Msg {
					d, err := form.ParseDuration(v["expiration"])
					if err != nil {
						return keyCreatedMsg{api: true, err: err}
					}
					var sec api.Secret
					err = call(func(c context.Context) (err error) { sec, err = b.CreateAPIKey(c, now.Add(d)); return })
					return keyCreatedMsg{key: sec, api: true, err: err}
				}
			},
		}
	}
	return module.Action{
		ID: "keys.new", Title: "New pre-auth key", Keys: []string{"n"}, Mutating: true,
		Enabled: func(ctx module.Context) (bool, string) {
			if ok, why := notRevealing(); !ok {
				return ok, why
			}
			if len(ctx.Store().Users) == 0 {
				return false, "Create a user first."
			}
			return true, ""
		},
		Params: func(ctx module.Context) *module.FormSpec {
			users := userOptions(ctx.Store())
			first := ""
			if len(users) > 0 {
				first = users[0]
			}
			return &module.FormSpec{Title: "New pre-auth key", Submit: "Create", Fields: []module.Field{
				{ID: "user", Label: "User", Kind: module.FieldSelect, Options: users, Value: first},
				{ID: "reusable", Label: "Reusable", Kind: module.FieldToggle, Value: "false", Help: "Can register more than one device"},
				{ID: "ephemeral", Label: "Ephemeral", Kind: module.FieldToggle, Value: "false", Help: "Devices are removed when they go offline"},
				{ID: "expiration", Label: "Expires in", Kind: module.FieldDuration, Value: "24h", Required: true},
				{ID: "tags", Label: "Tags", Kind: module.FieldTags, Help: "Optional, e.g. server, prod"},
			}}
		},
		Run: func(ctx module.Context, v module.Values) tea.Cmd {
			b, now := ctx.Backend(), ctx.Now()
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
						Expiration: now.Add(d), ACLTags: tags,
					})
					return
				})
				return keyCreatedMsg{key: k.Key, user: v["user"], err: err}
			}
		},
	}
}

func copyCmd(id, text, what string) tea.Cmd {
	return func() tea.Msg {
		_, err := clip.Copy(text)
		return module.ActionResult{ActionID: id, Err: err, Message: "Copied the " + what + "."}
	}
}
