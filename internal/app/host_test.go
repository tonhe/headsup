package app

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tonhe/fathom/internal/api"
	"github.com/tonhe/fathom/internal/api/fake"
	"github.com/tonhe/fathom/internal/host"
	hostfake "github.com/tonhe/fathom/internal/host/fake"
	"github.com/tonhe/fathom/internal/icons"
	"github.com/tonhe/fathom/internal/module"
	"github.com/tonhe/fathom/internal/store"
)

func demoHostFor(t *testing.T, b api.Backend, opts ...hostfake.Option) *hostfake.Host {
	t.Helper()
	nodes, err := b.ListNodes(context.Background(), api.NodeFilter{})
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]uint64, len(nodes))
	for i, n := range nodes {
		ids[i] = n.ID
	}
	return hostfake.New(append([]hostfake.Option{hostfake.WithNodes(ids)}, opts...)...)
}

func TestHostModeEnrichesMachines(t *testing.T) {
	b := fake.Demo()
	h := demoHostFor(t, b)
	d := newDriver(t, 160, 50, Options{Backend: b, Host: h, Profile: "host:hs-demo"})
	d.key("2", "enter") // close the detail pane so the list has room for host columns
	out := d.view()
	for _, want := range []string{"host", "OS", "CLIENT", "DERP"} {
		if !strings.Contains(out, want) {
			t.Errorf("host-mode machines view missing %q", want)
		}
	}
	if len(d.m.store.Extras) == 0 {
		t.Error("store should hold NodeExtras in host mode")
	}
}

func TestRemoteModeHidesHostColumns(t *testing.T) {
	d := newDriver(t, 160, 50, Options{})
	d.key("2")
	if strings.Contains(d.view(), "CLIENT") {
		t.Error("remote mode must not show host-only columns")
	}
}

func TestHostActionGatedWithFix(t *testing.T) {
	b := fake.Demo()
	h := demoHostFor(t, b)
	h.FailPermission(host.NeedDebug, "sudo usermod -aG headscale $USER")
	d := newDriver(t, 160, 50, Options{Backend: b, Host: h})
	d.key("2", "p")
	if out := d.view(); !strings.Contains(out, "usermod") {
		t.Errorf("ping without debug access should show the fix command:\n%s", out)
	}
}

func TestPingInRemoteModeExplainsHostMode(t *testing.T) {
	d := newDriver(t, 160, 50, Options{})
	d.key("2", "p")
	if out := d.view(); !strings.Contains(out, "host mode") {
		t.Errorf("host-only action in remote mode should mention host mode:\n%s", out)
	}
}

// hostOnly is a minimal host-only module for the explainer test.
type hostOnly struct{}

func (hostOnly) ID() string                                                { return "svc" }
func (hostOnly) Title() string                                             { return "Service" }
func (hostOnly) Icon(icons.Set) string                                     { return "" }
func (hostOnly) Order() int                                                { return 1 }
func (hostOnly) Modes() module.Modes                                       { return module.ModeHost }
func (hostOnly) Badge(*store.Store) string                                 { return "" }
func (hostOnly) Init(module.Context) tea.Cmd                               { return nil }
func (h hostOnly) Update(module.Context, tea.Msg) (module.Module, tea.Cmd) { return h, nil }
func (hostOnly) View(module.Context, module.Rect) string                   { return "SECRET HOST VIEW" }
func (hostOnly) Actions() []module.Action                                  { return nil }
func (hostOnly) Capturing() bool                                           { return false }
func (hostOnly) Hints() []module.Hint                                      { return nil }

func TestHostModuleExplainerInRemoteMode(t *testing.T) {
	d := newDriver(t, 120, 40, Options{Modules: []module.Module{hostOnly{}}})
	out := d.view()
	if strings.Contains(out, "SECRET HOST VIEW") || !strings.Contains(out, "host mode") {
		t.Errorf("host-only module should show its explainer in remote mode:\n%s", out)
	}
	d2 := newDriver(t, 120, 40, Options{Modules: []module.Module{hostOnly{}}, Host: hostfake.New()})
	if !strings.Contains(d2.view(), "SECRET HOST VIEW") {
		t.Error("host-only module should render in host mode")
	}
}
