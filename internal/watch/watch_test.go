package watch

import (
	"net/netip"
	"reflect"
	"testing"
	"time"

	"github.com/tonhe/headsup/internal/api"
	"github.com/tonhe/headsup/internal/store"
)

var t0 = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

func node(id uint64, name string, online bool, routes ...string) api.Node {
	n := api.Node{ID: id, GivenName: name, Online: online}
	for _, r := range routes {
		n.AvailableRoutes = append(n.AvailableRoutes, netip.MustParsePrefix(r))
	}
	return n
}

func st(nodes []api.Node, paks []api.PreAuthKey, apis []api.APIKey) *store.Store {
	s := store.New()
	s.Apply(store.Snapshot{Slices: store.SliceNodes | store.SlicePreAuthKeys | store.SliceAPIKeys,
		Nodes: nodes, PreAuthKeys: paks, APIKeys: apis, Errs: map[store.Slice]error{}})
	return s
}

func kinds(evs []Event) []string {
	var out []string
	for _, e := range evs {
		out = append(out, string(e.Kind)+" "+e.Subject+" "+e.Detail)
	}
	return out
}

func TestDiffer(t *testing.T) {
	exp := func(d time.Duration) *time.Time { x := t0.Add(d); return &x }
	cases := []struct {
		name       string
		before     *store.Store
		after      *store.Store
		afterSlice store.Slice
		at         time.Duration
		want       []string
	}{
		{
			name:   "online",
			before: st([]api.Node{node(1, "nas", false)}, nil, nil),
			after:  st([]api.Node{node(1, "nas", true)}, nil, nil),
			want:   []string{"node_online nas "},
		},
		{
			name:   "offline",
			before: st([]api.Node{node(1, "nas", true), node(2, "pi", true)}, nil, nil),
			after:  st([]api.Node{node(1, "nas", true), node(2, "pi", false)}, nil, nil),
			want:   []string{"node_offline pi "},
		},
		{
			name:   "new node online",
			before: st([]api.Node{node(1, "nas", true)}, nil, nil),
			after:  st([]api.Node{node(1, "nas", true), node(3, "new", true)}, nil, nil),
			want:   []string{"node_online new "},
		},
		{
			name:   "unchanged",
			before: st([]api.Node{node(1, "nas", true, "10.0.0.0/24")}, nil, nil),
			after:  st([]api.Node{node(1, "nas", true, "10.0.0.0/24")}, nil, nil),
		},
		{
			name:   "route advertised",
			before: st([]api.Node{node(1, "gw", true, "10.0.0.0/24")}, nil, nil),
			after:  st([]api.Node{node(1, "gw", true, "10.0.0.0/24", "192.168.1.0/24", "0.0.0.0/0")}, nil, nil),
			want:   []string{"route_advertised gw 0.0.0.0/0", "route_advertised gw 192.168.1.0/24"},
		},
		{
			name:   "pre-auth key enters warning window",
			before: st(nil, []api.PreAuthKey{{ID: 7, Expiration: t0.Add(8 * 24 * time.Hour)}}, nil),
			after:  st(nil, []api.PreAuthKey{{ID: 7, Expiration: t0.Add(8 * 24 * time.Hour)}}, nil),
			at:     2 * 24 * time.Hour,
			want:   []string{"key_expiring pre-auth key 7 expires in 6d"},
		},
		{
			name:   "already expiring stays quiet",
			before: st(nil, nil, []api.APIKey{{ID: 1, Prefix: "abc", Expiration: t0.Add(time.Hour)}}),
			after:  st(nil, nil, []api.APIKey{{ID: 1, Prefix: "abc", Expiration: t0.Add(time.Hour)}}),
			at:     time.Minute,
		},
		{
			name:   "machine key expiring",
			before: st([]api.Node{{ID: 4, GivenName: "lap", Expiry: exp(30 * time.Hour)}}, nil, nil),
			after:  st([]api.Node{{ID: 4, GivenName: "lap", Expiry: exp(5 * time.Hour)}}, nil, nil),
			want:   []string{"key_expiring machine lap expires in 5h"},
		},
		{
			name:   "api key expiring",
			before: st(nil, nil, nil),
			after:  st(nil, nil, []api.APIKey{{ID: 1, Prefix: "abc", Expiration: t0.Add(10 * time.Minute)}}),
			want:   []string{"key_expiring API key abc expires in 10m"},
		},
		{
			name:       "slice not refreshed",
			before:     st([]api.Node{node(1, "nas", true)}, nil, nil),
			after:      st([]api.Node{node(1, "nas", false)}, nil, nil),
			afterSlice: store.SliceUsers,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := New(24 * time.Hour * 7)
			if c.name == "machine key expiring" {
				w = New(24 * time.Hour)
			}
			if evs := w.Observe(c.before, store.SliceAll, t0); len(evs) != 0 {
				t.Fatalf("baseline emitted %v", kinds(evs))
			}
			sl := c.afterSlice
			if sl == 0 {
				sl = store.SliceAll
			}
			got := kinds(w.Observe(c.after, sl, t0.Add(c.at)))
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestNotLoadedIsNotBaseline(t *testing.T) {
	w := New(0)
	empty := store.New()
	if evs := w.Observe(empty, store.SliceAll, t0); len(evs) != 0 {
		t.Fatal(evs)
	}
	// The first real load is still the baseline.
	if evs := w.Observe(st([]api.Node{node(1, "nas", true)}, nil, nil), store.SliceAll, t0); len(evs) != 0 {
		t.Fatalf("first load emitted %v", kinds(evs))
	}
}

func TestTextAndFilter(t *testing.T) {
	evs := []Event{
		{Kind: NodeOnline, Subject: "a"},
		{Kind: NodeOffline, Subject: "b"},
		{Kind: KeyExpiring, Subject: "k", Detail: "expires in 2d"},
		{Kind: KeyExpiring, Subject: "k2"},
		{Kind: RouteAdvertised, Subject: "gw", Detail: "10.0.0.0/8"},
		{Kind: "other", Subject: "x"},
	}
	want := []string{"a came online", "b went offline", "k expires in 2d", "k2 expires soon", "gw advertised 10.0.0.0/8", "x"}
	for i, e := range evs {
		if got := e.Text(); got != want[i] {
			t.Errorf("Text %d = %q, want %q", i, got, want[i])
		}
	}
	if got := Filter(evs, []string{"node_offline", "route_advertised"}); len(got) != 2 || got[0].Subject != "b" {
		t.Errorf("Filter = %v", got)
	}
	if got := Filter(evs, nil); len(got) != 0 {
		t.Errorf("Filter(nil) = %v", got)
	}
	if approx(30*time.Second) != "1m" {
		t.Error(approx(30 * time.Second))
	}
}
