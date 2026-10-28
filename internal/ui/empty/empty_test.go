package empty

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/tonhe/headsup/internal/ui/uitest"
)

func TestRender(t *testing.T) {
	uitest.ForceColor()
	s := uitest.StyleFor("")
	for _, c := range []struct{ w, h int }{{80, 24}, {20, 3}, {6, 2}} {
		out := Render(s, "No machines", "Register one with a pre-auth key", "n", c.w, c.h)
		lines := strings.Split(out, "\n")
		if len(lines) != c.h {
			t.Fatalf("%v: %d lines", c, len(lines))
		}
		for i, l := range lines {
			if lipgloss.Width(l) != c.w {
				t.Errorf("%v line %d width %d", c, i, lipgloss.Width(l))
			}
		}
	}
	if !strings.Contains(ansi.Strip(Render(s, "No machines", "hint", "n", 80, 24)), "press n") {
		t.Error("key missing")
	}
}
