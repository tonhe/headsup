package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func click(x, y int) tea.MouseMsg {
	return tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
}

func wheel(x, y int, down bool) tea.MouseMsg {
	b := tea.MouseButtonWheelUp
	if down {
		b = tea.MouseButtonWheelDown
	}
	return tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: b}
}

func TestMouseSidebarClick(t *testing.T) {
	d := newDriver(t, 120, 40, Options{Mouse: true})
	d.send(click(4, 3)) // header is row 0; row 1 is "1 TAILNET", then Dashboard, Machines
	if got := d.m.current().ID(); got != "machines" {
		t.Fatalf("clicking the second sidebar entry should open machines, got %s", got)
	}
	d.send(click(4, 1)) // the section header goes to the section's first page
	if got := d.m.current().ID(); got != "dashboard" {
		t.Fatalf("clicking the section header should open its first page, got %s", got)
	}
	d.send(click(4, 3))
	d.send(click(4, 30)) // below the entries: nothing
	if got := d.m.current().ID(); got != "machines" {
		t.Errorf("click on empty sidebar moved to %s", got)
	}
}

func TestMouseWheelScrollsModule(t *testing.T) {
	d := newDriver(t, 120, 40, Options{Mouse: true})
	d.key("1", "m")
	first := cursorLine(d.view())
	d.send(wheel(60, 10, true))
	d.send(wheel(60, 10, true))
	if cursorLine(d.view()) == first {
		t.Error("wheel down should move the table cursor")
	}
	d.send(wheel(60, 10, false))
	d.send(click(60, 10)) // forwarded to the module; must not panic
}

// cursorLine returns the index of the first line holding the row cursor.
func cursorLine(view string) int {
	for i, l := range strings.Split(ansi.Strip(view), "\n") {
		if strings.Contains(l, "▌") {
			return i
		}
	}
	return -1
}

func TestMouseTabClick(t *testing.T) {
	d := newDriver(t, 80, 24, Options{Mouse: true})
	bar := strings.Split(ansi.Strip(d.view()), "\n")[1]
	x := strings.Index(bar, "Add device")
	if x < 0 {
		t.Fatalf("no third tab in %q", bar)
	}
	d.send(click(len([]rune(bar[:x])), 1))
	if got := d.m.current().ID(); got != "pending" {
		t.Fatalf("clicking the third tab should open pending, got %s", got)
	}
	if i := d.m.tabAt(79); i != -1 && i >= len(d.m.modules) {
		t.Errorf("tabAt out of range: %d", i)
	}
}

func TestMouseOffIgnoresClicks(t *testing.T) {
	d := newDriver(t, 120, 40, Options{})
	d.send(click(4, 2))
	if got := d.m.current().ID(); got != "dashboard" {
		t.Errorf("mouse off: click should be ignored, moved to %s", got)
	}
}

func TestMouseOverlays(t *testing.T) {
	d := newDriver(t, 120, 40, Options{Mouse: true})
	d.key("?")
	d.send(wheel(60, 20, true))
	d.key("esc")
	d.key(":")
	d.send(wheel(60, 20, true))
	if it, _ := d.m.palette.Selected(); it.ID == "" {
		t.Error("palette should keep a selection after scrolling")
	}
	d.key("esc", "T")
	before := d.m.picker.Current()
	d.send(wheel(60, 20, true))
	if d.m.picker.Current() == before {
		t.Error("wheel should move the theme picker")
	}
	if d.m.styles.Theme.Slug != d.m.picker.Current() {
		t.Error("wheel in the picker should preview")
	}
	d.key("esc")
	d.send(click(4, 3))
	if got := d.m.current().ID(); got != "machines" {
		t.Errorf("after closing modals clicks should work, on %s", got)
	}
}
