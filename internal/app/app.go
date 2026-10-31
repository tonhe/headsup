// Package app is the root Bubble Tea model: layout, modal stack, global keys,
// the refresh loop, and the action pipeline every module action runs through.
package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tonhe/headsup/internal/activity"
	"github.com/tonhe/headsup/internal/api"
	"github.com/tonhe/headsup/internal/brand"
	"github.com/tonhe/headsup/internal/config"
	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/icons"
	"github.com/tonhe/headsup/internal/keys"
	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/store"
	"github.com/tonhe/headsup/internal/theme"
	"github.com/tonhe/headsup/internal/ui/badge"
	"github.com/tonhe/headsup/internal/ui/confirm"
	"github.com/tonhe/headsup/internal/ui/form"
	"github.com/tonhe/headsup/internal/ui/header"
	"github.com/tonhe/headsup/internal/ui/help"
	"github.com/tonhe/headsup/internal/ui/modal"
	"github.com/tonhe/headsup/internal/ui/palette"
	"github.com/tonhe/headsup/internal/ui/sidebar"
	"github.com/tonhe/headsup/internal/ui/statusbar"
	"github.com/tonhe/headsup/internal/ui/styles"
	"github.com/tonhe/headsup/internal/ui/tabs"
	"github.com/tonhe/headsup/internal/ui/themepicker"
	"github.com/tonhe/headsup/internal/ui/toast"
	"github.com/tonhe/headsup/internal/watch"
)

// MinWidth and MinHeight are the smallest supported terminal size (SC-006).
const (
	MinWidth  = 80
	MinHeight = 24
	// SidebarBreakpoint is the width at which the sidebar replaces the tab bar.
	SidebarBreakpoint = 100
	sidebarWidth      = 22
)

// Options configure the app.
type Options struct {
	Backend      api.Backend
	Host         host.Host // nil in remote mode
	Modules      []module.Module
	Profile      string // display label, e.g. "host:hs01" or "homelab"
	ServerURL    string
	ReadOnly     bool
	Theme        string
	Icons        string
	Sidebar      string // auto | always | never
	Refresh      time.Duration
	RefreshIdle  time.Duration
	ExpiryWarn   time.Duration
	KeyOverrides map[string][]string
	Activity     activity.Logger
	Connector    Connector
	// Config is the loaded config file. Preference changes (theme, icons,
	// per-profile theme, ...) are written into it and saved to ConfigPath.
	// nil means built-in defaults.
	Config *config.Config
	// ConfigPath is where Config is saved; empty keeps changes for this
	// session only.
	ConfigPath string
	// ThemesDir is the user theme directory, reloaded from the theme picker.
	ThemesDir string
	// Mouse says mouse reporting is on (ui.mouse); the program must have
	// been started with mouse support for clicks to arrive.
	Mouse bool
	// Notifier delivers [notify] desktop and bell notifications; nil uses
	// SystemNotifier.
	Notifier Notifier
	// Now is injectable for tests.
	Now func() time.Time
}

// Model is the root model. It also implements module.Context.
type Model struct {
	opt     Options
	w, h    int
	styles  *styles.Styles
	icons   icons.Set
	store   *store.Store
	modules []module.Module
	active  int
	keymap  keys.Map
	chord   string

	toasts  *toast.Stack
	form    *form.Model
	confirm *confirm.Model
	help    *help.Model
	palette *palette.Model
	picker  *themepicker.Model
	recent  []string // palette item IDs, most recent first
	mouse   bool

	formID    string // app-owned form in progress (connection editor, switcher)
	pending   *module.Action
	pendVals  module.Values
	started   map[string]time.Time
	mutating  map[string]bool // started actions: whether each one mutates
	targets   map[string][]module.Target
	fetching  bool
	connected bool
	connErr   error
	showSide  bool
	quitting  bool
	watcher   *watch.Watcher
}

// New builds the root model.
func New(opt Options) *Model {
	if opt.Now == nil {
		opt.Now = time.Now
	}
	if opt.Refresh <= 0 {
		opt.Refresh = 5 * time.Second
	}
	if opt.ExpiryWarn <= 0 {
		opt.ExpiryWarn = 7 * 24 * time.Hour
	}
	// Without a persistent log (the demo passes Nop) keep this session's
	// entries in memory so the Activity page still lists them.
	if _, nop := opt.Activity.(activity.Nop); nop || opt.Activity == nil {
		opt.Activity = &activity.Memory{}
	}
	if opt.Notifier == nil {
		opt.Notifier = SystemNotifier{}
	}
	t := theme.ByName(opt.Theme)
	if t == nil {
		t = theme.ByName(theme.Default)
	}
	theme.Set(*t)
	if opt.Config == nil {
		c := config.Default()
		c.UI.Theme, c.UI.Mouse = t.Slug, opt.Mouse
		if opt.Icons != "" {
			c.UI.Icons = opt.Icons
		}
		if opt.Sidebar != "" {
			c.UI.Sidebar = opt.Sidebar
		}
		opt.Config = &c
	}
	ic := icons.ByName(opt.Icons)
	m := &Model{
		opt:      opt,
		styles:   styles.New(*t),
		icons:    ic,
		store:    store.New(),
		modules:  opt.Modules,
		started:  map[string]time.Time{},
		mutating: map[string]bool{},
		targets:  map[string][]module.Target{},
		showSide: opt.Sidebar != "never",
		mouse:    opt.Mouse,
		watcher:  watch.New(opt.ExpiryWarn),
	}
	m.toasts = toast.New(m.styles, ic)
	m.keymap, _ = keys.Merge(m.defaultBindings(), opt.KeyOverrides)
	return m
}

// --- module.Context -------------------------------------------------------

func (m *Model) Backend() api.Backend   { return m.opt.Backend }
func (m *Model) Host() host.Host        { return m.opt.Host }
func (m *Model) Store() *store.Store    { return m.store }
func (m *Model) Styles() *styles.Styles { return m.styles }
func (m *Model) Icons() icons.Set       { return m.icons }
func (m *Model) ReadOnly() bool         { return m.opt.ReadOnly }
func (m *Model) HostMode() bool         { return m.opt.Host != nil }
func (m *Model) ServerURL() string      { return m.opt.ServerURL }
func (m *Model) Profile() string        { return m.opt.Profile }

// Activity is the activity logger, for the activity module.
func (m *Model) Activity() activity.Logger { return m.opt.Activity }

// HostSettings hands the [host] config to the server module.
func (m *Model) HostSettings() config.Host { return m.opt.Config.Host }
func (m *Model) Now() time.Time            { return m.opt.Now() }
func (m *Model) Refresh() tea.Cmd          { return m.fetch(store.SliceAll) }
func (m *Model) Toast(l module.ToastLevel, msg string) tea.Cmd {
	return m.toasts.Push(l, msg)
}

type navMsg struct {
	id   string
	args any
}

func (m *Model) Navigate(id string, args any) tea.Cmd {
	return func() tea.Msg { return navMsg{id, args} }
}

// --- messages -------------------------------------------------------------

type connectedMsg struct {
	info api.ServerInfo
	err  error
}
type snapshotMsg struct{ sn store.Snapshot }
type tickMsg time.Time

// --- tea.Model ------------------------------------------------------------

func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.connect()}
	for _, mod := range m.modules {
		cmds = append(cmds, mod.Init(m))
	}
	return tea.Batch(cmds...)
}

func (m *Model) connect() tea.Cmd {
	b := m.opt.Backend
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), api.DefaultTimeout)
		defer cancel()
		info, err := b.Connect(ctx)
		return connectedMsg{info, err}
	}
}

func (m *Model) fetch(sl store.Slice) tea.Cmd {
	if m.fetching || !m.connected {
		return nil
	}
	m.fetching = true
	b, h := m.opt.Backend, m.opt.Host
	if h != nil {
		sl |= store.SliceHost
	}
	return func() tea.Msg {
		return snapshotMsg{store.Fetch(context.Background(), b, h, sl)}
	}
}

func (m *Model) tick() tea.Cmd {
	return tea.Tick(m.opt.Refresh, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		if m.help != nil {
			m.help.SetSize(m.w-8, m.h-6)
		}
		m.sizeModals()
		return m, nil

	case connectedMsg:
		if msg.err != nil {
			m.connErr = msg.err
			e := api.AsError(msg.err)
			text := e.Error()
			if e.Hint != "" {
				text += ". " + e.Hint
			}
			cmds := []tea.Cmd{m.toasts.Push(module.ToastError, text)}
			if shouldOfferEditor(msg.err) && m.form == nil {
				cmds = append(cmds, m.openConnEditor())
			}
			return m, tea.Batch(cmds...)
		}
		m.connected, m.connErr = true, nil
		m.store.Info = msg.info
		cmds := []tea.Cmd{m.fetch(store.SliceAll), m.tick()}
		if !api.Supported(msg.info.Version) {
			cmds = append(cmds, m.toasts.Push(module.ToastWarn,
				fmt.Sprintf("headscale %s is not a supported version (0.29.x, 0.30.x); some features may not work", msg.info.Version)))
		}
		return m, tea.Batch(cmds...)

	case snapshotMsg:
		m.fetching = false
		m.store.Apply(msg.sn)
		m.store.UpdatedAt = m.opt.Now()
		evs := m.watcher.Observe(m.store, msg.sn.Slices, m.opt.Now())
		return m, tea.Batch(m.broadcast(store.UpdatedMsg{Slices: msg.sn.Slices}), m.onEvents(evs))

	case tickMsg:
		return m, tea.Batch(m.fetch(store.SliceAll), m.tick())

	case navMsg:
		for i, mod := range m.modules {
			if mod.ID() == msg.id {
				m.active = i
				return m, m.updateModule(i, module.NavigateMsg{Args: msg.args})
			}
		}
		return m, nil

	case module.ActionResult:
		return m, m.finishAction(msg)

	case toast.DismissMsg:
		return m, m.toasts.Update(msg)

	case switchedMsg:
		if msg.err != nil {
			return m, m.toasts.Push(module.ToastError, "Could not connect: "+msg.err.Error())
		}
		return m, m.switchTo(msg.conn)

	case form.SubmitMsg:
		m.form = nil
		if cmd, ok := m.handleAppForm(msg.Values); ok {
			return m, cmd
		}
		if m.pending == nil {
			return m, nil
		}
		m.pendVals = msg.Values
		return m, m.confirmOrRun()

	case form.CancelMsg:
		m.form, m.pending, m.formID = nil, nil, ""
		return m, nil

	case confirm.ResultMsg:
		m.confirm = nil
		if msg.ID == quitConfirmID {
			if msg.OK {
				m.quitting = true
				return m, tea.Quit
			}
			return m, nil
		}
		if m.pending == nil || !msg.OK {
			m.pending = nil
			return m, nil
		}
		return m, m.run()

	case help.CloseMsg:
		m.help = nil
		return m, nil

	case tea.KeyMsg:
		return m, m.handleKey(msg)

	case tea.MouseMsg:
		return m, m.handleMouse(msg)
	}
	if cmd, ok := m.updateOverlays(msg); ok {
		return m, cmd
	}

	// Unknown messages go to the active module (results of its own commands)
	// and to modal components that may own them (blink, spinner ticks).
	var cmds []tea.Cmd
	if m.form != nil {
		cmds = append(cmds, m.form.Update(msg))
	}
	cmds = append(cmds, m.broadcast(msg))
	return m, tea.Batch(cmds...)
}

// broadcast forwards a message to every module; inactive modules use it to
// keep their caches current (e.g. a new snapshot).
func (m *Model) broadcast(msg tea.Msg) tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(m.modules))
	for i := range m.modules {
		cmds = append(cmds, m.updateModule(i, msg))
	}
	return tea.Batch(cmds...)
}

func (m *Model) updateModule(i int, msg tea.Msg) tea.Cmd {
	mod, cmd := m.modules[i].Update(m, msg)
	m.modules[i] = mod
	return cmd
}

func (m *Model) current() module.Module {
	if len(m.modules) == 0 {
		return nil
	}
	return m.modules[m.active]
}

// --- keys -----------------------------------------------------------------

func (m *Model) handleKey(k tea.KeyMsg) tea.Cmd {
	switch {
	case m.confirm != nil:
		return m.confirm.Update(k)
	case m.form != nil:
		return m.form.Update(k)
	case m.help != nil:
		return m.help.Update(k)
	case m.palette != nil:
		return m.palette.Update(k)
	case m.picker != nil:
		return m.picker.Update(k)
	}
	if k.String() == "ctrl+c" {
		return m.quit()
	}
	cur := m.current()
	if cur != nil && cur.Capturing() {
		return m.updateModule(m.active, k)
	}

	key := k.String()
	if key == " " {
		key = "space"
	}
	if m.chord != "" {
		key = m.chord + " " + key
		m.chord = ""
	} else if m.keymap.Pending(m.scope(), key) {
		m.chord = key
		return nil
	}

	if id, ok := m.keymap.Lookup(m.scope(), key); ok {
		if cmd, handled := m.global(id); handled {
			return cmd
		}
		if a, ok := m.findAction(id); ok {
			return m.invoke(a)
		}
	}
	if cur == nil {
		return nil
	}
	return m.updateModule(m.active, k)
}

func (m *Model) scope() string {
	if cur := m.current(); cur != nil {
		return cur.ID()
	}
	return keys.GlobalScope
}

func (m *Model) findAction(id string) (module.Action, bool) {
	cur := m.current()
	if cur == nil {
		return module.Action{}, false
	}
	for _, a := range cur.Actions() {
		if a.ID == id {
			return a, true
		}
	}
	return module.Action{}, false
}

// Chorder lets a module declare its "g x" jump key.
type Chorder interface{ Chord() string }

func (m *Model) defaultBindings() map[string][]string {
	b := map[string][]string{}
	for _, a := range appActions {
		b[a.id] = a.keys
	}
	for i, mod := range m.modules {
		var ks []string
		if i < 9 {
			ks = append(ks, fmt.Sprint(i+1))
		}
		if c, ok := mod.(Chorder); ok && c.Chord() != "" {
			ks = append(ks, "g "+c.Chord())
		}
		if j, ok := mod.(Jumper); ok {
			ks = append(ks, j.JumpKeys()...)
		}
		if len(ks) > 0 {
			b["app.goto."+mod.ID()] = ks
		}
		for _, a := range mod.Actions() {
			if len(a.Keys) > 0 {
				b[a.ID] = a.Keys
			}
		}
	}
	return b
}

func (m *Model) global(id string) (tea.Cmd, bool) {
	switch {
	case id == "app.quit":
		return m.quit(), true
	case id == "app.help":
		m.help = help.New(m.styles, m.helpSections())
		m.help.SetSize(m.w-8, m.h-6)
		return nil, true
	case id == "app.palette":
		return m.openPalette(), true
	case id == "app.theme":
		return m.openThemePicker(), true
	case strings.HasPrefix(id, "app.theme."):
		return m.chooseTheme(strings.TrimPrefix(id, "app.theme."), false), true
	case id == "app.refresh":
		m.fetching = false
		return m.fetch(store.SliceAll), true
	case id == "app.sidebar":
		m.showSide = !m.showSide
		return nil, true
	case id == "app.next" && len(m.modules) > 0:
		m.active = (m.active + 1) % len(m.modules)
		return nil, true
	case id == "app.prev" && len(m.modules) > 0:
		m.active = (m.active + len(m.modules) - 1) % len(m.modules)
		return nil, true
	case id == "app.switch":
		return m.openSwitcher(), true
	case id == "app.conn":
		if m.opt.Connector.Save == nil {
			return m.toasts.Push(module.ToastInfo, "Editing connections is not available here."), true
		}
		return m.openConnEditor(), true
	case strings.HasPrefix(id, "app.goto."):
		return m.Navigate(strings.TrimPrefix(id, "app.goto."), nil), true
	}
	return nil, false
}

// --- action pipeline --------------------------------------------------------

func (m *Model) gateEnv() GateEnv {
	env := GateEnv{ReadOnly: m.opt.ReadOnly, Caps: m.opt.Backend.Capabilities(), HostMode: m.HostMode()}
	if m.opt.Host != nil {
		env.Perms = m.opt.Host.Env().Perms
	}
	return env
}

// invoke starts the pipeline for an action: gate, then form, then confirm, then run.
func (m *Model) invoke(a module.Action) tea.Cmd {
	v := Gate(a, m, m.gateEnv())
	if !v.Allowed {
		msg := v.Reason
		if v.FixCommand != "" {
			msg += " Fix: " + v.FixCommand
		}
		return m.toasts.Push(module.ToastWarn, msg)
	}
	var tg []module.Target
	if a.Targets != nil {
		tg = a.Targets(m)
	}
	if a.Scope == module.ScopeSelection && len(tg) == 0 {
		return m.toasts.Push(module.ToastInfo, "Select something first.")
	}
	m.targets[a.ID] = tg
	act := a
	m.pending, m.pendVals = &act, nil
	if a.Params != nil {
		if spec := a.Params(m); spec != nil {
			m.form = form.New(m.styles, m.icons, *spec)
			return m.form.Init()
		}
	}
	return m.confirmOrRun()
}

func (m *Model) confirmOrRun() tea.Cmd {
	a := m.pending
	d := NeedsConfirm(*a)
	if !confirm.Needed(d) {
		return m.run()
	}
	req := confirm.Request{
		ID:          a.ID,
		Title:       a.Title,
		Danger:      d,
		Targets:     m.targets[a.ID],
		Consequence: a.Consequence,
	}
	if a.ConfirmText != nil && d == module.DangerCritical {
		req.Expect = a.ConfirmText(m)
	}
	m.confirm = confirm.New(m.styles, m.icons, req)
	return m.confirm.Init()
}

func (m *Model) run() tea.Cmd {
	a := m.pending
	m.pending = nil
	if a == nil || a.Run == nil {
		return nil
	}
	m.started[a.ID] = m.opt.Now()
	m.mutating[a.ID] = a.Mutating
	return a.Run(m, m.pendVals)
}

func (m *Model) finishAction(r module.ActionResult) tea.Cmd {
	start, ok := m.started[r.ActionID]
	delete(m.started, r.ActionID)
	targets := m.targets[r.ActionID]
	delete(m.targets, r.ActionID)
	mut, known := m.mutating[r.ActionID]
	delete(m.mutating, r.ActionID)
	// The activity log is for changes (FR-037): skip results of actions known
	// not to mutate, unless they still report a file write or a backup.
	record := !known || mut || len(r.Files) > 0 || r.BackupID != ""

	e := activity.Entry{Time: m.opt.Now(), Profile: m.opt.Profile, ActionID: r.ActionID, Result: activity.OK()}
	if ok {
		e.DurationMS = m.opt.Now().Sub(start).Milliseconds()
	}
	for _, t := range targets {
		e.Targets = append(e.Targets, activity.Target{Kind: t.Kind, ID: t.ID, Name: t.Name})
	}
	e.BackupID = r.BackupID
	e.AddFiles(fileChanges(r.Files)) // files written even when the action then failed
	var cmds []tea.Cmd
	if r.Err != nil {
		ae := api.AsError(r.Err)
		e.Result = activity.Err(ae.Kind.String(), ae.Message)
		text := r.Err.Error()
		if ae.Hint != "" {
			text += ". " + ae.Hint
		}
		cmds = append(cmds, m.toasts.Push(module.ToastError, text))
	} else if !r.Silent {
		text := r.Message
		if text == "" {
			text = "Done."
		}
		cmds = append(cmds, m.toasts.Push(module.ToastSuccess, text))
	}
	if record {
		if err := m.opt.Activity.Log(e); err != nil {
			cmds = append(cmds, m.toasts.Push(module.ToastWarn, "Could not write activity log: "+err.Error()))
		}
	}
	if r.Refresh != 0 {
		m.fetching = false
		cmds = append(cmds, m.fetch(r.Refresh))
	}
	cmds = append(cmds, m.broadcast(r))
	return tea.Batch(cmds...)
}

// --- view -----------------------------------------------------------------

func (m *Model) View() string {
	if m.quitting {
		return ""
	}
	if m.w == 0 {
		return ""
	}
	if m.w < MinWidth || m.h < MinHeight {
		msg := fmt.Sprintf("%s needs at least %dx%d.\nThis terminal is %dx%d. Please resize it.",
			brand.Display, MinWidth, MinHeight, m.w, m.h)
		return lipgloss.Place(m.w, m.h, lipgloss.Center, lipgloss.Center, m.styles.Warn.Render(msg))
	}

	head := header.Render(m.styles, m.headerData(), m.w)
	cur := m.current()
	var hints []module.Hint
	if cur != nil {
		hints = cur.Hints()
	}
	hints = append(hints, m.globalHints()...)
	foot := statusbar.Render(m.styles, hints, m.footerRight(), m.w)

	bodyH := m.h - 2
	items := m.navItems()
	activeID := ""
	if cur != nil {
		activeID = cur.ID()
	}
	var body string
	if m.useSidebar() {
		side := sidebar.Render(m.styles, m.icons, items, activeID, sidebarWidth, bodyH)
		content := m.moduleView(m.w-sidebarWidth, bodyH)
		body = lipgloss.JoinHorizontal(lipgloss.Top, side, content)
	} else {
		bar := tabs.Render(m.styles, m.icons, items, activeID, m.w)
		body = lipgloss.JoinVertical(lipgloss.Left, bar, m.moduleView(m.w, bodyH-1))
	}
	screen := lipgloss.JoinVertical(lipgloss.Left, head, body, foot)

	switch {
	case m.confirm != nil:
		screen = modal.Center(m.styles, screen, m.confirm.View(min(72, m.w-4)), m.w, m.h)
	case m.form != nil:
		screen = modal.Center(m.styles, screen, m.form.View(min(72, m.w-4)), m.w, m.h)
	case m.help != nil:
		screen = modal.Center(m.styles, screen, m.styles.Modal.Render(m.help.View()), m.w, m.h)
	case m.palette != nil:
		screen = m.placeTop(screen, m.palette.View())
	case m.picker != nil:
		// Not dimmed: the screen behind is the live preview.
		box := m.picker.View()
		bw, bh := lipgloss.Width(box), lipgloss.Height(box)
		screen = modal.Place(screen, box, (m.w-bw)/2, (m.h-bh)/2, m.w, m.h)
	}
	if m.toasts.Len() > 0 {
		screen = toast.Overlay(screen, m.toasts.View(toast.MaxWidth), m.w, m.h-1)
	}
	return screen
}

func (m *Model) useSidebar() bool {
	switch m.opt.Sidebar {
	case "always":
		return m.showSide
	case "never":
		return false
	default:
		return m.showSide && m.w >= SidebarBreakpoint
	}
}

func (m *Model) moduleView(w, h int) string {
	cur := m.current()
	if cur == nil {
		return lipgloss.NewStyle().Width(w).Height(h).Render("")
	}
	if cur.Modes() == module.ModeHost && !m.HostMode() {
		msg := fmt.Sprintf("%s is available when %s runs on the headscale server (host mode).",
			cur.Title(), brand.Display)
		return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, m.styles.Muted.Render(msg))
	}
	if !m.connected && m.connErr != nil && cur.Modes() != module.ModeLocal {
		e := api.AsError(m.connErr)
		msg := m.styles.Danger.Render("Could not connect: "+e.Error()) + "\n\n" + m.styles.Muted.Render(e.Hint)
		return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, msg)
	}
	v := cur.View(m, module.Rect{W: w, H: h})
	return lipgloss.NewStyle().Width(w).Height(h).MaxWidth(w).MaxHeight(h).Render(v)
}

func (m *Model) navItems() []sidebar.Item {
	items := make([]sidebar.Item, 0, len(m.modules))
	for i, mod := range m.modules {
		k := ""
		if i < 9 {
			k = fmt.Sprint(i + 1)
		}
		items = append(items, sidebar.Item{
			ID:    mod.ID(),
			Title: mod.Title(),
			Icon:  mod.Icon(m.icons),
			Badge: mod.Badge(m.store),
			Dim:   mod.Modes() == module.ModeHost && !m.HostMode(),
			Key:   k,
		})
	}
	return items
}

func (m *Model) headerData() header.Header {
	c := m.store.Counts(m.opt.Now(), m.opt.ExpiryWarn)
	mode := "remote"
	if m.HostMode() {
		mode = "host"
	}
	return header.Header{
		App:       brand.Name,
		Profile:   m.opt.Profile,
		ModeLabel: mode,
		Version:   m.store.Info.Version,
		Healthy:   m.connected && m.store.Info.Healthy && !m.store.Stale,
		Online:    c.Online,
		Total:     c.Nodes,
		Stale:     m.store.Stale,
		Icons:     m.icons,
	}
}

func (m *Model) footerRight() string {
	parts := []string{}
	if m.opt.ReadOnly {
		parts = append(parts, "read-only")
	}
	if m.chord != "" {
		parts = append(parts, m.chord+" …")
	}
	if !m.store.UpdatedAt.IsZero() {
		parts = append(parts, "updated "+m.store.UpdatedAt.Format("15:04:05"))
	}
	out := m.styles.Muted.Render(strings.Join(parts, " · "))
	if b := m.serviceBadge(); b != "" {
		if len(parts) > 0 {
			out = b + m.styles.Muted.Render(" · ") + out
		} else {
			out = b
		}
	}
	return out
}

// serviceBadge is the headscale service state for the status bar in host
// mode (● running, ○ stopped, ◐ restarting); "" when unknown.
func (m *Model) serviceBadge() string {
	st := m.store.Service
	if !m.HostMode() || st == nil {
		return ""
	}
	p := badge.ServicePhaseOf(st.State, st.Running, "")
	if p == badge.ServiceUnknown {
		return ""
	}
	return badge.Service(m.styles, m.icons, p, p.String())
}

// --- quit guard -------------------------------------------------------------

const quitConfirmID = "app.quit"

// unsavedModules lists the titles of modules holding unsaved edits.
func (m *Model) unsavedModules() []string {
	var out []string
	for _, mod := range m.modules {
		if u, ok := mod.(module.Unsaved); ok && u.Unsaved() {
			out = append(out, mod.Title())
		}
	}
	return out
}

// quit exits at once, or asks first while any module has unsaved edits.
func (m *Model) quit() tea.Cmd {
	names := m.unsavedModules()
	if len(names) == 0 {
		m.quitting = true
		return tea.Quit
	}
	targets := make([]module.Target, len(names))
	for i, n := range names {
		targets[i] = module.Target{Kind: "module", ID: n, Name: n}
	}
	m.confirm = confirm.New(m.styles, m.icons, confirm.Request{
		ID:          quitConfirmID,
		Title:       "Quit with unsaved changes?",
		Danger:      module.DangerConfirm,
		Targets:     targets,
		Consequence: "Unsaved edits in " + strings.Join(names, ", ") + " are lost.",
		Confirm:     "Quit",
	})
	return m.confirm.Init()
}

// fileChanges converts an action's file writes for the activity log.
func fileChanges(ws []module.FileWrite) []activity.FileChange {
	out := make([]activity.FileChange, 0, len(ws))
	for _, w := range ws {
		out = append(out, activity.FileChange{Path: w.Path, BackupID: w.BackupID,
			DiffStats: activity.DiffStats{Added: w.Added, Removed: w.Removed}})
	}
	return out
}
