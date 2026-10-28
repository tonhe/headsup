package theme

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func TestProfileFor(t *testing.T) {
	tests := []struct {
		name   string
		env    map[string]string
		detect termenv.Profile
		want   termenv.Profile
	}{
		{"no color wins", map[string]string{"NO_COLOR": "1"}, termenv.TrueColor, termenv.Ascii},
		{"truecolor", nil, termenv.TrueColor, termenv.TrueColor},
		{"256", nil, termenv.ANSI256, termenv.ANSI256},
		{"16", nil, termenv.ANSI, termenv.ANSI},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ProfileFor(func(k string) string { return tt.env[k] }, func() termenv.Profile { return tt.detect })
			if got != tt.want {
				t.Fatalf("got %v want %v", got, tt.want)
			}
		})
	}
}

func TestApplyProfileDownsamples(t *testing.T) {
	old := lipgloss.ColorProfile()
	defer lipgloss.SetColorProfile(old)

	c := TokyoNight.Base08
	for _, tt := range []struct {
		p    termenv.Profile
		want string // substring of the SGR sequence
	}{
		{termenv.TrueColor, "38;2;"},
		{termenv.ANSI256, "38;5;"},
		{termenv.ANSI, "\x1b["},
	} {
		ApplyProfile(tt.p)
		if lipgloss.ColorProfile() != tt.p {
			t.Fatalf("profile not applied")
		}
		out := lipgloss.NewStyle().Foreground(c).Render("x")
		if !strings.Contains(out, tt.want) || (tt.p == termenv.ANSI && strings.Contains(out, "38;")) {
			t.Errorf("profile %v: %q lacks %q", tt.p, out, tt.want)
		}
	}
	ApplyProfile(termenv.Ascii)
	if out := lipgloss.NewStyle().Foreground(c).Render("x"); out != "x" {
		t.Errorf("ascii output %q", out)
	}
}
