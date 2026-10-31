package server

import (
	"context"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tonhe/headsup/internal/api"
	apifake "github.com/tonhe/headsup/internal/api/fake"
	"github.com/tonhe/headsup/internal/config"
	"github.com/tonhe/headsup/internal/host"
	hostfake "github.com/tonhe/headsup/internal/host/fake"
	"github.com/tonhe/headsup/internal/icons"
	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/store"
	"github.com/tonhe/headsup/internal/ui/styles"
	"github.com/tonhe/headsup/internal/ui/uitest"
)

var testNow = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

type nav struct {
	id   string
	args any
}

// testCtx is a minimal module.Context over the fake backend and host.
type testCtx struct {
	b    *apifake.Backend
	h    *hostfake.Host // nil = remote mode
	st   *store.Store
	s    *styles.Styles
	ic   icons.Set
	navs []nav
	cfg  *config.Host
}

func newCtx(t *testing.T, version string, hostMode bool) *testCtx {
	t.Helper()
	clock := func() time.Time { return testNow }
	opts := []apifake.Option{apifake.WithClock(clock)}
	if version != "" {
		opts = append(opts, apifake.WithVersion(version))
	}
	c := &testCtx{b: apifake.Demo(opts...), st: store.New(), s: uitest.StyleFor("tokyo-night"), ic: icons.Unicode}
	if hostMode {
		c.h = hostfake.New(hostfake.WithClock(clock), hostfake.WithLogInterval(time.Hour))
	}
	info, err := c.b.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	c.st.Info = info
	return c
}

func (c *testCtx) Backend() api.Backend { return c.b }
func (c *testCtx) Host() host.Host {
	if c.h == nil {
		return nil
	}
	return c.h
}
func (c *testCtx) Store() *store.Store    { return c.st }
func (c *testCtx) Styles() *styles.Styles { return c.s }
func (c *testCtx) Icons() icons.Set       { return c.ic }
func (c *testCtx) ReadOnly() bool         { return false }
func (c *testCtx) HostMode() bool         { return c.h != nil }
func (c *testCtx) ServerURL() string      { return "https://hs.example.com" }
func (c *testCtx) Now() time.Time         { return testNow }
func (c *testCtx) Refresh() tea.Cmd       { return nil }
func (c *testCtx) Navigate(id string, args any) tea.Cmd {
	c.navs = append(c.navs, nav{id, args})
	return nil
}
func (c *testCtx) Toast(module.ToastLevel, string) tea.Cmd { return nil }

// settingsCtx adds the optional Settings interface.
type settingsCtx struct {
	*testCtx
}

func (c settingsCtx) HostSettings() config.Host { return *c.cfg }

func action(t *testing.T, m module.Module, id string) module.Action {
	t.Helper()
	for _, a := range m.Actions() {
		if a.ID == id {
			return a
		}
	}
	t.Fatalf("no action %q", id)
	return module.Action{}
}

// drain runs cmd and feeds every resulting message back into m until an
// ActionResult (returned) or nothing is left.
func drain(t *testing.T, ctx module.Context, m *Model, cmd tea.Cmd) []module.ActionResult {
	t.Helper()
	var out []module.ActionResult
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		switch msg := c().(type) {
		case nil:
		case tea.BatchMsg:
			queue = append(queue, msg...)
		case module.ActionResult:
			out = append(out, msg)
			_, next := m.Update(ctx, msg)
			queue = append(queue, next)
		default:
			_, next := m.Update(ctx, msg)
			queue = append(queue, next)
		}
	}
	return out
}

func keyMsg(k string) tea.KeyMsg {
	switch k {
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
}
