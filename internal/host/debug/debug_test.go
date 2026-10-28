package debug

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tonhe/headsup/internal/host"
)

var versions = []string{"v029", "v030"}

func fixture(t *testing.T, v, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "debug", v, name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// server serves the fixtures the way headscale does: JSON for routes only
// with Accept: application/json, otherwise text.
func server(t *testing.T, v string) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/registration-cache", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(fixture(t, v, "registration-cache.json"))
	})
	mux.HandleFunc("/debug/routes", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Header.Get("Accept"), "application/json") {
			w.Header().Set("Content-Type", "application/json")
			w.Write(fixture(t, v, "routes.json"))
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		w.Write(fixture(t, v, "routes.txt"))
	})
	mux.HandleFunc("/debug/ping", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		switch r.URL.Query().Get("node") {
		case "2":
			w.Write(fixture(t, v, "ping-ok.html"))
		case "3":
			w.Write(fixture(t, v, "ping-timeout.html"))
		default:
			w.Write(fixture(t, v, "ping-not-connected.html"))
		}
	})
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Write(fixture(t, v, "metrics.txt"))
	})
	s := httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func TestAgainstFixtures(t *testing.T) {
	for _, v := range versions {
		t.Run(v, func(t *testing.T) {
			s := server(t, v)
			c := New(strings.TrimPrefix(s.URL, "http://"))
			ctx := context.Background()
			if !c.Available() {
				t.Fatal("not available")
			}

			_, err := c.RegistrationCache(ctx)
			if !errors.Is(err, host.ErrUnsupportedShape) {
				t.Fatalf("registration cache err = %v, want ErrUnsupportedShape (counters only)", err)
			}
			sum, err := c.RegistrationCacheSummary(ctx)
			if err != nil || sum.CurrentLen != 2 || sum.Type != "expirable-lru" {
				t.Fatalf("summary %+v %v", sum, err)
			}

			routes, err := c.Routes(ctx)
			if err != nil {
				t.Fatal(err)
			}
			want := []host.RouteState{
				{Prefix: netip.MustParsePrefix("0.0.0.0/0"), Candidates: []uint64{4}},
				{Prefix: netip.MustParsePrefix("10.0.0.0/24"), Primary: 1, Candidates: []uint64{1, 2}},
				{Prefix: netip.MustParsePrefix("192.168.50.0/24"), Primary: 2, Candidates: []uint64{2}},
				{Prefix: netip.MustParsePrefix("::/0"), Candidates: []uint64{4}},
			}
			if len(routes) != len(want) {
				t.Fatalf("routes %+v", routes)
			}
			for i := range want {
				if routes[i].Prefix != want[i].Prefix || routes[i].Primary != want[i].Primary || !slices.Equal(routes[i].Candidates, want[i].Candidates) {
					t.Errorf("route %d = %+v, want %+v", i, routes[i], want[i])
				}
			}

			d, err := c.Ping(ctx, 2)
			if err != nil || d != 23*time.Millisecond {
				t.Fatalf("ping = %v %v", d, err)
			}
			if _, err := c.Ping(ctx, 3); !errors.Is(err, ErrPingFailed) {
				t.Fatalf("timeout ping err = %v", err)
			}
			if _, err := c.Ping(ctx, 9); !errors.Is(err, ErrPingFailed) || !strings.Contains(err.Error(), "not connected") {
				t.Fatalf("not connected err = %v", err)
			}

			m, err := c.Metrics(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if m.Nodes != 6 || m.MapResponsesTotal != 1334 || m.HeapBytes != 24502272 || m.Goroutines != 87 || m.At.IsZero() {
				t.Fatalf("metrics %+v", m)
			}
		})
	}
}

func TestRoutesText(t *testing.T) {
	r, err := ParseRoutes(fixture(t, "v030", "routes.txt"), "text/plain")
	if err != nil || len(r) != 2 || r[0].Primary != 1 {
		t.Fatalf("%+v %v", r, err)
	}
	if r, err := ParseRoutes(nil, "text/plain"); err != nil || len(r) != 0 {
		t.Fatalf("empty: %+v %v", r, err)
	}
}

func TestRegistrationShapes(t *testing.T) {
	tests := []struct {
		name string
		in   string
		ids  []string
		err  bool
	}{
		{"array", `[{"auth_id":"abc","hostname":"laptop","requested_at":"2026-10-28T12:00:00Z"}]`, []string{"abc"}, false},
		{"entries map", `{"entries":{"k1":{"node":{"hostname":"nas"},"created_at":"2026-10-28T11:00:00Z"},"k2":{"hostname":"pi","created_at":"2026-10-28T12:00:00Z"}}}`, []string{"k1", "k2"}, false},
		{"summary", `{"current_len":0,"type":"expirable-lru"}`, nil, true},
		{"html", `<!DOCTYPE html><html></html>`, nil, true},
		{"array of junk", `[1,2]`, nil, true},
		{"string", `"x"`, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseRegistrationCache([]byte(tt.in), "")
			if tt.err {
				if !errors.Is(err, host.ErrUnsupportedShape) {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var ids []string
			for _, e := range got {
				ids = append(ids, e.AuthID)
				if e.Hostname == "" {
					t.Errorf("no hostname in %+v", e)
				}
			}
			if !slices.Equal(ids, tt.ids) {
				t.Fatalf("ids %v want %v", ids, tt.ids)
			}
		})
	}
}

func TestUnsupportedShapes(t *testing.T) {
	junk := [][]byte{
		nil, []byte("{"), []byte("<html><body>hi</body></html>"), []byte(`{"primary_routes":{"nope":1}}`),
		[]byte(`{"primary_routes":{"10.0.0.0/8":"x"}}`), []byte(`{"available_routes":{"a":["10.0.0.0/8"]},"primary_routes":{}}`),
		[]byte("garbage line"), []byte(`{"foo":1}`), []byte("\x00\xff"),
	}
	for _, b := range junk {
		if _, err := ParseRoutes(b, ""); len(b) > 0 && !errors.Is(err, host.ErrUnsupportedShape) {
			t.Errorf("routes %q err = %v", b, err)
		}
		if _, err := ParseRegistrationCache(b, ""); !errors.Is(err, host.ErrUnsupportedShape) {
			t.Errorf("registration %q err = %v", b, err)
		}
		if _, err := ParsePing(b, 1); err == nil {
			t.Errorf("ping %q accepted", b)
		}
	}
	if _, err := ParseMetrics([]byte("not metrics at all {{{")); !errors.Is(err, host.ErrUnsupportedShape) {
		t.Errorf("metrics err = %v", err)
	}
	if _, err := ParseMetrics([]byte("other_metric 1\n")); !errors.Is(err, host.ErrUnsupportedShape) {
		t.Errorf("metrics without series err = %v", err)
	}
}

func TestNotServedAndUnavailable(t *testing.T) {
	s := httptest.NewServer(http.NotFoundHandler())
	defer s.Close()
	c := New(strings.TrimPrefix(s.URL, "http://"))
	if c.Available() {
		t.Error("available without /metrics")
	}
	if _, err := c.Routes(context.Background()); !errors.Is(err, host.ErrUnsupportedShape) {
		t.Errorf("routes err = %v", err)
	}
	if _, err := c.Ping(context.Background(), 1); !errors.Is(err, host.ErrUnsupportedShape) {
		t.Errorf("ping err = %v", err)
	}
	if New("").Available() {
		t.Error("empty address available")
	}
	if New("0.0.0.0:9090").base != "http://127.0.0.1:9090" || New("[::]:9090").base != "http://[::1]:9090" {
		t.Error("wildcard not mapped to loopback")
	}
}

func FuzzParsers(f *testing.F) {
	f.Add([]byte(`{"available_routes":{"1":["10.0.0.0/24"]},"primary_routes":{"10.0.0.0/24":1}}`))
	f.Add([]byte(`[{"auth_id":"a"}]`))
	f.Add([]byte("Node 1 responded in 5ms"))
	f.Fuzz(func(t *testing.T, b []byte) {
		_, _ = ParseRoutes(b, "")
		_, _ = ParseRegistrationCache(b, "")
		_, _ = ParsePing(b, 1)
		_, _ = ParseMetrics(b)
		_, _ = ParseCacheSummary(b, "")
	})
}
