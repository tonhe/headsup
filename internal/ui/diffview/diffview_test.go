package diffview

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/tonhe/headsup/internal/ui/uitest"
)

func sample() []Line {
	var ls []Line
	o, n := 0, 0
	for i := range 20 {
		o++
		n++
		ls = append(ls, Line{Op: ' ', Text: "ctx" + string(rune('a'+i)), Old: o, New: n})
		if i == 10 {
			n++
			ls = append(ls, Line{Op: '+', Text: "\tadded", New: n})
			o++
			ls = append(ls, Line{Op: '-', Text: "removed", Old: o})
		}
	}
	return ls
}

func TestRenderOneHunk(t *testing.T) {
	uitest.ForceColor()
	s := uitest.StyleFor("tokyo-night")
	ls := sample()
	if Rows(ls, 3) != 1+3+2+3 {
		t.Fatalf("rows = %d", Rows(ls, 3))
	}
	out := Render(s, ls, 40, 12, Options{})
	plain := ansi.Strip(out)
	if strings.Count(plain, "@@ -") != 1 {
		t.Fatalf("want one hunk header:\n%s", plain)
	}
	for _, want := range []string{"@@ -9,7 +9,7 @@", "+    added", "-removed", " ctxk"} {
		if !strings.Contains(plain, want) {
			t.Errorf("missing %q:\n%s", want, plain)
		}
	}
	if uitest.MaxWidth(out) != 40 || len(uitest.Lines(out)) != 12 {
		t.Errorf("size %dx%d", uitest.MaxWidth(out), len(uitest.Lines(out)))
	}
	if Summary(ls) != "+1 −1" {
		t.Errorf("summary %q", Summary(ls))
	}
	// Whole-file mode has no gaps; scrolling clamps.
	full := ansi.Strip(Render(s, ls, 40, 5, Options{Context: -1, Offset: 1000}))
	if !strings.Contains(full, "ctxt") {
		t.Errorf("scrolled to the end should show the last line:\n%s", full)
	}
}

func TestRenderEdgeCases(t *testing.T) {
	s := uitest.StyleFor("")
	if Render(s, nil, 0, 5, Options{}) != "" {
		t.Error("zero width renders nothing")
	}
	out := ansi.Strip(Render(s, []Line{{Op: ' ', Text: "x", Old: 1, New: 1}}, 20, 3, Options{}))
	if !strings.Contains(out, "No changes.") || len(uitest.Lines(out)) != 3 {
		t.Errorf("no-change render:\n%q", out)
	}
	two := []Line{{Op: '-', Text: "a", Old: 1}}
	for i := 2; i < 30; i++ {
		two = append(two, Line{Op: ' ', Text: "x", Old: i, New: i - 1})
	}
	two = append(two, Line{Op: '+', Text: "z", New: 29})
	if got := strings.Count(ansi.Strip(Render(s, two, 30, 40, Options{Context: 2})), "@@ -"); got != 2 {
		t.Errorf("far-apart changes: %d hunks", got)
	}
}
