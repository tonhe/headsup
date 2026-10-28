package badge

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/tonhe/fathom/internal/ui/uitest"
)

func TestTags(t *testing.T) {
	uitest.ForceColor()
	s := uitest.StyleFor("")
	tags := []string{"tag:server", "tag:db", "tag:prod", "tag:eu-west"}
	cases := []struct {
		w    int
		want string
	}{
		{0, " tag:server   tag:db   tag:prod   tag:eu-west "},
		{30, " tag:server   tag:db  +2"},
		{4, "+4"},
	}
	for _, c := range cases {
		out := Tags(s, tags, c.w)
		if got := ansi.Strip(out); got != c.want {
			t.Errorf("w=%d got %q want %q", c.w, got, c.want)
		}
		if c.w > 0 && lipgloss.Width(out) > c.w {
			t.Errorf("w=%d too wide", c.w)
		}
	}
	if !strings.Contains(ansi.Strip(Render(s, "3")), "3") {
		t.Error("badge")
	}
}
