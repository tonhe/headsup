package kv

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/tonhe/fathom/internal/ui/uitest"
)

func TestRender(t *testing.T) {
	uitest.ForceColor()
	s := uitest.StyleFor("")
	p := Pairs("IPs", "100.64.0.3\nfd7a:115c:a1e0::3", "Routes", strings.Repeat("10.0.0.0/24 ", 20), "Very long key name here", "v")
	for _, w := range []int{80, 30, 5} {
		out := Render(s, p, w)
		if uitest.MaxWidth(out) > w {
			t.Errorf("w=%d width %d", w, uitest.MaxWidth(out))
		}
	}
	lines := strings.Split(ansi.Strip(Render(s, p, 80)), "\n")
	if len(lines) != 4 || !strings.HasPrefix(lines[1], strings.Repeat(" ", 10)) || strings.Index(lines[0], "100") != strings.Index(lines[2], "10.") {
		t.Errorf("alignment:\n%s", strings.Join(lines, "\n"))
	}
}
