package tabs

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/tonhe/headsup/internal/icons"
	"github.com/tonhe/headsup/internal/ui/uitest"
)

func TestRender(t *testing.T) {
	uitest.ForceColor()
	s := uitest.StyleFor("")
	var items []Item
	for i, n := range []string{"Dashboard", "Machines", "Users", "Routes", "Keys", "Policy", "DNS", "DERP", "Settings", "Activity"} {
		items = append(items, Item{ID: n, Title: n, Key: string(rune('0' + i%10))})
	}
	for _, c := range []struct {
		w      int
		active string
	}{{80, "Activity"}, {80, "Dashboard"}, {40, "Policy"}, {10, "Settings"}, {200, "Keys"}} {
		out := Render(s, icons.Unicode, items, c.active, c.w)
		if lipgloss.Width(out) != c.w {
			t.Errorf("w=%d width %d", c.w, lipgloss.Width(out))
		}
		if c.w >= 20 && !strings.Contains(ansi.Strip(out), c.active) {
			t.Errorf("active %s not visible: %q", c.active, ansi.Strip(out))
		}
	}
}
