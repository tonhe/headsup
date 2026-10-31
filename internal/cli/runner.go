package cli

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tonhe/headsup/internal/activity"
	"github.com/tonhe/headsup/internal/api"
	"github.com/tonhe/headsup/internal/app"
	"github.com/tonhe/headsup/internal/config"
	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/icons"
	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/modules/registry"
	"github.com/tonhe/headsup/internal/store"
	"github.com/tonhe/headsup/internal/theme"
	"github.com/tonhe/headsup/internal/ui/form"
	"github.com/tonhe/headsup/internal/ui/styles"

	// Every built-in module registers itself; the CLI drives their Actions.
	_ "github.com/tonhe/headsup/internal/modules/all"
)

// The headless driver runs a module's Actions without a terminal. It builds
// a module.Context backed by the real connection, positions the module the
// way a user would (navigation arguments, key presses), fills the Action's
// parameter form from flags, applies the same gate and confirmation the TUI
// applies, then executes the returned tea.Cmd synchronously, feeding every
// intermediate message back into the module until the ActionResult arrives.

// maxMsgs bounds one pump so a module that keeps scheduling work cannot hang
// the CLI.
const maxMsgs = 10000

// modCtx is the module.Context handed to modules in CLI mode.
type modCtx struct {
	r  *runner
	c  *Conn
	st *store.Store
	s  *styles.Styles
	// toasts collects what a module would have shown in a toast.
	toasts []toastMsg
}

type toastMsg struct {
	level module.ToastLevel
	text  string
}

func (m *modCtx) Backend() api.Backend      { return m.c.Backend }
func (m *modCtx) Host() host.Host           { return m.c.Host }
func (m *modCtx) Store() *store.Store       { return m.st }
func (m *modCtx) Styles() *styles.Styles    { return m.s }
func (m *modCtx) Icons() icons.Set          { return icons.ASCII }
func (m *modCtx) ReadOnly() bool            { return m.c.ReadOnly }
func (m *modCtx) HostMode() bool            { return m.c.Host != nil }
func (m *modCtx) Profile() string           { return m.c.Label }
func (m *modCtx) ServerURL() string         { return m.c.ServerURL }
func (m *modCtx) Now() time.Time            { return m.r.now() }
func (m *modCtx) Refresh() tea.Cmd          { return nil }
func (m *modCtx) HostSettings() config.Host { return m.r.o.Config.Host }

// Navigate is a no-op: a CLI verb never leaves the module it drives.
func (m *modCtx) Navigate(string, any) tea.Cmd { return nil }

func (m *modCtx) Toast(l module.ToastLevel, msg string) tea.Cmd {
	return func() tea.Msg { return toastMsg{level: l, text: msg} }
}

// session is one module instance being driven.
type session struct {
	r   *runner
	ctx *modCtx
	mod module.Module
	// results are the ActionResults seen, in order.
	results []module.ActionResult
}

// session builds the module with id. init runs its Init commands (for
// modules that load files or lists on start); loadSlices are fetched into
// the store first.
func (r *runner) session(id string, init bool, loadSlices store.Slice) (*session, error) {
	c, err := r.connect()
	if err != nil {
		return nil, err
	}
	st, err := r.load(loadSlices)
	if err != nil {
		return nil, err
	}
	var mod module.Module
	for _, m := range registry.All(nil) {
		if m.ID() == id {
			mod = m
		}
	}
	if mod == nil {
		return nil, errorf(ExitError, "module %q is not built in", id)
	}
	t := theme.ByName(theme.Default)
	if t == nil {
		all := theme.All()
		t = &all[0]
	}
	s := &session{r: r, mod: mod, ctx: &modCtx{r: r, c: c, st: st, s: styles.New(*t)}}
	cmd := mod.Init(s.ctx)
	if init {
		s.pump(cmd)
	}
	s.send(store.UpdatedMsg{Slices: store.SliceAll | store.SlicePolicy})
	return s, nil
}

// send delivers one message to the module and runs what it returns.
func (s *session) send(msg tea.Msg) {
	n := 0
	s.deliver(msg, &n)
}

// pump runs cmd and everything it leads to.
func (s *session) pump(cmd tea.Cmd) {
	n := 0
	s.exec(cmd, &n)
}

func (s *session) exec(cmd tea.Cmd, n *int) {
	if cmd == nil || *n > maxMsgs {
		return
	}
	*n++
	s.deliver(cmd(), n)
}

var cmdType = reflect.TypeOf((*tea.Cmd)(nil)).Elem()

func (s *session) deliver(msg tea.Msg, n *int) {
	if msg == nil || *n > maxMsgs {
		return
	}
	switch m := msg.(type) {
	case module.ActionResult:
		s.results = append(s.results, m)
		return
	case toastMsg:
		s.ctx.toasts = append(s.ctx.toasts, m)
		return
	case tea.BatchMsg:
		for _, c := range m {
			s.exec(c, n)
		}
		return
	}
	// tea.Sequence's message type is unexported; it is a slice of commands.
	if v := reflect.ValueOf(msg); v.Kind() == reflect.Slice && v.Type().Elem() == cmdType {
		for i := 0; i < v.Len(); i++ {
			c, _ := v.Index(i).Interface().(tea.Cmd)
			s.exec(c, n)
		}
		return
	}
	mod, cmd := s.mod.Update(s.ctx, msg)
	if mod != nil {
		s.mod = mod
	}
	s.exec(cmd, n)
}

// key sends a key press, e.g. "tab" or "down".
func (s *session) key(k string) {
	s.send(keyMsg(k))
}

func keyMsg(k string) tea.KeyMsg {
	switch k {
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "home":
		return tea.KeyMsg{Type: tea.KeyHome}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
}

// action finds the Action with id in the module's current action list.
func (s *session) action(id string) (module.Action, bool) {
	for _, a := range s.mod.Actions() {
		if a.ID == id {
			return a, true
		}
	}
	return module.Action{}, false
}

// seek moves the cursor (home, then down) until the Action's first target
// has the wanted ID. It is how the CLI selects a row in modules that have no
// navigation argument for it.
func (s *session) seek(actionID, kind, want string) error {
	s.key("home")
	prev := "\x00"
	for i := 0; i < 100000; i++ {
		a, ok := s.action(actionID)
		if !ok || a.Targets == nil {
			break
		}
		tg := a.Targets(s.ctx)
		if len(tg) == 0 {
			break
		}
		if tg[0].ID == want {
			return nil
		}
		if tg[0].ID == prev {
			break // the cursor stopped moving: the last row
		}
		prev = tg[0].ID
		s.key("down")
	}
	return errorf(ExitNotFound, "%s %s not found", kind, want)
}

// errNoResult reports that a non-mutating Action only changed the module's
// state; callers driving a multi-step flow ignore it.
var errNoResult = &Error{Code: ExitError, Msg: "the action finished without a result"}

// call describes one Action invocation.
type call struct {
	id     string
	values module.Values // flag values overlaid on the form defaults
	yes    bool          // --yes
	// confirmed skips the prompt when an earlier step already asked.
	confirmed bool
}

// run gates, fills, confirms and executes an Action, returning its result.
func (s *session) run(c call) (module.ActionResult, error) {
	a, ok := s.action(c.id)
	if !ok {
		return module.ActionResult{}, errorf(ExitError, "action %s is not available", c.id)
	}
	env := app.GateEnv{ReadOnly: s.ctx.c.ReadOnly, Caps: s.ctx.c.Backend.Capabilities(), HostMode: s.ctx.HostMode()}
	if h := s.ctx.c.Host; h != nil {
		env.Perms = h.Env().Perms
	}
	if v := app.Gate(a, s.ctx, env); !v.Allowed {
		return module.ActionResult{}, gateError(a, env, v)
	}
	var targets []module.Target
	if a.Targets != nil {
		targets = a.Targets(s.ctx)
	}
	if a.Scope == module.ScopeSelection && len(targets) == 0 {
		return module.ActionResult{}, errorf(ExitNotFound, "nothing selected for %s", a.ID)
	}
	vals, err := fill(a, s.ctx, c.values)
	if err != nil {
		return module.ActionResult{}, err
	}
	if !c.confirmed {
		if err := s.r.confirm(app.NeedsConfirm(a), a.Title, a.Consequence, targets, c.yes); err != nil {
			return module.ActionResult{}, err
		}
	}
	start := s.r.now()
	before := len(s.results)
	s.pump(a.Run(s.ctx, vals))
	res, found := module.ActionResult{}, false
	for _, r := range s.results[before:] {
		if r.ActionID == a.ID {
			res, found = r, true
			break
		}
	}
	for _, t := range s.ctx.toasts {
		if t.level >= module.ToastWarn {
			s.r.logf("%s", t.text)
		}
	}
	s.ctx.toasts = nil
	if !found {
		if a.Mutating {
			return res, errorf(ExitError, "%s finished without a result", a.ID)
		}
		// A view action (switch tab, stage an edit) changes module state only.
		return res, errNoResult
	}
	if a.Mutating {
		s.r.record(a, targets, res, start)
	}
	if res.Refresh != 0 {
		if err := s.r.reload(res.Refresh); err == nil {
			s.send(store.UpdatedMsg{Slices: res.Refresh})
		}
	}
	return res, res.Err
}

func gateError(a module.Action, env app.GateEnv, v app.Verdict) error {
	switch {
	case a.Mutating && env.ReadOnly:
		return &Error{Code: ExitUnauthorized, Msg: "this profile is read-only; " + strings.ToLower(a.Title) + " is a change"}
	case a.Cap != api.CapNone && !env.Caps.Has(a.Cap):
		return &Error{Code: ExitUnsupported, Msg: v.Reason}
	case a.Needs != host.NeedNone && !env.HostMode:
		return &Error{Code: ExitNoHost, Msg: v.Reason, Fix: "run it on the headscale server; see `host info`"}
	case a.Needs != host.NeedNone:
		if _, missing := env.Perms.Missing(a.Needs); missing {
			return &Error{Code: ExitPermission, Msg: "permission denied: " + v.Reason, Fix: v.FixCommand}
		}
	}
	return &Error{Code: ExitError, Msg: v.Reason}
}

// fill starts from the Action's form defaults, overlays the flag values and
// validates, as submitting the form in the TUI would.
func fill(a module.Action, ctx module.Context, in module.Values) (module.Values, error) {
	out := module.Values{}
	if a.Params == nil {
		for k, v := range in {
			out[k] = v
		}
		return out, nil
	}
	spec := a.Params(ctx)
	if spec == nil {
		return out, nil
	}
	known := map[string]bool{}
	for _, f := range spec.Fields {
		known[f.ID] = true
		out[f.ID] = f.Value
	}
	var unknown []string
	for k, v := range in {
		if !known[k] {
			unknown = append(unknown, k)
			continue
		}
		out[k] = v
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, errorf(ExitError, "%s has no field %s", a.ID, strings.Join(unknown, ", "))
	}
	for _, f := range spec.Fields {
		v := out[f.ID]
		if f.Kind == module.FieldSelect && len(f.Options) > 0 && v != "" && !contains(f.Options, v) {
			return nil, errorf(ExitInvalid, "%s: %q is not one of %s", strings.ToLower(f.Label), v, strings.Join(f.Options, ", "))
		}
		if f.Required && strings.TrimSpace(v) == "" {
			return nil, errorf(ExitInvalid, "%s is required", strings.ToLower(f.Label))
		}
		if f.Kind == module.FieldDuration && v != "" {
			if _, err := form.ParseDuration(v); err != nil {
				return nil, errorf(ExitInvalid, "%s: %v", strings.ToLower(f.Label), err)
			}
		}
		if f.Validate != nil && v != "" {
			if err := f.Validate(v); err != nil {
				return nil, errorf(ExitInvalid, "%s: %v", strings.ToLower(f.Label), err)
			}
		}
	}
	return out, nil
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// record writes the activity-log entry the TUI writes for an action.
func (r *runner) record(a module.Action, targets []module.Target, res module.ActionResult, start time.Time) {
	label := "cli"
	if r.conn != nil && r.conn.Label != "" {
		label = r.conn.Label
	}
	e := activity.Entry{Time: r.now(), Profile: label, ActionID: a.ID, Result: activity.OK(),
		DurationMS: r.now().Sub(start).Milliseconds()}
	for _, t := range targets {
		e.Targets = append(e.Targets, activity.Target{Kind: t.Kind, ID: t.ID, Name: t.Name})
	}
	if res.Err != nil {
		ae := api.AsError(res.Err)
		e.Result = activity.Err(ae.Kind.String(), ae.Message)
	}
	if err := r.o.Activity.Log(e); err != nil {
		r.logf("could not write the activity log: %v", err)
	}
}

// runAction is the one-step convenience: open the module, optionally
// position it, run one Action, and print its message to stderr.
func (r *runner) runAction(moduleID string, init bool, sl store.Slice, pos func(*session) error, c call) (*session, module.ActionResult, error) {
	s, err := r.session(moduleID, init, sl)
	if err != nil {
		return nil, module.ActionResult{}, err
	}
	if pos != nil {
		if err := pos(s); err != nil {
			return s, module.ActionResult{}, err
		}
	}
	res, err := s.run(c)
	if err == nil && res.Message != "" && !res.Silent {
		r.logf("%s", res.Message)
	}
	return s, res, err
}

func selectID(id uint64) func(*session) error {
	return func(s *session) error {
		s.send(module.NavigateMsg{Args: module.SelectArgs{ID: id}})
		return nil
	}
}

func idStr(id uint64) string { return fmt.Sprint(id) }
