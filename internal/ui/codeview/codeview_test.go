package codeview

import (
	"strings"
	"testing"

	"github.com/alecthomas/chroma/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/tonhe/headsup/internal/theme"
	"github.com/tonhe/headsup/internal/ui/uitest"
)

const src = "// comment\n{\n  \"groups\": {\"group:a\": [\"x@\"]},\n  \"n\": 3, \"ok\": true,\n}\n"

func TestHighlightKeepsTextAndLines(t *testing.T) {
	uitest.ForceColor()
	s := uitest.StyleFor("tokyo-night")
	lines := Highlight(NewPalette(s.Theme), "json", src)
	want := strings.Split(strings.TrimSuffix(src, "\n"), "\n")
	if len(lines) != len(want) {
		t.Fatalf("%d lines, want %d", len(lines), len(want))
	}
	for i := range want {
		if ansi.Strip(lines[i]) != want[i] {
			t.Errorf("line %d = %q, want %q", i+1, ansi.Strip(lines[i]), want[i])
		}
	}
	if !strings.Contains(lines[0], "\x1b[") {
		t.Error("expected colour escapes in highlighted output")
	}
	// Unknown language falls back without losing text.
	if got := Highlight(NewPalette(s.Theme), "no-such-lang", "a\tb\n"); ansi.Strip(got[0]) != "a    b" {
		t.Errorf("fallback: %q", got)
	}
}

func TestPaletteFollowsTheme(t *testing.T) {
	th := *theme.ByName("tokyo-night")
	p := NewPalette(th)
	if p.Style(chroma.LiteralStringDouble).GetForeground() != th.Base0B {
		t.Error("strings should use base0B")
	}
	if p.Style(chroma.CommentSingle).GetForeground() != th.Base03 {
		t.Error("comments should use base03")
	}
	if p.Style(chroma.NameTag).GetForeground() != th.Base0D {
		t.Error("keys should use base0D")
	}
	if p.Style(chroma.TokenType(-12345)).GetForeground() != th.Base05 {
		t.Error("unknown tokens use the default foreground")
	}
}

func TestRenderGutterAndMarker(t *testing.T) {
	s := uitest.StyleFor("tokyo-night")
	var v View
	v.Set(s, "json", src)
	if v.Lines() != 5 {
		t.Fatalf("lines = %d", v.Lines())
	}
	out := v.Render(s, 30, 6, Options{ErrLine: 3, Cursor: 4})
	plain := uitest.Lines(ansi.Strip(out))
	if len(plain) != 6 || uitest.MaxWidth(out) != 30 {
		t.Fatalf("size %dx%d", uitest.MaxWidth(out), len(plain))
	}
	if !strings.HasPrefix(plain[2], "▶ 3 │") {
		t.Errorf("error line: %q", plain[2])
	}
	if !strings.HasPrefix(plain[3], "› 4 │") {
		t.Errorf("cursor line: %q", plain[3])
	}
	if !strings.HasPrefix(plain[0], "  1 │ // comment") {
		t.Errorf("first line: %q", plain[0])
	}
	if strings.TrimSpace(plain[5]) != "" {
		t.Errorf("padding row: %q", plain[5])
	}
	// Scrolling clamps; no-number mode still marks the error.
	out = ansi.Strip(v.Render(s, 30, 2, Options{Offset: 99, NoNumbers: true, ErrLine: 5, Marker: "!"}))
	if !strings.HasPrefix(uitest.Lines(out)[1], "! }") {
		t.Errorf("no-number error marker:\n%s", out)
	}
	if Render(s, nil, 0, 0, Options{}) != "" {
		t.Error("empty area")
	}
	if ClampOffset(-3, 10, 4) != 0 || ClampOffset(50, 10, 4) != 6 {
		t.Error("ClampOffset")
	}
	// Set is cached.
	before := v.lines
	v.Set(s, "json", src)
	if &before[0] != &v.lines[0] {
		t.Error("Set should not re-highlight unchanged text")
	}
}
