package palette

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/tonhe/headsup/internal/icons"
	"github.com/tonhe/headsup/internal/ui/uitest"
)

func items() []Item {
	return []Item{
		{ID: "app.help", Title: "Help", Keys: "?"},
		{ID: "machines.expire", Title: "Expire machine", Group: "Machines", Keys: "e"},
		{ID: "keys.new", Title: "New pre-auth key", Group: "Keys", Keys: "n"},
		{ID: "app.goto.routes", Title: "Routes", Group: "Go to", Keys: "4 / g r"},
		{ID: "app.theme.nord", Title: "Nord", Group: "Theme"},
		{ID: "machines.delete", Title: "Delete machine with a very long title that will not fit in the box", Group: "Machines", Keys: "D", Disabled: true, Reason: "read-only"},
	}
}

func runes(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

func msgOf(t *testing.T, c tea.Cmd) tea.Msg {
	t.Helper()
	if c == nil {
		t.Fatal("expected a command")
	}
	return c()
}

func TestFuzzyAndRecent(t *testing.T) {
	m := New(uitest.StyleFor(""), icons.Unicode, items(), []string{"keys.new", "keys.new", "app.theme.nord"})
	m.SetSize(60, 12)
	got := m.Matches()
	if got[0].ID != "keys.new" || got[1].ID != "app.theme.nord" {
		t.Fatalf("recent items should lead: %v %v", got[0].ID, got[1].ID)
	}
	m.Update(runes("expire"))
	if it, _ := m.Selected(); it.ID != "machines.expire" {
		t.Fatalf("expire -> %+v", it)
	}
	if c, ok := msgOf(t, m.Update(tea.KeyMsg{Type: tea.KeyEnter})).(ChosenMsg); !ok || c.ID != "machines.expire" {
		t.Fatalf("enter = %#v", c)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if m.Query() != "expir" {
		t.Errorf("backspace: %q", m.Query())
	}
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlW})
	if m.Query() != "" {
		t.Errorf("ctrl+w: %q", m.Query())
	}
	m.Update(runes("go"))
	m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m.Update(runes("rou"))
	if it, _ := m.Selected(); it.ID != "app.goto.routes" {
		t.Errorf("go rou -> %+v", it)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	m.Update(runes("zzzzqq"))
	if len(m.Matches()) != 0 {
		t.Error("nonsense should match nothing")
	}
	if !strings.Contains(ansi.Strip(m.View()), "No matching commands") {
		t.Error("empty state missing")
	}
	if m.Update(tea.KeyMsg{Type: tea.KeyEnter}) != nil {
		t.Error("enter with no match should do nothing")
	}
	// esc clears the query first, then closes.
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.Query() != "" {
		t.Error("esc should clear the query")
	}
	if _, ok := msgOf(t, m.Update(tea.KeyMsg{Type: tea.KeyEsc})).(CloseMsg); !ok {
		t.Error("second esc should close")
	}
}

func TestMovementAndView(t *testing.T) {
	m := New(uitest.StyleFor(""), icons.ASCII, items(), nil)
	m.SetSize(50, 7) // list of 3 rows
	for _, k := range []tea.KeyType{tea.KeyDown, tea.KeyCtrlN, tea.KeyTab, tea.KeyPgDown, tea.KeyEnd} {
		m.Update(tea.KeyMsg{Type: k})
	}
	if it, _ := m.Selected(); it.ID != "machines.delete" {
		t.Errorf("end -> %s", it.ID)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyHome})
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	if it, _ := m.Selected(); it.ID != "app.help" {
		t.Errorf("home -> %s", it.ID)
	}
	m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelDown})
	m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelUp})
	m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	for _, sz := range [][2]int{{50, 7}, {84, 22}, {30, 6}} {
		m.SetSize(sz[0], sz[1])
		v := m.View()
		lines := strings.Split(v, "\n")
		if len(lines) != sz[1] {
			t.Errorf("%v: %d lines", sz, len(lines))
		}
		for i, l := range lines {
			if lipgloss.Width(l) != sz[0] {
				t.Errorf("%v line %d width %d", sz, i, lipgloss.Width(l))
			}
		}
	}
	m.SetSize(84, 22)
	if !strings.Contains(ansi.Strip(m.View()), "up/down") {
		t.Error("ascii help text expected")
	}
	m.SetSize(5, 3)
	if m.View() != "" {
		t.Error("too small should render nothing")
	}
	if _, ok := msgOf(t, m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})).(CloseMsg); !ok {
		t.Error("ctrl+c closes")
	}
	if m.Update(nil) != nil {
		t.Error("unknown msg")
	}
}
