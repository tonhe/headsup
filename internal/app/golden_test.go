package app

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/tonhe/headsup/internal/api/fake"
	"github.com/tonhe/headsup/internal/icons"
	"github.com/tonhe/headsup/internal/theme"
	"github.com/tonhe/headsup/internal/ui/uitest"
)

var goldenNow = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

func goldenDriver(t *testing.T, w, h int, themeSlug, icons string) *driver {
	clock := func() time.Time { return goldenNow }
	return newDriver(t, w, h, Options{
		Backend:   fake.Demo(fake.WithClock(clock)),
		Now:       clock,
		Theme:     themeSlug,
		Icons:     icons,
		ServerURL: "https://hs.example.com",
	})
}

// TestGoldenScreens snapshots every MVP screen at both reference sizes, in
// the ASCII icon set, in 16 colours and under NO_COLOR (constitution V,
// SC-006, T171).
func TestGoldenScreens(t *testing.T) {
	for _, sz := range [][2]int{{80, 24}, {120, 40}} {
		// Tailnet section jumps: Dashboard, Machines, Add device.
		for i, key := range []string{"1 h", "1 m", "1 p"} {
			t.Run(fmt.Sprintf("%dx%d/module%d", sz[0], sz[1], i+1), func(t *testing.T) {
				uitest.Screen(t, func(t *testing.T, ic icons.Set) string {
					d := goldenDriver(t, sz[0], sz[1], "tokyo-night", ic.Name)
					d.key(strings.Fields(key)...)
					return d.view()
				})
			})
		}
	}
}

// TestGoldenJumpMode snapshots a pending section jump (sidebar and tab-bar
// layouts): the highlighted section, its page keys and the which-key line.
func TestGoldenJumpMode(t *testing.T) {
	for _, sz := range [][2]int{{80, 24}, {120, 40}} {
		t.Run(fmt.Sprintf("%dx%d", sz[0], sz[1]), func(t *testing.T) {
			uitest.Screen(t, func(t *testing.T, ic icons.Set) string {
				d := goldenDriver(t, sz[0], sz[1], "tokyo-night", ic.Name)
				d.key("1")
				return d.view()
			})
		})
	}
}

// TestAllThemesSmoke renders every screen in every bundled theme at 80x24 and
// checks it fits and never panics (SC-006: themes beyond the golden set).
func TestAllThemesSmoke(t *testing.T) {
	for _, th := range theme.All() {
		t.Run(th.Slug, func(t *testing.T) {
			d := goldenDriver(t, 80, 24, th.Slug, "unicode")
			for _, key := range []string{"1 h", "1 m", "1 p"} {
				d.key(strings.Fields(key)...)
				out := d.view()
				checkFrame(t, out, 80, 24)
				if strings.TrimSpace(out) == "" {
					t.Fatalf("module %s rendered nothing", key)
				}
			}
		})
	}
}
