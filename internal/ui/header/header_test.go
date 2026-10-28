package header

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/tonhe/fathom/internal/ui/uitest"
)

func TestRender(t *testing.T) {
	uitest.ForceColor()
	s := uitest.StyleFor("")
	h := Header{App: "fathom", Profile: "prod", ModeLabel: "host:hs01", Version: "v0.29.6", Healthy: true, Online: 42, Total: 51, Stale: true}
	for _, w := range []int{1, 3, 10, 30, 80, 200} {
		out := Render(s, h, w)
		if strings.Contains(out, "\n") || lipgloss.Width(out) != w {
			t.Errorf("w=%d width %d: %q", w, lipgloss.Width(out), ansi.Strip(out))
		}
	}
	full := ansi.Strip(Render(s, h, 120))
	for _, want := range []string{"fathom", "prod (host:hs01)", "headscale v0.29.6", "stale", "42 online / 51"} {
		if !strings.Contains(full, want) {
			t.Errorf("missing %q in %q", want, full)
		}
	}
}
