package themepicker

import (
	"path/filepath"
	"testing"

	"github.com/tonhe/headsup/internal/theme"
)

// TestShippedThemes (T125) loads the example theme files in themes/ the way
// a user's themes directory is loaded: both must parse cleanly, and the
// accessibility theme must clear WCAG AAA (7:1) for text and muted text.
func TestShippedThemes(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "themes")
	ts, warns := theme.LoadUserThemes(dir)
	if len(warns) > 0 {
		t.Errorf("warnings: %v", warns)
	}
	got := map[string]theme.Theme{}
	for _, th := range ts {
		got[th.Slug] = th
	}
	for _, slug := range []string{"midnight-harbor", "high-contrast"} {
		if _, ok := got[slug]; !ok {
			t.Fatalf("%s not shipped in themes/ (got %v)", slug, ts)
		}
	}
	if mh := got["midnight-harbor"]; mh.Name != "Midnight Harbor" || mh.Light {
		t.Errorf("midnight harbor metadata: %+v", mh)
	}
	r := got["high-contrast"].Roles()
	for name, c := range map[string]float64{
		"fg":      theme.Contrast(r.Fg, r.Bg),
		"muted":   theme.Contrast(r.Muted, r.Bg),
		"accent":  theme.Contrast(r.Accent, r.Bg),
		"danger":  theme.Contrast(r.Danger, r.Bg),
		"sel fg":  theme.Contrast(r.FgStrong, r.Selection),
		"pending": theme.Contrast(r.Pending, r.Bg),
		"online":  theme.Contrast(r.Online, r.Bg),
		"offline": theme.Contrast(r.Offline, r.Bg),
		"expiry":  theme.Contrast(r.Expiring, r.Bg),
		"ip":      theme.Contrast(r.IP, r.Bg),
		"tag":     theme.Contrast(r.TagFg, r.TagBg),
	} {
		if c < 7 {
			t.Errorf("high-contrast %s contrast %.1f:1 < 7:1", name, c)
		}
	}
}
