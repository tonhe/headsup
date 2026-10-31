package service

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tonhe/headsup/internal/api"
	apifake "github.com/tonhe/headsup/internal/api/fake"
	"github.com/tonhe/headsup/internal/app"
	"github.com/tonhe/headsup/internal/host"
	hostfake "github.com/tonhe/headsup/internal/host/fake"
	"github.com/tonhe/headsup/internal/host/logs"
	"github.com/tonhe/headsup/internal/icons"
	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/store"
	"github.com/tonhe/headsup/internal/ui/uitest"
)

var flowNow = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

// machinesStub stands in for the machines module so the test can see the
// Navigate a log line's machine link sends, without importing that module.
type machinesStub struct{ got []any }

func (*machinesStub) ID() string                  { return "machines" }
func (*machinesStub) Title() string               { return "Machines" }
func (*machinesStub) Icon(icons.Set) string       { return "" }
func (*machinesStub) Order() int                  { return 20 }
func (*machinesStub) Modes() module.Modes         { return module.ModeAPI }
func (*machinesStub) Badge(*store.Store) string   { return "" }
func (*machinesStub) Init(module.Context) tea.Cmd { return nil }
func (s *machinesStub) Update(_ module.Context, msg tea.Msg) (module.Module, tea.Cmd) {
	if n, ok := msg.(module.NavigateMsg); ok {
		s.got = append(s.got, n.Args)
	}
	return s, nil
}
func (*machinesStub) View(module.Context, module.Rect) string { return "MACHINES STUB" }
func (*machinesStub) Actions() []module.Action                { return nil }
func (*machinesStub) Capturing() bool                         { return false }
func (*machinesStub) Hints() []module.Hint                    { return nil }

// driver runs the app headlessly on the shared flow-test driver (fake clock,
// no dropped commands); see uitest.Driver.
type driver struct {
	*uitest.Driver
	t    *testing.T
	m    *app.Model
	svc  *Model
	mach *machinesStub
	h    *hostfake.Host
}

func newDriver(t *testing.T, w, ht int, h *hostfake.Host, iconSet string) *driver {
	t.Helper()
	clock := func() time.Time { return flowNow }
	b := apifake.Demo(apifake.WithClock(clock))
	d := &driver{t: t, svc: New(), mach: &machinesStub{}, h: h}
	opt := app.Options{Backend: b, Modules: []module.Module{d.svc, d.mach}, Now: clock,
		Refresh: time.Hour, Theme: "tokyo-night", Icons: iconSet, Profile: "host:hs-demo"}
	if h != nil {
		opt.Host = h
	}
	d.m = app.New(opt)
	d.Driver = uitest.NewDriver(t, d.m, w, ht)
	d.OnClose(func() {
		if d.svc.cancel != nil {
			d.svc.cancel()
		}
	})
	return d
}

func newHost(t *testing.T, opts ...hostfake.Option) *hostfake.Host {
	t.Helper()
	nodes, err := apifake.Demo().ListNodes(context.Background(), api.NodeFilter{})
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]uint64, len(nodes))
	for i, n := range nodes {
		ids[i] = n.ID
	}
	base := []hostfake.Option{hostfake.WithNodes(ids), hostfake.WithClock(func() time.Time { return flowNow }),
		hostfake.WithLogInterval(time.Hour)}
	return hostfake.New(append(base, opts...)...)
}

func (d *driver) send(msg tea.Msg)   { d.Send(msg) }
func (d *driver) key(keys ...string) { d.Key(keys...) }
func (d *driver) view() string       { return d.Screen() }

func (d *driver) state() host.ServiceStatus {
	st, err := d.h.Service().Status(context.Background())
	if err != nil {
		d.t.Fatal(err)
	}
	return st
}

// push feeds lines into the current log stream as if the source sent them.
func (d *driver) push(lines ...string) {
	es := make([]host.LogEntry, len(lines))
	for i, l := range lines {
		es[i] = logs.ParseLine(l)
	}
	d.send(logMsg{id: d.svc.streamID, entries: es})
}

func (d *driver) want(sub string)    { d.t.Helper(); d.Want(sub) }
func (d *driver) wantNot(sub string) { d.t.Helper(); d.WantNot(sub) }

func checkFrame(t *testing.T, out string, w, h int) {
	t.Helper()
	lines := strings.Split(out, "\n")
	if len(lines) > h {
		t.Errorf("frame has %d lines, terminal is %d", len(lines), h)
	}
	for i, l := range lines {
		if lw := lipgloss.Width(l); lw > w {
			t.Errorf("line %d is %d wide, terminal is %d: %q", i, lw, w, l)
		}
	}
}

func TestFlowFollowPauseResume(t *testing.T) {
	d := newDriver(t, 140, 40, newHost(t), "unicode")
	if d.svc.logs.Total() != 200 {
		t.Fatalf("initial tail: got %d lines, want 200", d.svc.logs.Total())
	}
	d.want("following")
	d.want("● active")
	d.want("PID 812")

	// Following: a new line shows up at the bottom.
	d.push("2026-10-28T12:00:01Z INF Fresh line one node.id=1")
	d.want("Fresh line one")

	// Pause: lines are held back, not shown, and counted.
	d.key("space")
	d.want("paused")
	d.push("2026-10-28T12:00:02Z WRN Held line node.id=2")
	d.wantNot("Held line")
	d.want("paused • 1 new")
	if d.svc.logs.Total() != 201 {
		t.Errorf("held lines must not enter the ring yet: %d", d.svc.logs.Total())
	}

	// Resume: the held line appears and following continues.
	d.key("space")
	d.want("Held line")
	d.want("following")
	d.wantNot("paused")

	// Scrolling up releases follow; new lines then stay off screen.
	for range 60 {
		d.key("k")
	}
	d.wantNot("following")
	d.push("2026-10-28T12:00:03Z INF Offscreen line")
	d.wantNot("Offscreen line")
	d.key("f")
	d.want("following")
	d.want("Offscreen line")
}

func TestFlowLevelFilterAndSearch(t *testing.T) {
	d := newDriver(t, 140, 40, newHost(t), "unicode")
	d.key("l", "l", "l", "l") // TRC -> DBG -> INF -> WRN -> ERR
	d.want("level >= ERR")
	if d.svc.logs.Len() == 0 {
		t.Fatal("expected some error lines")
	}
	es, _ := d.svc.logs.Window(1000)
	for _, e := range es {
		if e.Level < host.LevelError {
			t.Fatalf("level filter let through %v: %s", e.Level, e.Msg)
		}
	}
	errs := d.svc.logs.Len()
	d.key("l") // wrap back to everything
	d.wantNot("level >=")
	if d.svc.logs.Len() != 200 {
		t.Fatalf("filter off: %d lines", d.svc.logs.Len())
	}

	// Regex search, live while typing.
	d.key("/", "broken\\s+pipe")
	if !d.svc.Capturing() {
		t.Fatal("search input should capture keys")
	}
	d.key("enter")
	if d.svc.logs.Len() != errs {
		t.Errorf("search for broken pipe: %d lines, want %d", d.svc.logs.Len(), errs)
	}
	d.want("search: broken\\s+pipe")

	// An invalid regex is matched literally, with a note.
	d.key("/", "(")
	d.want("not a regex")
	d.key("esc")
	d.wantNot("search:")
	if d.svc.logs.Len() != 200 {
		t.Errorf("esc should clear the search: %d", d.svc.logs.Len())
	}
}

func TestFlowJumpToErrorExpandAndMachineLink(t *testing.T) {
	d := newDriver(t, 140, 40, newHost(t), "unicode")
	d.key("n")
	e, ok := d.svc.logs.Cursor()
	if !ok || e.Level < host.LevelError {
		t.Fatalf("n should land on an error line, got %+v", e)
	}
	d.wantNot("following")
	d.key("enter")
	d.want("write: broken pipe")
	d.want("m to open")
	d.key("m")
	if len(d.mach.got) != 1 {
		t.Fatalf("machines got %d navigations", len(d.mach.got))
	}
	sel, ok := d.mach.got[0].(module.SelectArgs)
	if !ok || sel.ID != e.NodeRefs[0] {
		t.Errorf("navigate args %#v, want node %d", d.mach.got[0], e.NodeRefs[0])
	}
	d.want("MACHINES STUB")
}

func TestFlowRestartRefusedOnConfigtestFailure(t *testing.T) {
	h := newHost(t)
	d := newDriver(t, 140, 40, h, "unicode")
	h.SetConfigtestFailure(host.Issue{Severity: host.SevError, Message: "server_url must start with https://", Line: 3})
	before := d.state()
	d.key("X")
	d.want("Connected clients will reconnect")
	d.key("y")
	d.want("restart refused")
	d.want("line 3: server_url must start with https://")
	after := d.state()
	if after.PID != before.PID || after.State != "active" {
		t.Fatalf("service was touched: before %+v after %+v", before, after)
	}

	// A passing check lets the restart through, and the health check reports the new PID.
	h.ClearFailures()
	d.key("X", "y")
	if got := d.state(); got.PID == before.PID {
		t.Fatalf("restart did not happen: %+v", got)
	}
	d.want("headscale restarted")
	if d.svc.refused != nil {
		t.Errorf("a passing restart should clear the refusal: %v", d.svc.refused)
	}
}

func TestFlowStartRefusedOnConfigtestFailure(t *testing.T) {
	h := newHost(t)
	if err := h.Service().Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	d := newDriver(t, 140, 40, h, "unicode")
	d.want("○ inactive")
	if got := d.svc.Badge(nil); got != "○" {
		t.Errorf("stopped badge %q", got)
	}
	h.SetConfigtestFailure(host.Issue{Severity: host.SevError, Message: "noise.private_key_path is required"})
	d.key("S", "y")
	d.want("start refused")
	if st := d.state(); st.State != "inactive" {
		t.Fatalf("start must not run after a failed check: %+v", st)
	}
	h.ClearFailures()
	d.key("S", "y")
	if st := d.state(); !st.Running {
		t.Fatalf("start after a passing check: %+v", st)
	}
	d.want("● active")
	if got := d.svc.Badge(nil); got != "●" {
		t.Errorf("running badge %q", got)
	}
}

func TestFlowStopRequiresTypedHostname(t *testing.T) {
	h := newHost(t)
	d := newDriver(t, 140, 40, h, "unicode")
	d.key("x")
	d.want("Every client loses the control plane")
	d.want("hs-demo")
	d.key("wrong-host", "enter")
	if !d.state().Running {
		t.Fatal("a wrong hostname must not stop the service")
	}
	d.key("esc")
	if !d.state().Running {
		t.Fatal("cancel must not stop the service")
	}
	d.key("x", "hs-demo", "enter")
	if st := d.state(); st.Running {
		t.Fatalf("typing the hostname should stop the service: %+v", st)
	}
	d.want("○ inactive")
	d.want("headscale stopped")

	// Stop and reload are refused while stopped, with a reason.
	d.key("H")
	d.want("not running")
}

func TestFlowReloadAndUnhealthyRestart(t *testing.T) {
	h := newHost(t)
	d := newDriver(t, 140, 40, h, "unicode")
	d.key("H")
	d.want("reloads the policy only")
	d.key("y")
	d.want("Policy reloaded")

	h.SetUnhealthyRestart(true)
	d.key("X", "y")
	d.want("not healthy after restart")
	if got := d.svc.Badge(nil); got != "○" {
		t.Errorf("failed badge %q", got)
	}
}

func TestFlowPermissionErrors(t *testing.T) {
	h := newHost(t)
	h.FailPermission(host.NeedLogs, "sudo usermod -aG systemd-journal $USER")
	h.FailPermission(host.NeedService, "sudo systemctl restart headscale")
	d := newDriver(t, 140, 40, h, "unicode")
	d.want("Cannot read headscale's logs")
	d.want("sudo usermod -aG systemd-journal $USER")
	d.key("X")
	d.want("Fix: sudo")

	// Retry once the user fixed it.
	h.ClearFailures()
	d.key("esc", "f")
	if d.svc.logs.Total() == 0 {
		t.Fatal("f should reopen the log stream")
	}
	d.wantNot("Cannot read")
}

func TestFlowBusyBlocksSecondAction(t *testing.T) {
	d := newDriver(t, 140, 40, newHost(t), "unicode")
	d.svc.busy = "restarting"
	if got := d.svc.Badge(nil); got != "◐" {
		t.Errorf("busy badge %q", got)
	}
	d.key("X")
	d.want("headscale is restarting; wait")
	d.want("restarting…")
}

func TestScreensFit(t *testing.T) {
	for _, sz := range [][2]int{{80, 24}, {120, 40}, {160, 50}} {
		d := newDriver(t, sz[0], sz[1], newHost(t), "unicode")
		checkFrame(t, d.view(), sz[0], sz[1])
		d.key("n", "enter", "/", "node")
		checkFrame(t, d.view(), sz[0], sz[1])
	}
}

func TestGoldenService(t *testing.T) {
	for _, sz := range [][2]int{{80, 24}, {120, 40}} {
		for _, ic := range []string{"unicode", "ascii"} {
			t.Run(fmt.Sprintf("%dx%d/%s", sz[0], sz[1], ic), func(t *testing.T) {
				d := newDriver(t, sz[0], sz[1], newHost(t), ic)
				uitest.Golden(t, d.View())
			})
		}
	}
	t.Run("expanded", func(t *testing.T) {
		d := newDriver(t, 120, 40, newHost(t), "unicode")
		d.key("n", "enter")
		uitest.Golden(t, d.View())
	})
}

func TestRemoteModeExplains(t *testing.T) {
	d := newDriver(t, 120, 40, nil, "unicode")
	d.want("host mode")
	if d.svc.Badge(nil) != "" {
		t.Error("no badge without a host")
	}
}
