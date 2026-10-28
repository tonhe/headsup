package pane

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/tonhe/headsup/internal/ui/uitest"
)

func TestRenderSize(t *testing.T) {
	uitest.ForceColor()
	s := uitest.StyleFor("")
	long := strings.Repeat("wide 漢字 ", 30)
	cases := []struct {
		name        string
		title, body string
		w, h        int
		opts        []Opt
	}{
		{"basic", "Machines", "a\nb", 40, 6, nil},
		{"extra+right", "Machines", "x", 80, 24, []Opt{WithExtra("51"), WithRight("filter: tag:x")}},
		{"clip", "T", long + "\n" + long, 30, 3, nil},
		{"tiny", strings.Repeat("t", 50), "", 8, 3, []Opt{WithRight("r"), WithBottom("b")}},
		{"nopad", "", "body", 10, 4, []Opt{NoPad()}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := Render(s, c.title, c.body, c.w, c.h, true, c.opts...)
			lines := strings.Split(out, "\n")
			if len(lines) != c.h {
				t.Fatalf("got %d lines want %d", len(lines), c.h)
			}
			for i, l := range lines {
				if w := lipgloss.Width(l); w != c.w {
					t.Errorf("line %d width %d want %d: %q", i, w, c.w, ansi.Strip(l))
				}
			}
		})
	}
}

func TestTitleInBorder(t *testing.T) {
	s := uitest.StyleFor("")
	out := ansi.Strip(Render(s, "Machines", "", 40, 3, false, WithExtra("51"), WithRight("r")))
	first := strings.Split(out, "\n")[0]
	if !strings.HasPrefix(first, "╭ Machines ─ 51 ") || !strings.HasSuffix(first, " r ─╮") {
		t.Fatalf("top edge = %q", first)
	}
}
