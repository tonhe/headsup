package spinner

import (
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/tonhe/headsup/internal/icons"
	"github.com/tonhe/headsup/internal/ui/uitest"
)

func TestSpinner(t *testing.T) {
	for _, ic := range []icons.Set{icons.Unicode, icons.ASCII} {
		m := New(uitest.StyleFor(""), ic)
		if m.Tick() == nil || lipgloss.Width(m.View()) != 1 {
			t.Errorf("%s: view %q", ic.Name, m.View())
		}
		msg := m.Tick()()
		if m.Update(msg) == nil {
			t.Errorf("%s: no follow-up tick", ic.Name)
		}
	}
}
