package badge

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/tonhe/headsup/internal/icons"
	"github.com/tonhe/headsup/internal/ui/uitest"
)

func TestServicePhase(t *testing.T) {
	cases := []struct {
		state   string
		running bool
		busy    string
		want    ServicePhase
		glyph   string
	}{
		{"active", true, "", ServiceRunning, "●"},
		{"running", true, "", ServiceRunning, "●"},
		{"inactive", false, "", ServiceStopped, "○"},
		{"exited", false, "", ServiceStopped, "○"},
		{"failed", false, "", ServiceFailed, "○"},
		{"activating", false, "", ServiceTransition, "◐"},
		{"active", true, "restarting", ServiceTransition, "◐"},
		{"unknown", false, "", ServiceUnknown, "◌"},
		{"", false, "", ServiceUnknown, "◌"},
	}
	for _, c := range cases {
		p := ServicePhaseOf(c.state, c.running, c.busy)
		if p != c.want {
			t.Errorf("%q/%v/%q: got %v, want %v", c.state, c.running, c.busy, p, c.want)
		}
		if g := ServiceGlyph(icons.Unicode, p); g != c.glyph {
			t.Errorf("%v glyph %q, want %q", p, g, c.glyph)
		}
	}
	if ServiceGlyph(icons.ASCII, ServiceRunning) != "*" {
		t.Error("ascii running glyph")
	}
}

func TestServiceRender(t *testing.T) {
	s := uitest.StyleFor("")
	for p := ServiceUnknown; p <= ServiceFailed; p++ {
		got := ansi.Strip(Service(s, icons.Unicode, p, p.String()))
		if !strings.HasSuffix(got, p.String()) || !strings.HasPrefix(got, ServiceGlyph(icons.Unicode, p)) {
			t.Errorf("phase %v rendered %q", p, got)
		}
	}
	if got := ansi.Strip(Service(s, icons.Unicode, ServiceRunning, "")); got != "●" {
		t.Errorf("no label: %q", got)
	}
}
