package help

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/ui/uitest"
)

func sections(n int) []Section {
	var out []Section
	for i := 0; i < n; i++ {
		sec := Section{Title: fmt.Sprintf("Section %d", i)}
		for j := 0; j < 6; j++ {
			sec.Keys = append(sec.Keys, module.Hint{Key: fmt.Sprintf("k%d", j), Desc: "does a thing"})
		}
		out = append(out, sec)
	}
	return out
}

func TestViewAndScroll(t *testing.T) {
	uitest.ForceColor()
	m := New(uitest.StyleFor(""), sections(8))
	for _, sz := range [][2]int{{80, 24}, {30, 10}, {120, 40}} {
		m.SetSize(sz[0], sz[1])
		lines := strings.Split(m.View(), "\n")
		if len(lines) != sz[1] {
			t.Fatalf("lines %d", len(lines))
		}
		for i, l := range lines {
			if lipgloss.Width(l) != sz[0] {
				t.Errorf("%v line %d width %d", sz, i, lipgloss.Width(l))
			}
		}
	}
	m.SetSize(80, 24)
	if !strings.Contains(ansi.Strip(m.View()), "Section 0") || !strings.Contains(ansi.Strip(strings.Split(m.View(), "\n")[1]), "Section") {
		t.Error("multi-column layout missing")
	}
	m.SetSize(30, 10)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	if m.Offset() == 0 {
		t.Error("G did not scroll")
	}
	if _, ok := m.Update(tea.KeyMsg{Type: tea.KeyEsc})().(CloseMsg); !ok {
		t.Error("esc should close")
	}
}

func TestFilterDimAndMouse(t *testing.T) {
	secs := []Section{
		{Title: "Machines", Keys: []module.Hint{{Key: "r", Desc: "Rename machine"}, {Key: "D", Desc: "Delete machine"}}, Dim: map[int]bool{1: true}},
		{Title: "Global", Keys: []module.Hint{{Key: "?", Desc: "Help"}, {Key: "ctrl+b", Desc: "Toggle sidebar"}}},
	}
	m := New(uitest.StyleFor(""), secs)
	m.SetSize(80, 20)
	if len(m.Sections()) != 2 {
		t.Fatal("sections")
	}
	key := func(s string) { m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}) }
	key("/")
	key("delete")
	m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	out := ansi.Strip(m.View())
	if m.Query() != "delete" || !strings.Contains(out, "Delete machine") || strings.Contains(out, "Rename") {
		t.Fatalf("filter %q:\n%s", m.Query(), out)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(ansi.Strip(m.View()), "filter: delete") {
		t.Error("kept filter should be shown")
	}
	key("/")
	m.Update(tea.KeyMsg{Type: tea.KeyEsc}) // leaves typing and clears
	key("/")
	key("global")
	if out := ansi.Strip(m.View()); !strings.Contains(out, "Toggle sidebar") {
		t.Errorf("matching a section title keeps the whole section:\n%s", out)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	key("/")
	key("zzzz")
	if !strings.Contains(ansi.Strip(m.View()), "No keys match") {
		t.Error("empty filter result")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.Query() != "" {
		t.Error("esc clears a kept filter")
	}
	m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelDown})
	m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelUp})
	for _, k := range []string{"j", "k", "g"} {
		key(k)
	}
	if _, ok := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})().(CloseMsg); !ok {
		t.Error("q closes")
	}
}
