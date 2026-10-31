package statusbar

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/ui/uitest"
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

func TestWhichKey(t *testing.T) {
	uitest.ForceColor()
	s := uitest.StyleFor("")
	lead := module.Hint{Key: "1", Desc: "Tailnet"}
	items := []module.Hint{{Key: "h", Desc: "Dashboard"}, {Key: "m", Desc: "Machines"}, {Key: "u", Desc: "Users"},
		{Key: "r", Desc: "Routes"}, {Key: "p", Desc: "Add device"}, {Key: "k", Desc: "Keys"}, {Key: "a", Desc: "Access controls"}}
	tail := module.Hint{Key: "esc", Desc: "cancel"}
	for _, c := range []struct {
		w       int
		has     []string
		missing []string
	}{
		{140, []string{"1 Tailnet", "h Dashboard", "a Access controls", "esc cancel"}, nil},
		{100, []string{"h Dashboard", "m Machines", "p Add device", "a Access controls", "esc"}, []string{"cancel"}},
		{80, []string{"1 Tailnet", "h Dashb…", "m Machi…", "u Users", "r Routes", "k Keys", "a Access…", "esc"}, []string{"Access controls", "cancel"}},
		{40, []string{"1 Tailnet", "h", "a", "esc"}, []string{"Dashboard"}},
		{20, nil, []string{"Dashboard"}},
		{0, nil, nil},
	} {
		out := WhichKey(s, lead, items, tail, "…", c.w)
		if lipgloss.Width(out) != c.w {
			t.Errorf("w=%d width %d: %q", c.w, lipgloss.Width(out), ansi.Strip(out))
		}
		p := ansi.Strip(out)
		for _, h := range c.has {
			if !strings.Contains(p, h) {
				t.Errorf("w=%d missing %q in %q", c.w, h, p)
			}
		}
		for _, h := range c.missing {
			if strings.Contains(p, h) {
				t.Errorf("w=%d unexpected %q in %q", c.w, h, p)
			}
		}
	}
}
