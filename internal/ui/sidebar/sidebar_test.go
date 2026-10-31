package sidebar

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/tonhe/headsup/internal/icons"
	"github.com/tonhe/headsup/internal/ui/uitest"
)

func Groups() []Group {
	return []Group{
		{Key: "1", Title: "Tailnet", Items: []Item{
			{ID: "dash", Title: "Dashboard", Icon: "◫", Key: "h", Chord: "g h"},
			{ID: "machines", Title: "Machines", Icon: "▣", Key: "m", Chord: "g m", Badge: "51"},
			{ID: "routes", Title: "Routes with a very long title", Key: "r", Chord: "g r", Badge: "2"},
		}},
		{Key: "2", Title: "Empty"},
		{Key: "3", Title: "App", Items: []Item{
			{ID: "service", Title: "Service", Dim: true, Key: "l", Chord: "g l"},
		}},
	}
}

func TestRender(t *testing.T) {
	uitest.ForceColor()
	s := uitest.StyleFor("")
	for _, jump := range []int{-1, 0, 2} {
		for _, c := range []struct{ w, h int }{{22, 24}, {8, 3}, {40, 2}} {
			out := Render(s, icons.Unicode, Groups(), "machines", jump, c.w, c.h)
			lines := strings.Split(out, "\n")
			if len(lines) != c.h {
				t.Fatalf("lines %d", len(lines))
			}
			for i, l := range lines {
				if lipgloss.Width(l) != c.w {
					t.Errorf("jump %d %v line %d width %d: %q", jump, c, i, lipgloss.Width(l), ansi.Strip(l))
				}
			}
		}
	}
	p := ansi.Strip(Render(s, icons.Unicode, Groups(), "machines", -1, 22, 8))
	for _, want := range []string{" 1 TAILNET", "▸ ▣ Machines  51  g m ", "  ◫ Dashboard     g h ", " 3 APP"} {
		if !strings.Contains(p, want) {
			t.Errorf("missing %q:\n%s", want, p)
		}
	}
	if strings.Contains(p, "EMPTY") {
		t.Errorf("empty groups must be skipped:\n%s", p)
	}
	if strings.Contains(p, "long title") && strings.Contains(p, "g r") {
		t.Errorf("a row without room drops its chord, not its title:\n%s", p)
	}

	j := ansi.Strip(Render(s, icons.Unicode, Groups(), "machines", 0, 22, 8))
	for _, want := range []string{" 1 TAILNET   pick one ", "  h Dashboard", "▸ m Machines"} {
		if !strings.Contains(j, want) {
			t.Errorf("jump mode missing %q:\n%s", want, j)
		}
	}
	if strings.Contains(j, "g m") {
		t.Errorf("jump mode shows page keys, not chords:\n%s", j)
	}
}

func TestRows(t *testing.T) {
	rows := Rows(Groups())
	want := []Row{{0, -1}, {0, 0}, {0, 1}, {0, 2}, {-1, -1}, {2, -1}, {2, 0}}
	if len(rows) != len(want) {
		t.Fatalf("rows = %v", rows)
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Errorf("row %d = %v, want %v", i, rows[i], want[i])
		}
	}
}
