//go:build integration

package integration

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/host/debug"
)

func itoa(n int) string { return strconv.Itoa(n) }

// TestShapeDrift fetches /debug/* and /metrics from this headscale and runs
// every parser on them: ErrUnsupportedShape means headscale changed a format
// headsup relies on. With HEADSUP_RECORD_FIXTURES=DIR the raw bodies are
// saved to DIR/<v029|v030> for comparing with testdata/debug (whose values
// the unit tests assert, so they are not overwritten).
func TestShapeDrift(t *testing.T) {
	_, id := joinedNode(t)
	// A node that is registered but not connected, for the "not connected" ping.
	off := S.startClient(t, "drift-off", reusableKey(t))
	offID := waitNode(t, "drift-off", 60*time.Second).ID
	off.stop(t)

	type fetch struct {
		file, path, accept string
		parse              func(b []byte, ct string) error
	}
	fetches := []fetch{
		{"registration-cache.json", "/debug/registration-cache", "application/json", func(b []byte, ct string) error {
			_, err := debug.ParseCacheSummary(b, ct)
			return err
		}},
		{"routes.json", "/debug/routes", "application/json", func(b []byte, ct string) error {
			_, err := debug.ParseRoutes(b, ct)
			return err
		}},
		{"routes.txt", "/debug/routes", "", func(b []byte, ct string) error {
			_, err := debug.ParseRoutes(b, ct)
			return err
		}},
		{"ping-ok.html", "/debug/ping?node=" + itoa(int(id)), "", func(b []byte, _ string) error {
			_, err := debug.ParsePing(b, id)
			return err
		}},
		{"ping-not-connected.html", "/debug/ping?node=" + itoa(int(offID)), "", func(b []byte, _ string) error {
			_, err := debug.ParsePing(b, offID)
			if errors.Is(err, host.ErrUnsupportedShape) {
				return err
			}
			if err == nil {
				return errf("an offline node should not answer")
			}
			return nil
		}},
		{"metrics.txt", "/metrics", "", func(b []byte, _ string) error {
			s, err := debug.ParseMetrics(b)
			if err == nil && s.Goroutines == 0 {
				return errf("metrics parsed but no go_goroutines")
			}
			return err
		}},
	}
	recordDir := os.Getenv("HEADSUP_RECORD_FIXTURES")
	for _, f := range fetches {
		t.Run(f.file, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, S.debugURL(f.path), nil)
			if f.accept != "" {
				req.Header.Set("Accept", f.accept)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != 200 {
				t.Fatalf("%s: HTTP %d: %s", f.path, resp.StatusCode, b)
			}
			if err := f.parse(b, resp.Header.Get("Content-Type")); err != nil {
				t.Errorf("%s on %s: %v (shape drift?)", f.path, S.image, err)
			}
			if recordDir != "" {
				p := filepath.Join(recordDir, S.series, f.file)
				_ = os.MkdirAll(filepath.Dir(p), 0o755)
				if err := os.WriteFile(p, b, 0o644); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
	// The registration cache's entries are not exposed by released versions;
	// if that changes, the pending queue can auto-fill.
	resp, err := http.Get(S.debugURL("/debug/registration-cache?format=json"))
	if err == nil {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if _, err := debug.ParseRegistrationCache(b, resp.Header.Get("Content-Type")); err == nil {
			t.Logf("%s now lists registration-cache entries", S.image)
		}
	}
}
