package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/tonhe/headsup/internal/api/fake"
	"github.com/tonhe/headsup/internal/config"
	"github.com/tonhe/headsup/internal/modules/registry"
	"github.com/tonhe/headsup/internal/ui/help"
	"github.com/tonhe/headsup/internal/ui/palette"

	_ "github.com/tonhe/headsup/internal/modules/all"
)

func allModulesDriver(t *testing.T, opt Options) *driver {
	t.Helper()
	opt.Modules = registry.All(nil)
	return newDriver(t, 120, 40, opt)
}

func helpHas(secs []help.Section, section, desc, key string) bool {
	for _, s := range secs {
		if !strings.HasPrefix(s.Title, section) {
			continue
		}
		for _, k := range s.Keys {
			if k.Desc == desc && strings.Contains(" / "+k.Key+" / ", " / "+key+" / ") {
				return true
			}
		}
	}
	return false
}

// TestEveryKeyedActionInPaletteAndHelp (T126, SC-007): every registered
// Action with Keys is reachable from the palette and listed in help with its
// key, whichever module is active. Generic over whatever modules exist.
func TestEveryKeyedActionInPaletteAndHelp(t *testing.T) {
	d := allModulesDriver(t, Options{})
	if len(d.m.modules) < 4 {
		t.Fatalf("expected the built-in modules, got %d", len(d.m.modules))
	}
	for active := range d.m.modules {
		d.m.active = active
		items := map[string]palette.Item{}
		for _, it := range d.m.paletteItems() {
			items[it.ID] = it
		}
		secs := d.m.helpSections()
		for _, mod := range d.m.modules {
			if _, ok := items["app.goto."+mod.ID()]; !ok {
				t.Errorf("palette has no go-to for %s", mod.ID())
			}
			for _, a := range mod.Actions() {
				if _, ok := items[a.ID]; !ok {
					t.Errorf("palette is missing %s", a.ID)
				}
				for _, k := range a.Keys {
					if !helpHas(secs, mod.Title(), a.Title, k) {
						t.Errorf("help (on %s) is missing %s %q for %s", d.m.modules[active].ID(), k, a.Title, a.ID)
					}
				}
			}
		}
		for _, a := range appActions {
			if a.id != "app.palette" {
				if _, ok := items[a.id]; !ok {
					t.Errorf("palette is missing %s", a.id)
				}
			}
			if !helpHas(secs, "Global", a.title, a.keys[0]) {
				t.Errorf("help is missing global %s", a.id)
			}
		}
	}
}

// TestRemappedKeyWorksAndShowsInHelp (US9 independent test).
func TestRemappedKeyWorksAndShowsInHelp(t *testing.T) {
	d := allModulesDriver(t, Options{KeyOverrides: map[string][]string{
		"machines.detail": {"o"},
		"app.palette":     {"ctrl+k"},
	}})
	d.key("1", "m")
	if !helpHas(d.m.helpSections(), "Machines", "Toggle detail pane", "o") {
		t.Error("help should show the remapped key")
	}
	if helpHas(d.m.helpSections(), "Machines", "Toggle detail pane", "enter") {
		t.Error("help should not show the replaced default")
	}
	d.send(tea.KeyMsg{Type: tea.KeyCtrlK})
	if d.m.palette == nil {
		t.Fatal("remapped palette key should open the palette")
	}
	d.key("esc")
	d.key(":")
	if d.m.palette != nil {
		t.Error("the replaced default palette key should no longer open it")
	}
}

func TestPaletteOpenFilterRun(t *testing.T) {
	d := allModulesDriver(t, Options{})
	d.key(":")
	if d.m.palette == nil {
		t.Fatal(": should open the palette")
	}
	out := ansi.Strip(d.view())
	checkFrame(t, d.view(), 120, 40)
	if !strings.Contains(out, "Commands") {
		t.Fatalf("palette not drawn:\n%s", out)
	}
	d.key("go to add device")
	it, ok := d.m.palette.Selected()
	if !ok || it.ID != "app.goto.pending" {
		t.Fatalf("top match = %+v, want app.goto.pending", it)
	}
	d.key("enter")
	if d.m.palette != nil || d.m.current().ID() != "pending" {
		t.Fatalf("enter should run the jump; on %s", d.m.current().ID())
	}

	// Recent first on reopen.
	d.send(tea.KeyMsg{Type: tea.KeyCtrlP})
	if it, _ := d.m.palette.Selected(); it.ID != "app.goto.pending" {
		t.Errorf("most recent command should come first, got %s", it.ID)
	}
	d.key("esc")
	if d.m.palette != nil {
		t.Error("esc should close")
	}

	// A module action from another module switches there and runs.
	d.key(":", "toggle detail pane", "enter")
	if d.m.current().ID() != "machines" {
		t.Errorf("running a machines action should switch to machines; on %s", d.m.current().ID())
	}

	// Theme entries.
	d.key(":", "theme nord", "enter")
	if d.m.styles.Theme.Slug != "nord" {
		t.Errorf("theme command should apply nord, got %s", d.m.styles.Theme.Slug)
	}
	d.key(":", "help", "enter")
	if d.m.help == nil {
		t.Error("palette Help should open help")
	}
}

func TestPaletteReadOnlyDims(t *testing.T) {
	d := allModulesDriver(t, Options{ReadOnly: true, Backend: fake.Demo()})
	for _, it := range d.m.paletteItems() {
		if it.ID == "machines.expire" && !it.Disabled {
			t.Error("mutations should be dimmed in read-only mode")
		}
	}
	d.key(":", "expire machine", "enter")
	if d.m.confirm != nil {
		t.Error("read-only gate must still apply to palette commands")
	}
}

func TestHelpOverlayContextAndFilter(t *testing.T) {
	d := allModulesDriver(t, Options{})
	d.key("1", "m", "?")
	if d.m.help == nil {
		t.Fatal("? should open help")
	}
	secs := d.m.help.Sections()
	if !strings.HasPrefix(secs[0].Title, "Machines") {
		t.Errorf("help should lead with the current module, got %q", secs[0].Title)
	}
	d.key("/", "rename")
	out := ansi.Strip(d.view())
	if !strings.Contains(out, "Rename machine") || strings.Contains(out, "Toggle sidebar") {
		t.Errorf("filter should narrow help to matches:\n%s", out)
	}
	d.key("enter", "esc")
	if d.m.help == nil {
		t.Fatal("first esc clears the filter")
	}
	d.key("esc")
	if d.m.help != nil {
		t.Error("second esc closes help")
	}
}

func TestHelpDimsGatedActions(t *testing.T) {
	d := allModulesDriver(t, Options{ReadOnly: true})
	d.key("1", "m")
	secs := d.m.helpSections()
	dimmed := false
	for i, k := range secs[0].Keys {
		if k.Desc == "Rename machine" {
			dimmed = secs[0].Dim[i]
		}
	}
	if !dimmed {
		t.Error("read-only: Rename should be dimmed in help")
	}
}

func TestPrefsJumpKey(t *testing.T) {
	d := allModulesDriver(t, Options{})
	d.key(",")
	if d.m.current().ID() != "prefs" {
		t.Fatalf(", should open Preferences, on %s", d.m.current().ID())
	}
	d.key("1", "h", "g", ",")
	if d.m.current().ID() != "prefs" {
		t.Errorf("g , should open Preferences, on %s", d.m.current().ID())
	}
	if !strings.Contains(ansi.Strip(d.view()), "Preferences") {
		t.Error("label should be Preferences")
	}
}

func TestThemePickerAndProfileOnlyWithoutProfile(t *testing.T) {
	d := newDriver(t, 120, 40, Options{})
	d.key("T")
	if d.m.picker == nil {
		t.Fatal("T should open the theme picker")
	}
	checkFrame(t, d.view(), 120, 40)
	d.key("nord")
	if d.m.styles.Theme.Slug != "nord" {
		t.Errorf("preview should apply nord, got %s", d.m.styles.Theme.Slug)
	}
	d.send(tea.KeyMsg{Type: tea.KeyCtrlT}) // no profile: ignored
	if d.m.picker == nil {
		t.Error("ctrl+t without a config profile should do nothing")
	}
	d.send(tea.KeyMsg{Type: tea.KeyCtrlR}) // no themes dir: info toast
	d.key("enter")
	if d.m.picker != nil || d.m.opt.Config.UI.Theme != "nord" {
		t.Errorf("enter should choose nord; config theme %q", d.m.opt.Config.UI.Theme)
	}
	if cmd := d.m.chooseTheme("no-such-theme", false); cmd == nil {
		t.Error("unknown theme should toast")
	}
}

func TestSwitchAppliesProfileTheme(t *testing.T) {
	d := newDriver(t, 120, 40, Options{})
	cfg := d.m.opt.Config
	cfg.Profiles = append(cfg.Profiles, config.Profile{Name: "prod", Theme: "dracula"})
	d.send(switchedMsg{conn: Conn{Backend: fake.Demo(), Label: "prod"}})
	if d.m.styles.Theme.Slug != "dracula" {
		t.Errorf("switching to prod should apply its theme, got %s", d.m.styles.Theme.Slug)
	}
	if p, s, ok := d.m.ProfileTheme(); !ok || p != "prod" || s != "dracula" {
		t.Errorf("ProfileTheme = %q %q %v", p, s, ok)
	}
	if cmd := d.m.SetProfileTheme("bogus"); cmd == nil {
		t.Error("unknown theme should be rejected")
	}
}

func TestSetUIPrefsRejectsInvalid(t *testing.T) {
	d := newDriver(t, 120, 40, Options{})
	d.Do(func() tea.Cmd { return d.m.SetUIPrefs(func(u *config.UI) { u.Icons = "emoji" }) })
	if d.m.UIPrefs().Icons == "emoji" {
		t.Error("invalid icons must be rejected")
	}
	d.Do(func() tea.Cmd { return d.m.SetUIPrefs(func(u *config.UI) { u.Theme = "no-such" }) })
	if d.m.UIPrefs().Theme == "no-such" {
		t.Error("unknown theme must be rejected")
	}
	d.Do(func() tea.Cmd { return d.m.SetUIPrefs(func(u *config.UI) { u.Sidebar = "never"; u.Refresh = "9s" }) })
	if d.m.useSidebar() || d.m.opt.Refresh.Seconds() != 9 {
		t.Error("sidebar and refresh should apply live")
	}
	if d.m.ConfigSaved() {
		t.Error("no config path: not saved")
	}
	_ = d.m.ColumnSets()
}
