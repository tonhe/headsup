package qr

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestRender(t *testing.T) {
	url := "https://hs.example.com/register/mkey:0123456789abcdef"
	out, err := Render(url)
	if err != nil {
		t.Fatal(err)
	}
	w, h, _ := Size(url)
	lines := strings.Split(out, "\n")
	if len(lines) != h || lipgloss.Width(out) != w {
		t.Fatalf("size %dx%d, got %dx%d", w, h, lipgloss.Width(out), len(lines))
	}
	cases := []struct {
		w, h int
		want bool
	}{{80, 24, true}, {w, h, true}, {w - 1, h, false}, {w, h - 1, false}}
	for _, c := range cases {
		if got := Fits(url, c.w, c.h); got != c.want {
			t.Errorf("Fits(%d,%d)=%v", c.w, c.h, got)
		}
	}
	if Fits(strings.Repeat("x", 5000), 500, 500) {
		t.Error("oversized content should not fit")
	}
}
