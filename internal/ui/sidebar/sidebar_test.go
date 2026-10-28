package sidebar

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/tonhe/fathom/internal/icons"
	"github.com/tonhe/fathom/internal/ui/uitest"
)

func Items() []Item {
	return []Item{
		{ID: "dash", Title: "Dashboard", Icon: "◫", Key: "1"},
		{ID: "machines", Title: "Machines", Icon: "▣", Key: "2", Badge: "51"},
		{ID: "routes", Title: "Routes with a very long title", Key: "3", Badge: "2"},
		{ID: "service", Title: "Service", Dim: true},
	}
}

func TestRender(t *testing.T) {
	uitest.ForceColor()
	s := uitest.StyleFor("")
	for _, c := range []struct{ w, h int }{{20, 24}, {8, 3}, {40, 2}} {
		out := Render(s, icons.Unicode, Items(), "machines", c.w, c.h)
		lines := strings.Split(out, "\n")
		if len(lines) != c.h {
			t.Fatalf("lines %d", len(lines))
		}
		for i, l := range lines {
			if lipgloss.Width(l) != c.w {
				t.Errorf("%v line %d width %d: %q", c, i, lipgloss.Width(l), ansi.Strip(l))
			}
		}
	}
	p := ansi.Strip(Render(s, icons.Unicode, Items(), "machines", 24, 4))
	if !strings.Contains(p, "▸ 2 ▣ Machines") || !strings.Contains(p, " 51 ") {
		t.Errorf("render:\n%s", p)
	}
}
