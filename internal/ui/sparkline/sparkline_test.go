package sparkline

import (
	"math"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/tonhe/fathom/internal/ui/uitest"
)

func TestRender(t *testing.T) {
	uitest.ForceColor()
	s := uitest.StyleFor("")
	cases := []struct {
		vals  []float64
		w     int
		ascii bool
		want  string
	}{
		{[]float64{0, 1, 2, 3, 4, 5, 6, 7}, 8, false, "▁▂▃▄▅▆▇█"},
		{[]float64{0, 7}, 4, true, "  _#"},
		{[]float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, 3, false, "▇██"},
		{[]float64{5, math.NaN(), 0}, 3, false, "█ ▁"},
		{nil, 3, false, "   "},
		{[]float64{0, 0}, 2, false, "▁▁"},
	}
	for _, c := range cases {
		out := Render(s, c.vals, c.w, c.ascii)
		if got := ansi.Strip(out); got != c.want {
			t.Errorf("%v got %q want %q", c.vals, got, c.want)
		}
		if lipgloss.Width(out) != c.w {
			t.Errorf("width %d", lipgloss.Width(out))
		}
	}
}
