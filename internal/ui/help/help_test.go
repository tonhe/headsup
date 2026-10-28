package help

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/tonhe/fathom/internal/module"
	"github.com/tonhe/fathom/internal/ui/uitest"
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
