package routes

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/tonhe/headsup/internal/api"
	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/icons"
	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/store"
	"github.com/tonhe/headsup/internal/ui/uitest"
)

var pfx = netip.MustParsePrefix

func setup(t *testing.T) (*Model, *testCtx) {
	t.Helper()
	c := newCtx(t)
	m := New()
	m.Init(c)
	m.Update(c, store.UpdatedMsg{Slices: store.SliceAll})
	return m, c
}

// selectEntry puts the cursor on the subnet (or exit) row for node name and prefix.
func selectEntry(t *testing.T, m *Model, c *testCtx, node, prefix string) {
	t.Helper()
	for _, n := range c.st.Nodes {
		if n.DisplayName() != node {
			continue
		}
		key := entryKey(n.ID, prefix)
		if prefix == "exit" {
			m.focusExit = true
			if !m.exitTbl.SetCursorKey(key) {
				t.Fatalf("no exit row for %s", node)
			}
			return
		}
		m.focusExit = false
		if !m.subTbl.SetCursorKey(key) {
			t.Fatalf("no route row %s for %s", prefix, node)
		}
		return
	}
	t.Fatalf("no node %q", node)
}

func lastSetRoutes(t *testing.T, c *testCtx) (uint64, []netip.Prefix) {
	t.Helper()
	var calls [][]any
	for _, call := range c.b.Calls()[c.skip:] {
		if call.Method == "SetApprovedRoutes" {
			calls = append(calls, call.Args)
		}
	}
	if len(calls) != 1 {
		t.Fatalf("SetApprovedRoutes called %d times, want 1", len(calls))
	}
	return calls[0][0].(uint64), calls[0][1].([]netip.Prefix)
}

func sameSet(a, b []netip.Prefix) bool {
	if len(a) != len(b) {
		return false
	}
	for _, p := range a {
		if !slices.Contains(b, p) {
			return false
		}
	}
	return true
}

// T114: approving one prefix sends the node's full approved list with only
// that prefix added.
func TestApproveSendsFullListWithPrefixAdded(t *testing.T) {
	tests := []struct {
		name    string
		prep    []netip.Prefix // approved routes to set on the node first; nil keeps the seed
		approve string
		want    []netip.Prefix
	}{
		{
			name:    "adds to existing approvals",
			approve: "192.168.50.0/24",
			want:    []netip.Prefix{pfx("192.168.51.0/24"), pfx("192.168.50.0/24")},
		},
		{
			name:    "keeps approvals the node no longer advertises",
			prep:    []netip.Prefix{pfx("192.168.51.0/24"), pfx("10.99.0.0/24")},
			approve: "192.168.50.0/24",
			want:    []netip.Prefix{pfx("192.168.51.0/24"), pfx("10.99.0.0/24"), pfx("192.168.50.0/24")},
		},
		{
			name:    "first approval",
			prep:    []netip.Prefix{},
			approve: "192.168.51.0/24",
			want:    []netip.Prefix{pfx("192.168.51.0/24")},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newCtx(t)
			n := nodeNamed(t, c, "vpn-gw-lab")
			if tt.prep != nil {
				if _, err := c.b.SetApprovedRoutes(t.Context(), n.ID, tt.prep); err != nil {
					t.Fatal(err)
				}
				c.reload()
			}
			c.skip = len(c.b.Calls())
			m := New()
			m.Init(c)
			m.Update(c, store.UpdatedMsg{Slices: store.SliceAll})
			selectEntry(t, m, c, "vpn-gw-lab", tt.approve)

			a := action(t, m, "routes.approve")
			if ok, why := a.Enabled(c); !ok {
				t.Fatalf("approve disabled: %s", why)
			}
			if !a.Mutating || a.Danger != module.DangerConfirm {
				t.Fatalf("approve should be a confirmed mutation: %+v", a)
			}
			if tg := a.Targets(c); len(tg) != 1 || !strings.Contains(tg[0].Name, tt.approve) {
				t.Fatalf("targets = %+v", tg)
			}
			res := run(t, c, a, nil)
			if res.Err != nil {
				t.Fatal(res.Err)
			}
			if res.Refresh&store.SliceNodes == 0 {
				t.Fatal("approve should refresh nodes")
			}
			id, got := lastSetRoutes(t, c)
			if id != n.ID {
				t.Fatalf("approved on node %d, want %d", id, n.ID)
			}
			if !sameSet(got, tt.want) {
				t.Fatalf("sent %v, want %v", got, tt.want)
			}
		})
	}
}

func nodeNamed(t *testing.T, c *testCtx, name string) api.Node {
	t.Helper()
	for _, n := range c.st.Nodes {
		if n.DisplayName() == name {
			return n
		}
	}
	t.Fatalf("no node %q", name)
	return api.Node{}
}

func TestRevokeRemovesOnlyThatPrefix(t *testing.T) {
	m, c := setup(t)
	selectEntry(t, m, c, "vpn-gw-lab", "192.168.51.0/24")
	if ok, _ := action(t, m, "routes.approve").Enabled(c); ok {
		t.Fatal("approve should be disabled for an approved route")
	}
	a := action(t, m, "routes.revoke")
	if ok, why := a.Enabled(c); !ok {
		t.Fatal(why)
	}
	if res := run(t, c, a, nil); res.Err != nil {
		t.Fatal(res.Err)
	}
	_, got := lastSetRoutes(t, c)
	if len(got) != 0 {
		t.Fatalf("revoke sent %v, want empty", got)
	}
}

func TestExitNodeApproveAndRevoke(t *testing.T) {
	m, c := setup(t)
	selectEntry(t, m, c, "exit-fra-1", "exit")
	if ok, _ := action(t, m, "routes.approve").Enabled(c); ok {
		t.Fatal("exit-fra-1 is already approved")
	}
	run(t, c, action(t, m, "routes.revoke"), nil)
	_, got := lastSetRoutes(t, c)
	if len(got) != 0 {
		t.Fatalf("revoking exit sent %v, want both default routes removed", got)
	}
	c.reload()
	m.Update(c, store.UpdatedMsg{Slices: store.SliceNodes})
	selectEntry(t, m, c, "exit-fra-1", "exit")
	a := action(t, m, "routes.approve")
	if ok, why := a.Enabled(c); !ok {
		t.Fatal(why)
	}
	c.skip = len(c.b.Calls())
	run(t, c, a, nil)
	_, got = lastSetRoutes(t, c)
	if !sameSet(got, []netip.Prefix{pfx("0.0.0.0/0"), pfx("::/0")}) {
		t.Fatalf("approving exit sent %v", got)
	}
}

func TestApproveAllForNode(t *testing.T) {
	c := newCtx(t)
	n := nodeNamed(t, c, "vpn-gw-lab")
	if _, err := c.b.SetApprovedRoutes(t.Context(), n.ID, nil); err != nil {
		t.Fatal(err)
	}
	c.reload()
	c.skip = len(c.b.Calls())
	m := New()
	m.Init(c)
	m.Update(c, store.UpdatedMsg{Slices: store.SliceAll})
	selectEntry(t, m, c, "vpn-gw-lab", "192.168.50.0/24")
	a := action(t, m, "routes.approveall")
	if ok, why := a.Enabled(c); !ok {
		t.Fatal(why)
	}
	run(t, c, a, nil)
	_, got := lastSetRoutes(t, c)
	if !sameSet(got, []netip.Prefix{pfx("192.168.50.0/24"), pfx("192.168.51.0/24")}) {
		t.Fatalf("approve all sent %v", got)
	}

	selectEntry(t, m, c, "router-ha-a", "10.0.0.0/24")
	if ok, _ := action(t, m, "routes.approveall").Enabled(c); ok {
		t.Fatal("approve all should be disabled when nothing is pending")
	}
}

func TestGroupsHASetsAndOverlaps(t *testing.T) {
	c := newCtx(t)
	// Add an overlapping /16 on a second machine.
	n := nodeNamed(t, c, "homelab-alice")
	if n.AvailableRoutes[0] != pfx("10.20.0.0/16") {
		t.Fatalf("seed changed: %v", n.AvailableRoutes)
	}
	es := buildEntries(c.st, false)
	var ha []entry
	for _, e := range es.subnets {
		if e.prefix == pfx("10.0.0.0/24") {
			ha = append(ha, e)
		}
	}
	if len(ha) != 2 || ha[0].haSize != 2 || !ha[0].first || ha[1].first {
		t.Fatalf("HA set not grouped: %+v", ha)
	}
	if len(es.exits) != 1 || es.exits[0].node.DisplayName() != "exit-fra-1" {
		t.Fatalf("exits = %+v", es.exits)
	}

	// Overlap: 10.20.0.0/16 vs a new 10.20.5.0/24.
	st := *c.st
	st.Nodes = append(slices.Clone(st.Nodes), api.Node{ID: 999, GivenName: "overlapper",
		AvailableRoutes: []netip.Prefix{pfx("10.20.5.0/24")}})
	es = buildEntries(&st, false)
	found := false
	for _, e := range es.subnets {
		if e.prefix == pfx("10.20.5.0/24") {
			found = true
			if len(e.overlaps) != 1 || e.overlaps[0] != pfx("10.20.0.0/16") {
				t.Fatalf("overlaps = %v", e.overlaps)
			}
		}
		if e.prefix == pfx("10.0.0.0/24") && len(e.overlaps) != 0 {
			t.Fatalf("10.0.0.0/24 should not overlap anything: %v", e.overlaps)
		}
	}
	if !found {
		t.Fatal("overlapper missing")
	}
}

// T117: in host mode, the server's route state marks primary and failover.
func TestPrimaryAndFailoverMarkers(t *testing.T) {
	c := newCtx(t)
	c.host = true
	a, b := nodeNamed(t, c, "router-ha-a"), nodeNamed(t, c, "router-ha-b")
	c.st.Routes = []host.RouteState{{Prefix: pfx("10.0.0.0/24"), Primary: b.ID, Candidates: []uint64{b.ID, a.ID}}}
	m := New()
	m.Init(c)
	m.Update(c, store.UpdatedMsg{Slices: store.SliceAll})
	es := buildEntries(c.st, true)
	for _, e := range es.subnets {
		switch e.node.ID {
		case a.ID:
			if e.role != roleFailover {
				t.Fatalf("router-ha-a role = %v, want failover", e.role)
			}
		case b.ID:
			if e.role != rolePrimary {
				t.Fatalf("router-ha-b role = %v, want primary", e.role)
			}
		}
	}
	out := m.View(c, module.Rect{W: 120, H: 30})
	if !strings.Contains(out, "primary") || !strings.Contains(out, "failover") {
		t.Fatalf("view lacks HA markers:\n%s", out)
	}
	// Without host data, nothing claims to be primary.
	for _, e := range buildEntries(c.st, false).subnets {
		if e.role != roleUnknown {
			t.Fatalf("remote mode should not mark roles: %+v", e)
		}
	}
}

func TestBadgeCountsPendingRoutes(t *testing.T) {
	m, c := setup(t)
	// Seed: homelab-alice 10.20.0.0/16 and vpn-gw-lab 192.168.50.0/24 pending.
	if got := m.Badge(c.st); got != "2" {
		t.Fatalf("badge = %q, want 2", got)
	}
	n := nodeNamed(t, c, "exit-fra-1")
	if _, err := c.b.SetApprovedRoutes(t.Context(), n.ID, nil); err != nil {
		t.Fatal(err)
	}
	c.reload()
	if got := m.Badge(c.st); got != "3" {
		t.Fatalf("badge = %q, want 3 (an exit node counts once)", got)
	}
	if got := m.Badge(storeWithoutNodes()); got != "" {
		t.Fatalf("empty badge = %q", got)
	}
}

func storeWithoutNodes() *store.Store { return store.New() }

func TestTabSwitchesSectionAndEnterOpensMachine(t *testing.T) {
	m, c := setup(t)
	if m.focusExit {
		t.Fatal("subnets should have focus first")
	}
	m.Update(c, keyMsg("tab"))
	if !m.focusExit {
		t.Fatal("tab should focus exit nodes")
	}
	run(t, c, action(t, m, "routes.machine"), nil)
	if len(c.navs) != 1 || c.navs[0].id != "machines" {
		t.Fatalf("navs = %+v", c.navs)
	}
	n := nodeNamed(t, c, "exit-fra-1")
	if sa, ok := c.navs[0].args.(module.SelectArgs); !ok || sa.ID != n.ID {
		t.Fatalf("args = %#v", c.navs[0].args)
	}
	m.Update(c, keyMsg("tab"))
	m.Update(c, keyMsg("j"))
	if m.focusExit {
		t.Fatal("second tab returns to subnets")
	}
}

func TestViewGolden(t *testing.T) {
	for _, sz := range [][2]int{{98, 37}, {58, 21}} {
		t.Run(fmt.Sprintf("%dx%d", sz[0], sz[1]), func(t *testing.T) {
			uitest.Screen(t, func(t *testing.T, ic icons.Set) string {
				c := newCtx(t)
				c.ic = ic
				c.host = true
				a, b := nodeNamed(t, c, "router-ha-a"), nodeNamed(t, c, "router-ha-b")
				c.st.Routes = []host.RouteState{{Prefix: pfx("10.0.0.0/24"), Primary: a.ID, Candidates: []uint64{a.ID, b.ID}}}
				m := New()
				m.Init(c)
				m.Update(c, store.UpdatedMsg{Slices: store.SliceAll})
				out := m.View(c, module.Rect{W: sz[0], H: sz[1]})
				if w := uitest.MaxWidth(out); w > sz[0] {
					t.Fatalf("view is %d wide, want <= %d", w, sz[0])
				}
				if h := len(uitest.Lines(out)); h > sz[1] {
					t.Fatalf("view is %d tall, want <= %d", h, sz[1])
				}
				return out
			})
		})
	}
}

func TestEmptyAndLoading(t *testing.T) {
	c := newCtx(t)
	m := New()
	m.Init(c)
	if v := m.View(c, module.Rect{W: 80, H: 20}); !strings.Contains(v, "Loading") {
		t.Fatalf("want loading:\n%s", v)
	}
	c.st.Nodes = nil
	m.Update(c, store.UpdatedMsg{Slices: store.SliceNodes})
	if v := m.View(c, module.Rect{W: 80, H: 20}); !strings.Contains(v, "No routes") {
		t.Fatalf("want empty state:\n%s", v)
	}
	if ok, _ := action(t, m, "routes.approve").Enabled(c); ok {
		t.Fatal("approve with no rows should be disabled")
	}
}
