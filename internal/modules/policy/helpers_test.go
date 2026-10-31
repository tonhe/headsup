package policy

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tonhe/headsup/internal/api"
	"github.com/tonhe/headsup/internal/api/fake"
	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/icons"
	"github.com/tonhe/headsup/internal/module"
	pol "github.com/tonhe/headsup/internal/policy"
	"github.com/tonhe/headsup/internal/store"
	"github.com/tonhe/headsup/internal/ui/styles"
	"github.com/tonhe/headsup/internal/ui/uitest"
)

var testNow = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

// tctx is a module.Context for driving the module without the app.
type tctx struct {
	b        api.Backend
	h        host.Host
	st       *store.Store
	s        *styles.Styles
	readOnly bool
	toasts   []string
	navs     []module.FilterArgs
	results  []module.ActionResult
}

func (c *tctx) Backend() api.Backend   { return c.b }
func (c *tctx) Host() host.Host        { return c.h }
func (c *tctx) Store() *store.Store    { return c.st }
func (c *tctx) Styles() *styles.Styles { return c.s }
func (c *tctx) Icons() icons.Set       { return icons.Unicode }
func (c *tctx) ReadOnly() bool         { return c.readOnly }
func (c *tctx) HostMode() bool         { return c.h != nil }
func (c *tctx) ServerURL() string      { return "https://hs.example.com" }
func (c *tctx) Now() time.Time         { return testNow }
func (c *tctx) Refresh() tea.Cmd       { return nil }
func (c *tctx) Navigate(id string, args any) tea.Cmd {
	if fa, ok := args.(module.FilterArgs); ok {
		c.navs = append(c.navs, module.FilterArgs{Query: id + " " + fa.Query})
	}
	return nil
}
func (c *tctx) Toast(_ module.ToastLevel, msg string) tea.Cmd {
	c.toasts = append(c.toasts, msg)
	return nil
}

// harness wires a module to a context and runs commands synchronously.
type harness struct {
	t   *testing.T
	m   *Model
	ctx *tctx
}

func newHarness(t *testing.T, b api.Backend, h host.Host) *harness {
	t.Helper()
	m := New()
	m.hist = pol.NewHistory(filepath.Join(t.TempDir(), "history"))
	m.editor = func() []string { return []string{"true"} }
	hs := &harness{t: t, m: m, ctx: &tctx{b: b, h: h, st: store.New(), s: uitest.StyleFor("tokyo-night")}}
	hs.send(module.NavigateMsg{})
	if !m.loaded {
		t.Fatalf("policy did not load: %v", m.loadErr)
	}
	return hs
}

func (hs *harness) send(msg tea.Msg) {
	_, cmd := hs.m.Update(hs.ctx, msg)
	hs.run(cmd)
}

func (hs *harness) run(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-done:
	case <-time.After(100 * time.Millisecond):
		return // a timer (cursor blink): not needed headlessly
	}
	if t := reflect.TypeOf(msg); t != nil && strings.Contains(t.PkgPath(), "charmbracelet/bubbles") {
		return // cursor blinks and other widget timers
	}
	switch msg := msg.(type) {
	case nil:
	case tea.BatchMsg:
		for _, c := range msg {
			hs.run(c)
		}
	case module.ActionResult:
		hs.ctx.results = append(hs.ctx.results, msg)
		hs.send(msg)
	default:
		hs.send(msg)
	}
}

func (hs *harness) action(id string) module.Action {
	hs.t.Helper()
	for _, a := range hs.m.Actions() {
		if a.ID == id {
			return a
		}
	}
	hs.t.Fatalf("no action %s", id)
	return module.Action{}
}

// do runs an action the way the app would: gate, then Run (confirmation is
// the app's job and is assumed given).
func (hs *harness) do(id string) {
	hs.t.Helper()
	a := hs.action(id)
	if a.Mutating && hs.ctx.readOnly {
		hs.t.Fatalf("%s: read-only", id)
	}
	if a.Enabled != nil {
		if ok, why := a.Enabled(hs.ctx); !ok {
			hs.t.Fatalf("%s disabled: %s", id, why)
		}
	}
	hs.run(a.Run(hs.ctx, nil))
}

func (hs *harness) enabled(id string) (bool, string) {
	a := hs.action(id)
	if a.Enabled == nil {
		return true, ""
	}
	return a.Enabled(hs.ctx)
}

func (hs *harness) key(k string) {
	var msg tea.KeyMsg
	switch k {
	case "ctrl+s":
		msg = tea.KeyMsg{Type: tea.KeyCtrlS}
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "tab":
		msg = tea.KeyMsg{Type: tea.KeyTab}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
	}
	hs.send(msg)
}

// editTo opens the inline editor, replaces the buffer, and presses ctrl+s.
func (hs *harness) editTo(text string) {
	hs.t.Helper()
	hs.do("policy.edit")
	if !hs.m.Capturing() {
		hs.t.Fatal("the inline editor should capture keys")
	}
	hs.m.ta.SetValue(text)
	hs.key("ctrl+s")
}

func (hs *harness) lastResult() module.ActionResult {
	if len(hs.ctx.results) == 0 {
		return module.ActionResult{}
	}
	return hs.ctx.results[len(hs.ctx.results)-1]
}

func (hs *harness) view(w, h int) string {
	hs.t.Helper()
	out := hs.m.View(hs.ctx, module.Rect{W: w, H: h})
	lines := strings.Split(out, "\n")
	if len(lines) > h {
		hs.t.Errorf("view has %d lines, area is %d", len(lines), h)
	}
	for i, l := range lines {
		if lw := lipgloss.Width(l); lw > w {
			hs.t.Errorf("line %d is %d wide, area is %d: %q", i, lw, w, l)
		}
	}
	return out
}

func addAfter(t *testing.T, raw, anchor, ins string) string {
	t.Helper()
	i := strings.Index(raw, anchor)
	if i < 0 {
		t.Fatalf("anchor %q missing", anchor)
	}
	i += len(anchor)
	return raw[:i] + ins + raw[i:]
}

func countCalls(b *fake.Backend, method string) int {
	n := 0
	for _, c := range b.Calls() {
		if c.Method == method {
			n++
		}
	}
	return n
}
