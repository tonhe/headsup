// Package sparkline renders a one-line bar sparkline coloured with the
// theme's spark gradient.
package sparkline

import (
	"math"
	"strings"

	"github.com/tonhe/headsup/internal/ui/styles"
)

var (
	blocks = []string{"▁", "▂", "▃", "▄", "▅", "▆", "▇", "█"}
	asciiB = []string{"_", ".", "-", ":", "=", "+", "*", "#"}
)

// Render draws the most recent width values right-aligned in exactly width
// cells. Values are scaled from min(0, lowest) to the highest; NaN is a gap.
// ascii selects plain ASCII glyphs.
func Render(s *styles.Styles, values []float64, width int, ascii bool) string {
	if width <= 0 {
		return ""
	}
	glyphs := blocks
	if ascii {
		glyphs = asciiB
	}
	if len(values) > width {
		values = values[len(values)-width:]
	}
	lo, hi := 0.0, math.Inf(-1)
	for _, v := range values {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		lo, hi = math.Min(lo, v), math.Max(hi, v)
	}
	var b strings.Builder
	b.WriteString(strings.Repeat(" ", width-len(values)))
	for _, v := range values {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			b.WriteByte(' ')
			continue
		}
		lvl := 0
		if hi > lo {
			lvl = int((v - lo) / (hi - lo) * 7.999)
		}
		lvl = max(0, min(7, lvl))
		b.WriteString(s.Spark[lvl].Render(glyphs[lvl]))
	}
	return b.String()
}
