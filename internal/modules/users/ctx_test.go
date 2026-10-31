package users

import (
	"context"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tonhe/headsup/internal/api"
	"github.com/tonhe/headsup/internal/api/fake"
	"github.com/tonhe/headsup/internal/host"
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

// testCtx is a minimal module.Context over the fake backend.
type testCtx struct {
	b    *fake.Backend
	st   *store.Store
	s    *styles.Styles
	ic   icons.Set
	navs []nav
}

func newCtx(t *testing.T) *testCtx {
	t.Helper()
	c := &testCtx{
		b:  fake.Demo(fake.WithClock(func() time.Time { return testNow })),
		st: store.New(),
		s:  uitest.StyleFor("tokyo-night"),
		ic: icons.Unicode,
	}
	c.reload()
	return c
}

func (c *testCtx) reload() {
	c.st.Apply(store.Fetch(context.Background(), c.b, nil, store.SliceAll))
}

func (c *testCtx) Backend() api.Backend   { return c.b }
func (c *testCtx) Host() host.Host        { return nil }
func (c *testCtx) Store() *store.Store    { return c.st }
func (c *testCtx) Styles() *styles.Styles { return c.s }
func (c *testCtx) Icons() icons.Set       { return c.ic }
func (c *testCtx) ReadOnly() bool         { return false }
func (c *testCtx) HostMode() bool         { return false }
func (c *testCtx) ServerURL() string      { return "https://hs.example.com" }
func (c *testCtx) Now() time.Time         { return testNow }
func (c *testCtx) Refresh() tea.Cmd       { return nil }
func (c *testCtx) Navigate(id string, args any) tea.Cmd {
	c.navs = append(c.navs, nav{id, args})
	return nil
}
func (c *testCtx) Toast(module.ToastLevel, string) tea.Cmd { return nil }

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

// run executes an action's command and returns its ActionResult.
func run(t *testing.T, c *testCtx, a module.Action, v module.Values) module.ActionResult {
	t.Helper()
	cmd := a.Run(c, v)
	if cmd == nil {
		return module.ActionResult{}
	}
	res, ok := cmd().(module.ActionResult)
	if !ok {
		t.Fatalf("action %s did not finish with an ActionResult", a.ID)
	}
	return res
}

func keyMsg(k string) tea.KeyMsg {
	switch k {
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
}
