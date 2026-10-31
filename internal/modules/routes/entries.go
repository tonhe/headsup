package routes

import (
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/tonhe/headsup/internal/api"
	"github.com/tonhe/headsup/internal/store"
)

var exitRoutes = []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0"), netip.MustParsePrefix("::/0")}

// state is a route's approval and serving state.
type state int

const (
	statePending  state = iota // advertised, not approved
	stateApproved              // approved, not currently served
	stateServed                // approved and being served
)

func (s state) String() string {
	switch s {
	case stateServed:
		return "served"
	case stateApproved:
		return "approved"
	default:
		return "pending"
	}
}

// role is a machine's place in an HA set, from the server's own route state
// (host mode only).
type role int

const (
	roleUnknown  role = iota
	rolePrimary       // currently serving the prefix
	roleFailover      // a healthy candidate that takes over if the primary fails
	roleStandby       // advertises the prefix but is not a candidate
)

// entry is one advertised route on one machine. Exit-node advertisements
// (both default routes) are a single entry with exit set.
type entry struct {
	node     api.Node
	prefix   netip.Prefix
	exit     bool
	state    state
	first    bool // first row of its prefix group
	haSize   int  // machines advertising this prefix
	overlaps []netip.Prefix
	role     role
}

func entryKey(id uint64, prefix string) string { return strconv.FormatUint(id, 10) + "/" + prefix }

func (e entry) key() string {
	if e.exit {
		return entryKey(e.node.ID, "exit")
	}
	return entryKey(e.node.ID, e.prefix.String())
}

// routes returns the prefixes this entry stands for.
func (e entry) routes() []netip.Prefix {
	if !e.exit {
		return []netip.Prefix{e.prefix}
	}
	var out []netip.Prefix
	for _, p := range exitRoutes {
		if slices.Contains(e.node.AvailableRoutes, p) {
			out = append(out, p)
		}
	}
	return out
}

type entries struct {
	subnets []entry
	exits   []entry
}

func routeState(n api.Node, ps []netip.Prefix) state {
	for _, p := range ps {
		if !slices.Contains(n.ApprovedRoutes, p) {
			return statePending
		}
	}
	for _, p := range ps {
		if !slices.Contains(n.SubnetRoutes, p) {
			return stateApproved
		}
	}
	return stateServed
}

// buildEntries derives the routes view from the store: subnet routes grouped
// by prefix (HA sets), overlap detection, exit nodes, and, when hostRoutes is
// set, primary and failover roles from the server's route state.
func buildEntries(st *store.Store, hostRoutes bool) entries {
	var out entries
	for _, n := range st.Nodes {
		hasExit := false
		for _, p := range n.AvailableRoutes {
			if api.IsExitRoute(p) {
				hasExit = true
				continue
			}
			out.subnets = append(out.subnets, entry{node: n, prefix: p, state: routeState(n, []netip.Prefix{p})})
		}
		if hasExit {
			e := entry{node: n, prefix: exitRoutes[0], exit: true}
			e.state = routeState(n, e.routes())
			out.exits = append(out.exits, e)
		}
	}
	slices.SortStableFunc(out.subnets, func(a, b entry) int {
		if c := a.prefix.Addr().Compare(b.prefix.Addr()); c != 0 {
			return c
		}
		if c := a.prefix.Bits() - b.prefix.Bits(); c != 0 {
			return c
		}
		return strings.Compare(a.node.DisplayName(), b.node.DisplayName())
	})
	slices.SortStableFunc(out.exits, func(a, b entry) int {
		return strings.Compare(a.node.DisplayName(), b.node.DisplayName())
	})

	count := map[netip.Prefix]int{}
	var distinct []netip.Prefix
	for _, e := range out.subnets {
		if count[e.prefix] == 0 {
			distinct = append(distinct, e.prefix)
		}
		count[e.prefix]++
	}
	overlaps := map[netip.Prefix][]netip.Prefix{}
	for i, p := range distinct {
		for _, q := range distinct[i+1:] {
			if p.Overlaps(q) {
				overlaps[p] = append(overlaps[p], q)
				overlaps[q] = append(overlaps[q], p)
			}
		}
	}
	var rs map[string]routeInfo
	if hostRoutes {
		rs = map[string]routeInfo{}
		for _, r := range st.Routes {
			rs[r.Prefix.String()] = routeInfo{primary: r.Primary, candidates: r.Candidates}
		}
	}
	for i := range out.subnets {
		e := &out.subnets[i]
		e.haSize = count[e.prefix]
		e.first = i == 0 || out.subnets[i-1].prefix != e.prefix
		e.overlaps = overlaps[e.prefix]
		if ri, ok := rs[e.prefix.String()]; ok {
			switch {
			case ri.primary == e.node.ID:
				e.role = rolePrimary
			case slices.Contains(ri.candidates, e.node.ID):
				e.role = roleFailover
			default:
				e.role = roleStandby
			}
		}
	}
	return out
}

type routeInfo struct {
	primary    uint64
	candidates []uint64
}

// PendingCount is the number of route approvals waiting: each pending subnet
// route, plus one per machine whose exit-node use is not approved.
func PendingCount(st *store.Store) int {
	n := 0
	es := buildEntries(st, false)
	for _, e := range append(es.subnets, es.exits...) {
		if e.state == statePending {
			n++
		}
	}
	return n
}

// withAdded returns approved plus add, keeping every existing approval (also
// ones the node no longer advertises) and its order.
func withAdded(approved, add []netip.Prefix) []netip.Prefix {
	out := slices.Clone(approved)
	for _, p := range add {
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

// without returns approved minus rm, otherwise unchanged.
func without(approved, rm []netip.Prefix) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(approved))
	for _, p := range approved {
		if !slices.Contains(rm, p) {
			out = append(out, p)
		}
	}
	return out
}
