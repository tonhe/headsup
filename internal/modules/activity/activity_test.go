package activity

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	alog "github.com/tonhe/headsup/internal/activity"
	"github.com/tonhe/headsup/internal/api/fake"
	"github.com/tonhe/headsup/internal/app"
	"github.com/tonhe/headsup/internal/icons"
	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/store"
	"github.com/tonhe/headsup/internal/ui/uitest"
	"github.com/tonhe/headsup/internal/watch"
)

var flowNow = time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

// actor is a module with three mutating actions, for the US12 independent
// test: perform three actions and see them in order with their targets.
type actor struct{}

func (actor) ID() string                                                { return "actor" }
func (actor) Title() string                                             { return "Actor" }
func (actor) Icon(icons.Set) string                                     { return "" }
func (actor) Order() int                                                { return 1 }
func (actor) Modes() module.Modes                                       { return module.ModeLocal }
func (actor) Badge(*store.Store) string                                 { return "" }
func (actor) Init(module.Context) tea.Cmd                               { return nil }
func (a actor) Update(module.Context, tea.Msg) (module.Module, tea.Cmd) { return a, nil }
func (actor) View(module.Context, module.Rect) string                   { return "actor" }
func (actor) Capturing() bool                                           { return false }
func (actor) Hints() []module.Hint                                      { return nil }
func (actor) Actions() []module.Action {
	mk := func(id, key, target string, files ...module.FileWrite) module.Action {
		return module.Action{ID: id, Title: id, Keys: []string{key}, Mutating: true,
			Targets: func(module.Context) []module.Target { return []module.Target{{Kind: "node", ID: "1", Name: target}} },
			Run: func(module.Context, module.Values) tea.Cmd {
				return func() tea.Msg { return module.ActionResult{ActionID: id, Files: files} }
			}}
	}
	return []module.Action{
		mk("actor.rename", "a", "alpha-node"),
		mk("actor.tags", "b", "bravo-node"),
		mk("actor.apply", "c", "charlie-node", module.FileWrite{Path: "/etc/headscale/config.yaml", BackupID: "config.yaml.bak", Added: 2, Removed: 1}),
	}
}

type driver struct {
	*uitest.Driver
	t   *testing.T
	mod *Model
	log *alog.File
}

func newDriver(t *testing.T) *driver {
	t.Helper()
	l, err := alog.Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	mod := New()
	mod.home = t.TempDir()
	m := app.New(app.Options{
		Backend: fake.Demo(fake.WithClock(func() time.Time { return flowNow })), Modules: []module.Module{actor{}, mod},
		Now: func() time.Time { return flowNow }, Refresh: time.Hour, Theme: "tokyo-night", Profile: "homelab", Activity: l,
	})
	return &driver{Driver: uitest.NewDriver(t, m, 120, 40), t: t, mod: mod, log: l}
}

func TestThreeActionsListedInOrder(t *testing.T) {
	d := newDriver(t)
	d.Key("1", "a", "y", "b", "y", "c", "y")
	d.Key("2")
	d.Want("Changes (3)", "actor.apply", "actor.tags", "actor.rename", "charlie", "+2 -1", "config.yaml.bak")
	scr := d.Screen()
	if i, j, k := strings.Index(scr, "actor.apply"), strings.Index(scr, "actor.tags"), strings.Index(scr, "actor.rename"); !(i < j && j < k) {
		t.Errorf("newest first expected:\n%s", scr)
	}

	// Filter.
	d.Key("/")
	d.Type("tags")
	d.Key("enter")
	d.Want("filter: tags", "actor.tags")
	d.WantNot("actor.rename")
	d.Key("esc")
	d.Want("actor.rename")

	// Live events tab.
	d.Key("tab")
	d.Want("No events yet")
	d.Send(watch.EventsMsg{Events: []watch.Event{
		{Kind: watch.NodeOffline, At: flowNow, Subject: "nas-01"},
		{Kind: watch.RouteAdvertised, At: flowNow, Subject: "gw", Detail: "10.9.0.0/24"},
	}})
	d.Want("Live events (2)", "nas-01 went offline", "gw advertised 10.9.0.0/24")
	d.Key("/")
	d.Type("route")
	d.Key("enter")
	d.WantNot("nas-01 went offline")
	d.Key("/", "esc")
	d.Want("nas-01 went offline")
	if hs := d.mod.Hints(); len(hs) != 3 {
		t.Errorf("hints = %+v", hs)
	}
}

func TestExport(t *testing.T) {
	d := newDriver(t)
	d.Key("1", "a", "y", "2")
	d.Send(watch.EventsMsg{Events: []watch.Event{{Kind: watch.NodeOnline, At: flowNow, Subject: "nas-01"}}})
	d.Key("x")
	d.Want("Export activity")
	d.Key("enter")
	path := filepath.Join(d.mod.home, "headsup-activity-"+flowNow.Format("20060102-150405")+".json")
	d.Want("Exported 1 entries and 1 events")
	d.Key("esc")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var ex Export
	if err := json.Unmarshal(b, &ex); err != nil {
		t.Fatal(err)
	}
	if len(ex.Entries) != 1 || ex.Entries[0].ActionID != "actor.rename" || len(ex.Events) != 1 || ex.Profile != "homelab" {
		t.Errorf("export = %+v", ex)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
			t.Errorf("mode = %v", fi.Mode().Perm())
		}
	}
	// A second export to the same name refuses to overwrite.
	d.Key("x", "enter")
	d.Want("file exists")
	if err := writeExport("", Export{}); err == nil {
		t.Error("empty path must fail")
	}
	if got := expandHome("~/x.json"); strings.HasPrefix(got, "~") {
		t.Errorf("expandHome = %q", got)
	}
}

func TestEmptyLogAndNoSource(t *testing.T) {
	m := New()
	if m.load(nil) != nil {
		t.Error("no source, no load")
	}
	if m.ID() != "activity" || m.Modes() != module.ModeLocal || m.Chord() == "" || m.Badge(nil) != "" || m.Title() != "Activity" {
		t.Error("identity")
	}
	r := entryRow{e: alog.Entry{Result: alog.Err("permission", "x")}}
	if r.Cell("result") != "error: permission" || r.Cell("nope") != "" || r.Cell("diff") != "" {
		t.Error("cells")
	}
	if (entryRow{e: alog.Entry{Result: alog.Result{Status: "error"}}}).Cell("result") != "error" {
		t.Error("bare error")
	}
	if (eventRow{e: watch.Event{Kind: watch.KeyExpiring}}).Cell("kind") != "key expiring" || (eventRow{}).Cell("x") != "" {
		t.Error("event cells")
	}
	d := newDriver(t)
	d.Key("2")
	d.Want("Nothing recorded yet")
}
