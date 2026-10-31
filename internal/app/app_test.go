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
	"github.com/tonhe/headsup/internal/ui/uitest"
)

// driver runs a Model headlessly on the shared flow-test driver (fake clock,
// no dropped commands); see uitest.Driver.
type driver struct {
	*uitest.Driver
	t *testing.T
	m *Model
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
	m := New(opt)
	return &driver{Driver: uitest.NewDriver(t, m, w, h), t: t, m: m}
}

func (d *driver) send(msg tea.Msg)   { d.Send(msg) }
func (d *driver) key(keys ...string) { d.Key(keys...) }
func (d *driver) view() string       { return d.View() }

func windowSize(w, h int) tea.WindowSizeMsg { return tea.WindowSizeMsg{Width: w, Height: h} }

func ctrlS() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyCtrlS} }

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
		for _, k := range []string{"1 h", "1 m", "1 p"} {
			d.key(strings.Fields(k)...)
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
	d.key("1", "m", "/")
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
	d.key("1", "m", "D")
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
	d.key("1", "m", "/")
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
	d.key("1", "m", "e", "D", "r")
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
	d.key("1", "p", "n")
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
