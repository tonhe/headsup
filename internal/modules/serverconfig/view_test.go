package serverconfig

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	hostfake "github.com/tonhe/headsup/internal/host/fake"
	"github.com/tonhe/headsup/internal/ui/toast"
	"github.com/tonhe/headsup/internal/ui/uitest"
)

func checkFrame(t *testing.T, out string, w, h int) {
	t.Helper()
	lines := strings.Split(out, "\n")
	if len(lines) > h {
		t.Errorf("frame has %d lines, terminal is %d", len(lines), h)
	}
	for i, l := range lines {
		if lw := lipgloss.Width(l); lw > w {
			t.Errorf("line %d is %d wide, terminal is %d: %q", i, lw, w, l)
		}
	}
}

// dismissToasts clears toasts so snapshots show the module itself.
func (d *driver) dismissToasts() {
	for id := 0; id < 64; id++ {
		d.send(toast.DismissMsg{ID: id})
	}
}

// screens drives the module into each of its states.
var screens = []struct {
	name string
	run  func(d *driver)
}{
	{"general", func(d *driver) {}},
	{"nodes_edited", func(d *driver) { d.editField("Nodes", "node.expiry", "180d") }},
	{"dns_link", func(d *driver) { d.gotoSection("DNS") }},
	{"database_view", func(d *driver) { d.gotoSection("Database") }},
	{"review", func(d *driver) { d.editField("Nodes", "node.expiry", "180d"); d.key("ctrl+s") }},
	{"restart_offer", func(d *driver) { d.editField("Nodes", "node.expiry", "180d"); d.key("ctrl+s", "ctrl+s", "y") }},
	{"unhealthy", func(d *driver) {
		d.editField("Nodes", "node.expiry", "180d")
		d.key("ctrl+s", "ctrl+s", "y")
		d.h.SetUnhealthyRestart(true)
		d.key("X", "y")
	}},
}

// TestGoldenScreens snapshots each state at both reference sizes and in the
// ASCII icon set (constitution V).
func TestGoldenScreens(t *testing.T) {
	for _, sz := range [][2]int{{80, 24}, {120, 40}} {
		for _, ic := range []string{"unicode", "ascii"} {
			for _, sc := range screens {
				t.Run(fmt.Sprintf("%dx%d/%s/%s", sz[0], sz[1], ic, sc.name), func(t *testing.T) {
					h := hostfake.New(hostfake.WithClock(func() time.Time { return flowNow }))
					d := newSizedDriver(t, h, false, sz[0], sz[1], ic)
					sc.run(d)
					d.dismissToasts()
					out := d.m.View()
					checkFrame(t, out, sz[0], sz[1])
					uitest.Golden(t, out)
				})
			}
		}
	}
}
