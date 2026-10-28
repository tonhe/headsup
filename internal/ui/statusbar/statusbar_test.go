package statusbar

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/tonhe/fathom/internal/module"
	"github.com/tonhe/fathom/internal/ui/uitest"
)

func TestRender(t *testing.T) {
	uitest.ForceColor()
	s := uitest.StyleFor("")
	hints := []module.Hint{{Key: "/", Desc: "filter"}, {Key: "enter", Desc: "open"}, {Key: "r", Desc: "rename"}, {Key: "t", Desc: "tags"}, {Key: "e", Desc: "expire"}, {Key: ":", Desc: "palette"}, {Key: "?", Desc: "help"}}
	cases := []struct {
		w       int
		has     []string
		missing []string
	}{
		{80, []string{"/ filter", "? help", "ro"}, nil},
		{30, []string{"/ filter"}, []string{"? help"}},
		{2, nil, []string{"filter"}},
	}
	for _, c := range cases {
		out := Render(s, hints, "ro", c.w)
		if lipgloss.Width(out) != c.w {
			t.Errorf("w=%d width %d", c.w, lipgloss.Width(out))
		}
		p := ansi.Strip(out)
		for _, h := range c.has {
			if !strings.Contains(p, h) {
				t.Errorf("w=%d missing %q in %q", c.w, h, p)
			}
		}
		for _, h := range c.missing {
			if strings.Contains(p, h) {
				t.Errorf("w=%d unexpected %q", c.w, h)
			}
		}
	}
}
