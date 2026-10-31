//go:build integration

package integration

import (
	"os"
	"strings"
	"testing"
	"time"
)

// TestExplore prints screens for a key sequence, for writing scenarios:
// EXPLORE="1 m r" [EXPLORE_HOST=1] [EXPLORE_CLIENT=1] [EXPLORE_WEB=1].
func TestExplore(t *testing.T) {
	if os.Getenv("EXPLORE") == "" {
		t.Skip("set EXPLORE to a key sequence")
	}
	if os.Getenv("EXPLORE_CLIENT") != "" {
		joinedNode(t)
	}
	if os.Getenv("EXPLORE_WEB") != "" {
		S.startClient(t, "web-1", "").loginURL(t)
	}
	var d *tui
	if os.Getenv("EXPLORE_HOST") != "" {
		d, _ = hostTUI(t, 120, 40)
	} else {
		d = remoteTUI(t, 120, 40, appOpts{})
	}
	time.Sleep(2 * time.Second)
	t.Log("\n" + d.Screen())
	for _, k := range strings.Fields(os.Getenv("EXPLORE")) {
		switch k {
		case "_":
			k = "space"
		case "~":
			time.Sleep(3 * time.Second)
			continue
		}
		d.Key(k)
		time.Sleep(700 * time.Millisecond)
		t.Logf("after %q:\n%s", k, d.Screen())
	}
}
