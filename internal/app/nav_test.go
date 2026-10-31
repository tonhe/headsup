package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/tonhe/headsup/internal/api/fake"
	"github.com/tonhe/headsup/internal/icons"
	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/modules/registry"
	"github.com/tonhe/headsup/internal/store"
)

// recorder is a module that records every key it is sent, to prove keys
// typed during a section jump never reach the page.
type recorder struct {
	id, title string
	sec       module.Section
	order     int
	keys      *[]string
}

func (r recorder) ID() string                { return r.id }
func (r recorder) Title() string             { return r.title }
func (recorder) Icon(icons.Set) string       { return "" }
func (r recorder) Order() int                { return r.order }
func (recorder) Modes() module.Modes         { return module.ModeLocal }
func (r recorder) Section() module.Section   { return r.sec }
func (recorder) Badge(*store.Store) string   { return "" }
func (recorder) Init(module.Context) tea.Cmd { return nil }
func (r recorder) View(module.Context, module.Rect) string {
	return strings.ToUpper(r.title) + " PAGE"
}
func (recorder) Actions() []module.Action { return nil }
func (recorder) Capturing() bool          { return false }
func (recorder) Hints() []module.Hint     { return nil }
func (r recorder) Update(_ module.Context, msg tea.Msg) (module.Module, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		*r.keys = append(*r.keys, k.String())
	}
	return r, nil
}

func screen(d *driver) string { return ansi.Strip(d.view()) }

func lastLine(s string) string {
	ls := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return ls[len(ls)-1]
}

func TestSectionJumpShowsHintsThenLetterJumps(t *testing.T) {
	d := allModulesDriver(t, Options{})
	d.key("1")
	if d.m.jump != module.SectionTailnet {
		t.Fatalf("1 should start a Tailnet jump, jump=%v", d.m.jump)
	}
	scr := screen(d)
	foot := lastLine(scr)
	for _, want := range []string{"1 Tailnet", "h Dashboard", "m Machines", "u Users", "r Routes", "p Add device", "k Keys", "a Access controls", "esc cancel"} {
		if !strings.Contains(foot, want) {
			t.Errorf("which-key line missing %q: %q", want, foot)
		}
	}
	for _, want := range []string{"1 TAILNET   pick one", "  m Machines", "  u Users"} {
		if !strings.Contains(scr, want) {
			t.Errorf("sidebar missing %q in jump mode:\n%s", want, scr)
		}
	}
	d.key("u")
	if got := d.m.current().ID(); got != "users" {
		t.Fatalf("1 u should open users, on %s", got)
	}
	if d.m.jump != 0 || strings.Contains(screen(d), "pick one") {
		t.Error("jump mode should end after the page key")
	}

	d.key("2", "d") // host-only DNS is hidden in remote mode: d is unbound here
	if got := d.m.current().ID(); got != "users" {
		t.Errorf("2 d in remote mode should do nothing, on %s", got)
	}
	d.key("3", "v")
	if got := d.m.current().ID(); got != "activity" {
		t.Errorf("3 v should open activity, on %s", got)
	}
	d.key("3", ",")
	if got := d.m.current().ID(); got != "prefs" {
		t.Errorf("3 , should open preferences, on %s", got)
	}
}

func TestSectionDigitTwiceOrEnterGoesToFirstPage(t *testing.T) {
	d := allModulesDriver(t, Options{})
	d.key("1", "m")
	d.key("1", "1")
	if got := d.m.current().ID(); got != "dashboard" {
		t.Errorf("1 1 should open the first Tailnet page, on %s", got)
	}
	d.key("3", "enter")
	if got := d.m.current().ID(); got != "activity" {
		t.Errorf("3 enter should open the first App page, on %s", got)
	}
	d.key("1", "3") // a digit picks the nth page
	if got := d.m.current().ID(); got != "users" {
		t.Errorf("1 3 should open the third Tailnet page, on %s", got)
	}
	d.key("1", "9") // past the end: cancel
	if got := d.m.current().ID(); got != "users" || d.m.jump != 0 {
		t.Errorf("1 9 should just cancel, on %s jump=%v", got, d.m.jump)
	}
}

func newRecorderDriver(t *testing.T, w, h int) (*driver, *[]string) {
	var got []string
	mods := []module.Module{
		recorder{id: "alpha", title: "Alpha", sec: module.SectionTailnet, order: 1, keys: &got},
		recorder{id: "bravo", title: "Bravo", sec: module.SectionTailnet, order: 2, keys: &got},
		recorder{id: "settings", title: "Settings", sec: module.SectionApp, order: 1, keys: &got},
	}
	return newDriver(t, w, h, Options{Modules: mods}), &got
}

func TestJumpEscAndUnboundKeysAreSwallowed(t *testing.T) {
	d, got := newRecorderDriver(t, 120, 40)
	d.key("1", "esc")
	if d.m.jump != 0 || d.m.current().ID() != "alpha" {
		t.Fatalf("esc should cancel and stay put: jump=%v on %s", d.m.jump, d.m.current().ID())
	}
	d.key("1", "x", "1", "/", "1", "j", "3", "q")
	if len(*got) != 0 {
		t.Errorf("keys during a jump reached the page: %q", *got)
	}
	if d.m.quitting || d.m.current().ID() != "alpha" || d.m.jump != 0 {
		t.Errorf("unbound keys must only cancel: quitting=%v on %s jump=%v", d.m.quitting, d.m.current().ID(), d.m.jump)
	}
	// After cancelling, keys reach the page again.
	d.key("x")
	if len(*got) != 1 || (*got)[0] != "x" {
		t.Errorf("keys after a cancelled jump should reach the page: %q", *got)
	}
	// Page keys fall back to title letters; an empty section does nothing.
	d.key("1", "b")
	if d.m.current().ID() != "bravo" {
		t.Errorf("1 b should open Bravo, on %s", d.m.current().ID())
	}
	d.key("2")
	if d.m.jump != 0 {
		t.Error("an empty section must not start a jump")
	}
}

func TestRemoteModeHidesHostOnlyPages(t *testing.T) {
	d := allModulesDriver(t, Options{})
	scr := screen(d)
	for _, want := range []string{"1 TAILNET", "2 SERVER", "3 APP", "◎ Server", "Activity", "Preferences", "g m"} {
		if !strings.Contains(scr, want) {
			t.Errorf("remote sidebar missing %q:\n%s", want, scr)
		}
	}
	for _, hidden := range []string{"Server Settings", "DNS", "DERP", "⏻ Service"} {
		if strings.Contains(scr, hidden) {
			t.Errorf("remote sidebar should hide host-only %q:\n%s", hidden, scr)
		}
	}
	d.key("2")
	foot := lastLine(screen(d))
	if !strings.Contains(foot, "S Server") || strings.Contains(foot, "DNS") {
		t.Errorf("remote SERVER jump should offer only Server: %q", foot)
	}
	d.key("esc")
	// The chord still reaches a hidden page, which explains host mode.
	d.key("g", "d")
	if d.m.current().ID() != "dns" || !strings.Contains(screen(d), "host mode") {
		t.Errorf("g d should still open DNS and explain host mode, on %s", d.m.current().ID())
	}
	// ] and [ cycle over visible pages only.
	d.key("1", "a", "]")
	if got := d.m.current().ID(); got != "server" {
		t.Errorf("] after Access controls should go to Server, on %s", got)
	}
	d.key("]")
	if got := d.m.current().ID(); got != "activity" {
		t.Errorf("] after Server should skip host-only pages to Activity, on %s", got)
	}
}

func TestHostModeServerSection(t *testing.T) {
	b := fake.Demo()
	// Service is left out: its log stream would outlive the test's bubble.
	d := newDriver(t, 120, 40, Options{Backend: b, Host: demoHostFor(t, b), Modules: registry.All([]string{"service"}), Profile: "host:hs-demo"})
	d.key("2")
	foot := lastLine(screen(d))
	for _, want := range []string{"2 Server", "S Server", "s Server Settings", "d DNS", "D DERP"} {
		if !strings.Contains(foot, want) {
			t.Errorf("host SERVER jump missing %q: %q", want, foot)
		}
	}
	d.key("D")
	if got := d.m.current().ID(); got != "derp" {
		t.Errorf("2 D should open DERP, on %s", got)
	}
	d.key("2", "2")
	if got := d.m.current().ID(); got != "server" {
		t.Errorf("2 2 should open Server, on %s", got)
	}
}

func TestChordsAndPaletteStillJump(t *testing.T) {
	d := allModulesDriver(t, Options{})
	d.key("g", "m")
	if got := d.m.current().ID(); got != "machines" {
		t.Errorf("g m should open machines, on %s", got)
	}
	d.key(",")
	if got := d.m.current().ID(); got != "prefs" {
		t.Errorf(", should open preferences, on %s", got)
	}
	d.Do(func() tea.Cmd { return d.m.runCommand("app.goto.keys") })
	if got := d.m.current().ID(); got != "keys" {
		t.Errorf("palette Go to Keys should open keys, on %s", got)
	}
}

func TestWhichKeyInTabLayoutAndCollapsedSidebar(t *testing.T) {
	d := allModulesDriver(t, Options{})
	d.send(windowSize(80, 24))
	d.key("1")
	scr := screen(d)
	foot := lastLine(scr)
	for _, want := range []string{"1 Tailnet", "h Dashb", "m Machi", "a Access", "esc"} {
		if !strings.Contains(foot, want) {
			t.Errorf("80x24 which-key line missing %q: %q", want, foot)
		}
	}
	bar := strings.Split(scr, "\n")[1]
	if !strings.Contains(bar, "m Machines") || strings.Contains(bar, "Activity") {
		t.Errorf("tab bar should list the pending section's pages with keys: %q", bar)
	}
	checkFrame(t, d.view(), 80, 24)
	d.key("esc")
	if strings.Contains(strings.Split(screen(d), "\n")[1], "m Machines") {
		t.Error("tab bar keys should go away after cancelling")
	}

	d.send(windowSize(120, 40))
	d.key("ctrl+b") // sidebar off: the tab bar replaces it
	d.key("3")
	scr = screen(d)
	if !strings.Contains(lastLine(scr), "3 App") || strings.Contains(scr, "3 APP") {
		t.Errorf("collapsed sidebar should still show the which-key line:\n%s", scr)
	}
	d.key("v")
	if got := d.m.current().ID(); got != "activity" {
		t.Errorf("3 v with the sidebar off should open activity, on %s", got)
	}
}

func TestHelpListsNavigation(t *testing.T) {
	d := allModulesDriver(t, Options{})
	secs := d.m.helpSections()
	if !helpHas(secs, "Navigation", "First page of the section", "1 1") {
		t.Error("help should explain the double digit")
	}
	if !helpHas(secs, "Go to: 1 Tailnet", "Machines", "1 m  ·  g m") {
		t.Errorf("help should list each page's jump and chord: %+v", secs)
	}
	if !helpHas(secs, "Go to: 3 App", "Preferences", "3 ,  ·  g , / ,") {
		t.Error("help should list Preferences with its direct keys")
	}
	if !helpHas(secs, "Go to: host-only pages", "DNS (host mode)", "g d") {
		t.Error("help should keep hidden pages' chords")
	}
	d.key("?")
	if !strings.Contains(screen(d), "Pick a section") {
		t.Errorf("help overlay should show the navigation section:\n%s", screen(d))
	}
}

func TestRemappedSectionKey(t *testing.T) {
	d := allModulesDriver(t, Options{KeyOverrides: map[string][]string{
		"app.section.tailnet": {"F"},
		"app.goto.machines":   {"g x"},
	}})
	d.key("F")
	if !strings.Contains(lastLine(screen(d)), "F Tailnet") || !strings.Contains(lastLine(screen(d)), "x Machines") {
		t.Errorf("remapped keys should show in the which-key line: %q", lastLine(screen(d)))
	}
	d.key("x")
	if got := d.m.current().ID(); got != "machines" {
		t.Errorf("F x should open machines, on %s", got)
	}
}
