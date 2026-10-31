package cli

import (
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/tonhe/headsup/internal/api"
	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/store"
)

func routesLs(r *runner, args []string) error {
	v := r.newFlags("routes ls", fmtTable)
	pending := v.fs.Bool("pending", false, "only routes waiting for approval")
	if _, err := v.args(args, 0, 0, "routes ls [--pending]"); err != nil {
		return err
	}
	st, err := r.load(store.SliceNodes)
	if err != nil {
		return err
	}
	ns := append([]api.Node(nil), st.Nodes...)
	sortNodes(ns)
	out := []routeOut{}
	for _, n := range ns {
		for _, p := range n.AvailableRoutes {
			ok := slices.Contains(n.ApprovedRoutes, p)
			if *pending && ok {
				continue
			}
			out = append(out, routeOut{NodeID: n.ID, Node: n.DisplayName(), Prefix: p.String(), Approved: ok,
				Exit: api.IsExitRoute(p), Online: n.Online})
		}
	}
	return emit(r.stdout, v.out, out, func(wide bool) table {
		t := table{head: []string{"NODE ID", "NODE", "PREFIX", "STATE", "EXIT"}}
		if wide {
			t.head = append(t.head, "ONLINE")
		}
		for _, x := range out {
			state := "pending"
			if x.Approved {
				state = "approved"
			}
			row := []string{idStr(x.NodeID), x.Node, x.Prefix, state, yesNo(x.Exit)}
			if wide {
				row = append(row, yesNo(x.Online))
			}
			t.add(row...)
		}
		return t
	})
}

func routesApprove(r *runner, args []string) error { return routesSet(r, args, true) }
func routesRevoke(r *runner, args []string) error  { return routesSet(r, args, false) }

// routesSet drives the machine's "Approve routes" form: every advertised
// route is a toggle; the named prefixes are switched on or off and the rest
// keep their current state. "exit" names both default routes.
func routesSet(r *runner, args []string, approve bool) error {
	verb := "revoke"
	if approve {
		verb = "approve"
	}
	v := r.newFlags("routes "+verb, "")
	pos, err := v.args(args, 2, -1, "routes "+verb+" ID|NAME PREFIX... (or exit)")
	if err != nil {
		return err
	}
	st, err := r.load(store.SliceNodes)
	if err != nil {
		return err
	}
	n, err := resolveNode(st, pos[0])
	if err != nil {
		return err
	}
	var want []netip.Prefix
	for _, a := range pos[1:] {
		if strings.EqualFold(a, "exit") {
			want = append(want, netip.MustParsePrefix("0.0.0.0/0"), netip.MustParsePrefix("::/0"))
			continue
		}
		p, err := netip.ParsePrefix(a)
		if err != nil {
			return errorf(ExitInvalid, "%q is not a prefix (e.g. 10.0.0.0/24)", a)
		}
		want = append(want, p.Masked())
	}
	vals := module.Values{}
	for i, p := range n.AvailableRoutes {
		vals["r"+strconv.Itoa(i)] = strconv.FormatBool(slices.Contains(n.ApprovedRoutes, p))
	}
	for _, p := range want {
		i := slices.Index(n.AvailableRoutes, p)
		if i < 0 {
			return errorf(ExitNotFound, "%s does not advertise %s", n.DisplayName(), p)
		}
		vals["r"+strconv.Itoa(i)] = strconv.FormatBool(approve)
	}
	if _, _, err := r.runAction("machines", false, store.SliceNodes, selectID(n.ID),
		call{id: "machines.routes", values: vals, yes: v.yes}); err != nil {
		return err
	}
	return r.printNode(v, n.ID)
}
