package app

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tonhe/headsup/internal/api/fake"
	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/modules/dashboard"
	"github.com/tonhe/headsup/internal/modules/machines"
	"github.com/tonhe/headsup/internal/modules/pending"
)

// driver runs a Model headlessly: it executes commands synchronously, drops
// any that block (ticks, blinks), and feeds results back in.
type driver struct {
	t    *testing.T
	m    *Model
	wait time.Duration // how long a command may run before it counts as a timer
}

func newDriver(t *testing.T, w, h int, opt Options) *driver {
	t.Helper()
	if opt.Backend == nil {
		opt.Backend = fake.Demo()
	}
	if opt.Modules == nil {
		opt.Modules = []module.Module{dashboard.New(), machines.New(), pending.New()}
	}
	opt.Refresh = time.Hour
	d := &driver{t: t, m: New(opt), wait: 150 * time.Millisecond}
	d.send(tea.WindowSizeMsg{Width: w, Height: h})
	d.exec(d.m.Init(), 0)
	return d
}

func (d *driver) send(msg tea.Msg) {
	_, cmd := d.m.Update(msg)
	d.exec(cmd, 0)
}

func (d *driver) exec(cmd tea.Cmd, depth int) {
	if cmd == nil || depth > 20 {
		return
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-done:
	case <-time.After(d.wait):
		return // a timer or blink: not needed for a headless render
	}
	switch msg := msg.(type) {
	case nil:
	case tea.BatchMsg:
		for _, c := range msg {
			d.exec(c, depth+1)
		}
	case tea.QuitMsg:
	default:
		_, next := d.m.Update(msg)
		d.exec(next, depth+1)
	}
}

func (d *driver) key(keys ...string) {
	for _, k := range keys {
		var msg tea.KeyMsg
		switch k {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		case "tab":
			msg = tea.KeyMsg{Type: tea.KeyTab}
		case "right":
			msg = tea.KeyMsg{Type: tea.KeyRight}
		case "space":
			msg = tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		d.send(msg)
	}
}

func windowSize(w, h int) tea.WindowSizeMsg { return tea.WindowSizeMsg{Width: w, Height: h} }

func ctrlS() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyCtrlS} }

func (d *driver) view() string { return d.m.View() }

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

func TestDemoScreensFit(t *testing.T) {
	for _, sz := range [][2]int{{80, 24}, {120, 40}, {160, 50}} {
		w, h := sz[0], sz[1]
		d := newDriver(t, w, h, Options{})
		for _, k := range []string{"1", "2", "3"} {
			d.key(k)
			out := d.view()
			checkFrame(t, out, w, h)
			if strings.Contains(out, "Loading") {
				t.Errorf("%dx%d module %s still loading", w, h, k)
			}
		}
	}
}

func TestDashboardShowsCounts(t *testing.T) {
	d := newDriver(t, 120, 40, Options{})
	out := d.view()
	for _, want := range []string{"42 / 51", "Needs attention"} {
		if !strings.Contains(out, want) {
			t.Errorf("dashboard missing %q:\n%s", want, out)
		}
	}
}

func TestMachinesFilterAndRename(t *testing.T) {
	b := fake.Demo()
	d := newDriver(t, 120, 40, Options{Backend: b})
	d.key("2", "/")
	d.key("n", "a", "s")
	d.key("enter")
	out := d.view()
	if !strings.Contains(out, "nas-01") {
		t.Fatalf("filter should show nas-01:\n%s", out)
	}
	d.key("r")
	if d.m.form == nil {
		t.Fatal("rename should open a form")
	}
	// Replace the prefilled name.
	for range 20 {
		d.send(tea.KeyMsg{Type: tea.KeyBackspace})
	}
	d.key("vault")
	d.key("enter")
	if d.m.form != nil {
		t.Fatalf("form should submit:\n%s", d.view())
	}
	found := false
	for _, c := range b.Calls() {
		if c.Method == "RenameNode" {
			found = true
		}
	}
	if !found {
		t.Fatalf("RenameNode not called; calls: %+v", b.Calls())
	}
}

func TestDeleteNeedsTypedConfirmation(t *testing.T) {
	b := fake.Demo()
	d := newDriver(t, 120, 40, Options{Backend: b})
	d.key("2", "D")
	if d.m.confirm == nil {
		t.Fatal("delete must ask for confirmation")
	}
	d.key("esc")
	d.key("D")
	d.key("y", "enter") // wrong: Critical needs the name typed
	for _, c := range b.Calls() {
		if c.Method == "DeleteNode" {
			t.Fatal("DeleteNode ran without the name being typed")
		}
	}
}

func TestDeleteWithTypedName(t *testing.T) {
	b := fake.Demo()
	d := newDriver(t, 120, 40, Options{Backend: b})
	d.key("2", "/")
	d.key("b", "a", "s", "t", "i", "o", "n", "enter", "D")
	d.key("bastion", "enter")
	n := 0
	for _, c := range b.Calls() {
		if c.Method == "DeleteNode" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("DeleteNode calls = %d, want 1", n)
	}
	for _, n := range d.m.store.Nodes {
		if n.DisplayName() == "bastion" {
			t.Error("deleted machine still in the store after refresh")
		}
	}
	if !strings.Contains(d.view(), "Machines • 0") {
		t.Errorf("filtered list should be empty after delete:\n%s", d.view())
	}
}

func TestReadOnlyBlocksMutations(t *testing.T) {
	b := fake.Demo()
	d := newDriver(t, 120, 40, Options{Backend: b, ReadOnly: true})
	d.key("2", "e", "D", "r")
	if d.m.confirm != nil || d.m.form != nil {
		t.Fatal("read-only must not open forms or confirmations for mutations")
	}
	for _, c := range b.Calls() {
		switch c.Method {
		case "ExpireNode", "DeleteNode", "RenameNode":
			t.Fatalf("%s ran in read-only mode", c.Method)
		}
	}
}

func TestPreAuthKeyShownOnce(t *testing.T) {
	d := newDriver(t, 120, 40, Options{Backend: fake.Demo(), ServerURL: "https://hs.example.com"})
	d.key("3", "n")
	if d.m.form == nil {
		t.Fatal("n should open the new-key form")
	}
	d.send(tea.KeyMsg{Type: tea.KeyCtrlS})
	out := d.view()
	if !strings.Contains(out, "tailscale up --login-server=https://hs.example.com --authkey=") {
		t.Fatalf("reveal should show the command:\n%s", out)
	}
	d.key("esc")
	if strings.Contains(d.view(), "--authkey=") {
		t.Fatal("key must not be shown after closing the reveal")
	}
}

func TestTinyTerminal(t *testing.T) {
	d := newDriver(t, 60, 20, Options{})
	if !strings.Contains(d.view(), "Please resize") {
		t.Fatal("expected resize notice below 80x24")
	}
}
