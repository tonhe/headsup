package app

import (
	"strconv"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tonhe/fathom/internal/api"
	"github.com/tonhe/fathom/internal/host"
	"github.com/tonhe/fathom/internal/module"
	"github.com/tonhe/fathom/internal/store"
	"github.com/tonhe/fathom/internal/ui/form"
)

// Conn is a live connection the app can switch to.
type Conn struct {
	Backend   api.Backend
	Host      host.Host
	Label     string
	ServerURL string
	ReadOnly  bool
}

// ProfileInput is what the connection editor collects.
type ProfileInput struct {
	Name     string
	URL      string
	Key      api.Secret // stored in the OS keyring, never in the config file
	Insecure bool
	ReadOnly bool
}

// Connector lets the app switch connections without restarting. All fields
// are optional; without them the editor and switcher are unavailable.
type Connector struct {
	// Profiles lists switchable targets: profile names, plus "host" when host
	// mode is usable on this machine.
	Profiles func() []string
	// Open connects to a target from Profiles.
	Open func(target string) (Conn, error)
	// Save stores a profile (key in the keyring) and returns its target name.
	Save func(ProfileInput) (string, error)
}

const (
	formConnEdit = "app.connedit"
	formSwitch   = "app.switch"
)

type switchedMsg struct {
	conn Conn
	err  error
}

// openConnEditor shows the connection editor, prefilled from the current
// connection. It is opened automatically when connecting fails with an
// authentication or reachability error.
func (m *Model) openConnEditor() tea.Cmd {
	if m.opt.Connector.Save == nil {
		return nil
	}
	m.formID = formConnEdit
	m.form = form.New(m.styles, m.icons, module.FormSpec{
		Title:  "Connect to headscale",
		Submit: "Connect",
		Fields: []module.Field{
			{ID: "name", Label: "Profile name", Value: defaultProfileName(m.opt.Profile), Required: true},
			{ID: "url", Label: "Server URL", Value: m.opt.ServerURL, Required: true, Placeholder: "https://headscale.example.com"},
			{ID: "key", Label: "API key", Kind: module.FieldSecret, Required: true,
				Help: "From `headscale apikeys create`; saved in the OS keyring"},
			{ID: "insecure", Label: "Skip TLS verify", Kind: module.FieldToggle, Value: "false", Help: "Unsafe: only for testing"},
			{ID: "readonly", Label: "Read-only", Kind: module.FieldToggle, Value: strconv.FormatBool(m.opt.ReadOnly)},
		},
	})
	return m.form.Init()
}

func defaultProfileName(label string) string {
	if label == "" || label == "demo" || len(label) > 5 && label[:5] == "host:" {
		return "default"
	}
	return label
}

func (m *Model) openSwitcher() tea.Cmd {
	c := m.opt.Connector
	if c.Profiles == nil || c.Open == nil {
		return m.toasts.Push(module.ToastInfo, "No other connections are configured.")
	}
	targets := c.Profiles()
	if len(targets) == 0 {
		return m.toasts.Push(module.ToastInfo, "No profiles yet. Add one with the connection editor (C).")
	}
	m.formID = formSwitch
	m.form = form.New(m.styles, m.icons, module.FormSpec{
		Title:  "Switch connection",
		Submit: "Connect",
		Fields: []module.Field{{ID: "target", Label: "Connect to", Kind: module.FieldSelect, Options: targets, Value: targets[0]}},
	})
	return m.form.Init()
}

// handleAppForm processes a submitted app-owned form. It reports false when
// the form belonged to a module action instead.
func (m *Model) handleAppForm(v module.Values) (tea.Cmd, bool) {
	id := m.formID
	m.formID = ""
	c := m.opt.Connector
	switch id {
	case formSwitch:
		target := v["target"]
		return func() tea.Msg { conn, err := c.Open(target); return switchedMsg{conn, err} }, true
	case formConnEdit:
		in := ProfileInput{
			Name: v["name"], URL: v["url"], Key: api.NewSecret(v["key"]),
			Insecure: v["insecure"] == "true", ReadOnly: v["readonly"] == "true",
		}
		return func() tea.Msg {
			target, err := c.Save(in)
			if err != nil {
				return switchedMsg{err: err}
			}
			conn, err := c.Open(target)
			return switchedMsg{conn, err}
		}, true
	}
	return nil, false
}

// switchTo replaces the live connection and restarts modules and the store.
func (m *Model) switchTo(c Conn) tea.Cmd {
	m.opt.Backend, m.opt.Host = c.Backend, c.Host
	m.opt.Profile, m.opt.ServerURL, m.opt.ReadOnly = c.Label, c.ServerURL, c.ReadOnly
	m.store = store.New()
	m.connected, m.connErr, m.fetching = false, nil, false
	cmds := []tea.Cmd{m.connect(), m.toasts.Push(module.ToastInfo, "Connecting to "+c.Label+"…")}
	for _, mod := range m.modules {
		cmds = append(cmds, mod.Init(m))
	}
	return tea.Batch(cmds...)
}

// shouldOfferEditor reports whether a connect error is one the user can fix
// by editing the connection.
func shouldOfferEditor(err error) bool {
	return api.IsKind(err, api.KindUnauthorized) || api.IsKind(err, api.KindUnreachable)
}
