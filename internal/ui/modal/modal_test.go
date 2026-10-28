package modal

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/tonhe/fathom/internal/ui/uitest"
)

func TestCenterAndPlace(t *testing.T) {
	uitest.ForceColor()
	s := uitest.StyleFor("")
	base := strings.Repeat(s.Accent.Render("漢字abc")+"0123456789012345678901234567890123456789\n", 30)
	box := s.Modal.Render("hello")
	cases := []struct{ w, h int }{{80, 24}, {20, 5}, {6, 3}, {200, 50}}
	for _, c := range cases {
		out := Center(s, base, box, c.w, c.h)
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
	out := Place("aaaaaaaaaa\nbbbbbbbbbb", "XY", 3, 1, 10, 2)
	if got := ansi.Strip(out); got != "aaaaaaaaaa\nbbbXYbbbbb" {
		t.Fatalf("place = %q", got)
	}
	// Wide char split by the box edge keeps exact width.
	out = Place("漢字漢字漢字", "X", 1, 0, 12, 1)
	if lipgloss.Width(out) != 12 {
		t.Fatalf("wide split width %d", lipgloss.Width(out))
	}
}
