package app

import (
	"errors"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tonhe/headsup/internal/activity"
	"github.com/tonhe/headsup/internal/api/fake"
	"github.com/tonhe/headsup/internal/config"
	"github.com/tonhe/headsup/internal/icons"
	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/modules/dashboard"
	"github.com/tonhe/headsup/internal/store"
	"github.com/tonhe/headsup/internal/watch"
)

// memLog is an in-memory activity.Logger.
type memLog struct {
	mu sync.Mutex
	es []activity.Entry
}

func (l *memLog) Log(e activity.Entry) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.es = append(l.es, activity.Redact(e))
	return nil
}
func (l *memLog) Recent(int) ([]activity.Entry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]activity.Entry, 0, len(l.es))
	for i := len(l.es) - 1; i >= 0; i-- {
		out = append(out, l.es[i])
	}
	return out, nil
}

// fakeNotifier records notifications instead of showing them.
type fakeNotifier struct {
	mu      sync.Mutex
	desktop []string
	bells   int
}

func (n *fakeNotifier) Desktop(title, body string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.desktop = append(n.desktop, title+": "+body)
	return nil
}
func (n *fakeNotifier) Bell() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.bells++
	return nil
}

// probe is a configurable test module.
type probe struct {
	unsaved bool
	actions []module.Action
	events  []watch.Event
}

func (*probe) ID() string                  { return "probe" }
func (*probe) Title() string               { return "Probe" }
func (*probe) Icon(icons.Set) string       { return "" }
func (*probe) Order() int                  { return 1 }
func (*probe) Modes() module.Modes         { return module.ModeLocal }
func (*probe) Badge(*store.Store) string   { return "" }
func (*probe) Init(module.Context) tea.Cmd { return nil }
func (p *probe) Update(_ module.Context, msg tea.Msg) (module.Module, tea.Cmd) {
	if ev, ok := msg.(watch.EventsMsg); ok {
		p.events = append(p.events, ev.Events...)
	}
	return p, nil
}
func (*probe) View(module.Context, module.Rect) string { return "probe view" }
func (p *probe) Actions() []module.Action              { return p.actions }
func (*probe) Capturing() bool                         { return false }
func (*probe) Hints() []module.Hint                    { return nil }
func (p *probe) Unsaved() bool                         { return p.unsaved }

func TestQuitGuard(t *testing.T) {
	p := &probe{unsaved: true}
	d := newDriver(t, 120, 40, Options{Modules: []module.Module{p}})
	d.key("q")
	d.Want("Quit with unsaved changes?", "Probe")
	if d.m.quitting {
		t.Fatal("must ask before quitting with unsaved edits")
	}
	d.key("n")
	if d.m.quitting || d.m.confirm != nil {
		t.Fatal("declining keeps the app running")
	}
	d.send(tea.KeyMsg{Type: tea.KeyCtrlC})
	d.Want("Quit with unsaved changes?")
	d.key("y")
	if !d.m.quitting {
		t.Fatal("confirming quits")
	}

	p2 := &probe{}
	d2 := newDriver(t, 120, 40, Options{Modules: []module.Module{p2}})
	d2.key("q")
	if !d2.m.quitting {
		t.Fatal("no unsaved edits quits at once")
	}
}

func TestConfirmTextAndFileActivity(t *testing.T) {
	log := &memLog{}
	p := &probe{actions: []module.Action{{
		ID: "probe.write", Title: "Write file", Keys: []string{"w"}, Mutating: true, Danger: module.DangerCritical,
		Targets: func(module.Context) []module.Target {
			return []module.Target{{Kind: "file", ID: "/etc/x.yaml", Name: "x.yaml"}}
		},
		ConfirmText: func(module.Context) string { return "/etc/x.yaml" },
		Run: func(module.Context, module.Values) tea.Cmd {
			return func() tea.Msg {
				return module.ActionResult{ActionID: "probe.write", BackupID: "", Files: []module.FileWrite{
					{Path: "/etc/x.yaml", BackupID: "x.yaml.bak1", Added: 3, Removed: 1},
				}}
			}
		},
	}}}
	d := newDriver(t, 120, 40, Options{Modules: []module.Module{p}, Activity: log, Profile: "homelab"})
	d.key("w")
	d.Want("/etc/x.yaml")
	d.Type("x.yaml")
	d.key("enter")
	if len(log.es) != 0 {
		t.Fatal("the target name must not satisfy a custom confirm text")
	}
	d.key("ctrl+u")
	d.Type("/etc/x.yaml")
	d.key("enter")
	if len(log.es) != 1 {
		d.key("esc")
		t.Fatalf("entries = %d", len(log.es))
	}
	e := log.es[0]
	if e.BackupID != "x.yaml.bak1" || e.DiffStats == nil || e.DiffStats.Added != 3 || len(e.Files) != 1 || e.Profile != "homelab" {
		t.Errorf("entry = %+v", e)
	}
}

func TestServiceBadgeInStatusBar(t *testing.T) {
	b := fake.Demo()
	h := demoHostFor(t, b)
	d := newDriver(t, 120, 40, Options{Backend: b, Host: h, Profile: "host:demo"})
	d.Want("● running")
	if d.m.Profile() != "host:demo" {
		t.Error("Profile() should be the active profile")
	}
	d2 := newDriver(t, 120, 40, Options{})
	d2.WantNot("● running")
}

func TestHostSettings(t *testing.T) {
	c := config.Default()
	c.Host.BackupDir = "/srv/bk"
	m := New(Options{Backend: fake.Demo(), Config: &c})
	if m.HostSettings().BackupDir != "/srv/bk" || m.Activity() == nil {
		t.Errorf("host settings = %+v", m.HostSettings())
	}
}

func TestWatchEventsFlow(t *testing.T) {
	c := config.Default()
	c.Notify.Desktop, c.Notify.Bell = true, true
	c.Notify.Events = []string{"node_offline"}
	n := &fakeNotifier{}
	p := &probe{}
	d := newDriver(t, 120, 40, Options{Modules: []module.Module{p, dashboard.New()}, Config: &c, Notifier: n})
	if len(d.m.store.Nodes) == 0 {
		t.Fatal("demo store not loaded")
	}
	var on, off string
	sn := store.Snapshot{Slices: store.SliceNodes, Errs: map[store.Slice]error{}}
	for _, nd := range d.m.store.Nodes {
		switch {
		case nd.Online && off == "":
			nd.Online, off = false, nd.DisplayName()
		case !nd.Online && on == "":
			nd.Online, on = true, nd.DisplayName()
		}
		sn.Nodes = append(sn.Nodes, nd)
	}
	d.send(snapshotMsg{sn})
	var kinds []watch.Kind
	for _, e := range p.events {
		kinds = append(kinds, e.Kind)
	}
	if !reflect.DeepEqual(kinds, []watch.Kind{watch.NodeOnline, watch.NodeOffline}) &&
		!reflect.DeepEqual(kinds, []watch.Kind{watch.NodeOffline, watch.NodeOnline}) {
		t.Fatalf("events = %v", p.events)
	}
	d.Want(off + " went offline")
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.bells != 1 || len(n.desktop) != 1 || !strings.Contains(n.desktop[0], off+" went offline") || strings.Contains(n.desktop[0], on) {
		t.Errorf("notifications: bells=%d desktop=%q", n.bells, n.desktop)
	}
}

func TestEventSummary(t *testing.T) {
	var evs []watch.Event
	for _, s := range []string{"a", "b", "c", "d", "e"} {
		evs = append(evs, watch.Event{Kind: watch.NodeOffline, Subject: s})
	}
	if got := eventSummary(evs); got != "a went offline; b went offline; c went offline; and 2 more (activity page)" {
		t.Error(got)
	}
	m := New(Options{Backend: fake.Demo()})
	if m.onEvents(nil) != nil {
		t.Error("no events, no command")
	}
}

type fakeLook map[string]bool

func (f fakeLook) look(name string) (string, error) {
	if f[name] {
		return "/usr/bin/" + name, nil
	}
	return "", errors.New("not found")
}

func TestDesktopCommand(t *testing.T) {
	name, args, err := desktopCommand("linux", "T", `say "hi"`, fakeLook{"notify-send": true}.look)
	if err != nil || name != "notify-send" || args[len(args)-1] != `say "hi"` || args[len(args)-3] != "--" {
		t.Errorf("linux: %s %q %v", name, args, err)
	}
	if _, _, err := desktopCommand("freebsd", "T", "b", fakeLook{}.look); err == nil {
		t.Error("missing notify-send should fail")
	}
	name, args, _ = desktopCommand("darwin", `a"b`, `c\d`, nil)
	if name != "osascript" || args[1] != `display notification "c\\d" with title "a\"b"` {
		t.Errorf("darwin: %q", args)
	}
	name, args, _ = desktopCommand("windows", "it's", "b", nil)
	if name != "powershell" || !strings.Contains(args[len(args)-1], "'it''s'") {
		t.Errorf("windows: %q", args)
	}

	var got []string
	sn := SystemNotifier{run: func(name string, args ...string) error { got = append([]string{name}, args...); return nil }}
	if err := sn.Desktop("T", "B"); err != nil && !errors.Is(err, errNoNotifier) {
		t.Fatal(err)
	}
	var buf strings.Builder
	if err := (SystemNotifier{Out: &buf}).Bell(); err != nil || buf.String() != "\a" {
		t.Errorf("bell wrote %q", buf.String())
	}
	if runtime.GOOS == "linux" && len(got) > 0 && got[0] != "notify-send" {
		t.Errorf("ran %q", got)
	}
}
