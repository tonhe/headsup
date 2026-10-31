package cli

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/tonhe/headsup/internal/api"
	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/store"
	"github.com/tonhe/headsup/internal/ui/filter"
	"github.com/tonhe/headsup/internal/ui/humanize"
)

// expiryWarn matches the machines page's "expiring" window.
const expiryWarn = 7 * 24 * time.Hour

// nodeMatch adapts a node to the TUI's filter syntax (text, tag:x, user:x,
// is:online, -neg, ~fuzzy), so --filter means the same as the / bar.
type nodeMatch struct {
	n   api.Node
	now time.Time
}

func (m nodeMatch) FilterText() string {
	n := m.n
	parts := []string{n.GivenName, n.Name, n.User.Name}
	for _, ip := range n.IPAddresses {
		parts = append(parts, ip.String())
	}
	parts = append(parts, n.Tags...)
	return strings.ToLower(strings.Join(parts, " "))
}

func (m nodeMatch) FilterField(key string) []string {
	n := m.n
	switch key {
	case "tag":
		out := make([]string, 0, 2*len(n.Tags))
		for _, t := range n.Tags {
			out = append(out, strings.TrimPrefix(t, "tag:"), t)
		}
		return out
	case "user":
		return []string{n.User.Name}
	case "name":
		return []string{n.GivenName, n.Name}
	case "ip":
		var out []string
		for _, ip := range n.IPAddresses {
			out = append(out, ip.String())
		}
		return out
	case "route":
		return prefixStrings(n.AvailableRoutes)
	case "id":
		return []string{strconv.FormatUint(n.ID, 10)}
	}
	return nil
}

func (m nodeMatch) FilterIs(state string) bool {
	n := m.n
	switch state {
	case "online":
		return n.Online
	case "offline":
		return !n.Online
	case "idle":
		return n.Status(m.now) == api.StatusIdle
	case "expired":
		return n.Expired(m.now)
	case "expiring":
		return n.ExpiresSoon(m.now, expiryWarn)
	case "exit":
		return n.IsExitNode()
	case "pending":
		return len(n.PendingRoutes()) > 0
	case "tagged":
		return len(n.Tags) > 0
	}
	return false
}

// resolveNode turns ID|NAME into a node. A name must be unique; an
// ambiguous one lists the candidates and exits 2.
func resolveNode(st *store.Store, arg string) (api.Node, error) {
	if id, err := strconv.ParseUint(arg, 10, 64); err == nil {
		if n, ok := st.Node(id); ok {
			return n, nil
		}
	}
	var hits []api.Node
	for _, n := range st.Nodes {
		if strings.EqualFold(n.GivenName, arg) || strings.EqualFold(n.Name, arg) {
			hits = append(hits, n)
		}
	}
	switch len(hits) {
	case 0:
		return api.Node{}, errorf(ExitNotFound, "no machine %q", arg)
	case 1:
		return hits[0], nil
	}
	var c []string
	for _, n := range hits {
		c = append(c, fmt.Sprintf("%d (%s, %s)", n.ID, n.DisplayName(), n.User.Name))
	}
	return api.Node{}, usagef("%q matches %d machines; use an ID: %s", arg, len(hits), strings.Join(c, "; "))
}

func firstIP(n api.Node, v4 bool) string {
	for _, ip := range n.IPAddresses {
		if ip.Is4() == v4 {
			return ip.String()
		}
	}
	return ""
}

func nodesTable(ns []api.Node, now time.Time) func(bool) table {
	return func(wide bool) table {
		t := table{head: []string{"ID", "NAME", "USER", "IP", "STATUS", "LAST SEEN", "TAGS"}}
		if wide {
			t.head = append(t.head, "HOSTNAME", "IPV6", "EXPIRY", "ROUTES", "CREATED")
		}
		for _, n := range ns {
			seen := "now"
			if !n.Online {
				seen = ""
				if !n.LastSeen.IsZero() {
					seen = humanize.Ago(n.LastSeen, now)
				}
			}
			row := []string{idStr(n.ID), n.DisplayName(), n.User.Name, firstIP(n, true), n.Status(now).String(), seen, strings.Join(n.Tags, ",")}
			if wide {
				exp := "never"
				if !n.ExpiryDisabled() {
					exp = cellTime(*n.Expiry)
				}
				var routes []string
				for _, p := range n.AvailableRoutes {
					s := p.String()
					if !containsPrefix(n.ApprovedRoutes, s) {
						s += "*"
					}
					routes = append(routes, s)
				}
				row = append(row, n.Name, firstIP(n, false), exp, strings.Join(routes, ","), cellTime(n.CreatedAt))
			}
			t.add(row...)
		}
		return t
	}
}

func containsPrefix(ps []netip.Prefix, s string) bool {
	for _, p := range ps {
		if p.String() == s {
			return true
		}
	}
	return false
}

func nodesLs(r *runner, args []string) error {
	v := r.newFlags("nodes ls", fmtTable)
	user := v.fs.String("user", "", "only this user's machines")
	q := v.fs.String("filter", "", "filter query, as in the TUI (tag:x is:online -word ~fuzzy)")
	if _, err := v.args(args, 0, 0, "nodes ls [--user U] [--filter Q] [-o table|wide|json|yaml]"); err != nil {
		return err
	}
	st, err := r.load(store.SliceNodes)
	if err != nil {
		return err
	}
	now := r.now()
	query := filter.Parse(*q)
	var ns []api.Node
	for _, n := range st.Nodes {
		if *user != "" && n.User.Name != *user {
			continue
		}
		if !query.Empty() && !query.Match(nodeMatch{n: n, now: now}) {
			continue
		}
		ns = append(ns, n)
	}
	sortNodes(ns)
	out := make([]nodeOut, len(ns))
	for i, n := range ns {
		out[i] = nodeDTO(n, now)
	}
	return emit(r.stdout, v.out, out, nodesTable(ns, now))
}

func nodeDetail(n api.Node, now time.Time) func(bool) table {
	return func(bool) table {
		d := nodeDTO(n, now)
		str := func(p *string) string {
			if p == nil {
				return ""
			}
			return *p
		}
		exp := str(d.Expiry)
		if d.Expiry == nil {
			exp = "never"
		}
		return kvTable(
			[2]string{"id", idStr(d.ID)}, [2]string{"name", d.Name}, [2]string{"hostname", d.Hostname},
			[2]string{"user", d.User.Name}, [2]string{"ips", strings.Join(d.IPAddresses, ", ")},
			[2]string{"tags", strings.Join(d.Tags, ", ")}, [2]string{"status", d.Status},
			[2]string{"last_seen", str(d.LastSeen)}, [2]string{"expiry", exp},
			[2]string{"created", str(d.CreatedAt)}, [2]string{"registered_by", d.RegisterMethod},
			[2]string{"routes", strings.Join(d.AvailableRoutes, ", ")},
			[2]string{"approved", strings.Join(d.ApprovedRoutes, ", ")},
			[2]string{"exit_node", yesNo(d.ExitNode)},
		)
	}
}

func nodesShow(r *runner, args []string) error {
	v := r.newFlags("nodes show", fmtTable)
	pos, err := v.args(args, 1, 1, "nodes show ID|NAME")
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
	return emit(r.stdout, v.out, nodeDTO(n, r.now()), nodeDetail(n, r.now()))
}

// nodeAction runs a machines-page Action on one node and prints the result
// when -o is given.
func (r *runner) nodeAction(v *verbFlags, arg string, c call) error {
	st, err := r.load(store.SliceNodes | store.SliceUsers)
	if err != nil {
		return err
	}
	n, err := resolveNode(st, arg)
	if err != nil {
		return err
	}
	c.yes = v.yes
	if _, _, err := r.runAction("machines", false, store.SliceNodes, selectID(n.ID), c); err != nil {
		return err
	}
	return r.printNode(v, n.ID)
}

// printNode prints the node after a change, only when -o was given.
func (r *runner) printNode(v *verbFlags, id uint64) error {
	if v.out == "" {
		return nil
	}
	n, ok := r.rec.lastNode()
	if !ok || n.ID != id {
		if err := r.reload(store.SliceNodes); err != nil {
			return err
		}
		if n, ok = r.st.Node(id); !ok {
			return nil // deleted
		}
	}
	return emit(r.stdout, v.out, nodeDTO(n, r.now()), nodeDetail(n, r.now()))
}

func nodesRename(r *runner, args []string) error {
	v := r.newFlags("nodes rename", "")
	pos, err := v.args(args, 2, 2, "nodes rename ID|NAME NEW")
	if err != nil {
		return err
	}
	return r.nodeAction(v, pos[0], call{id: "machines.rename", values: module.Values{"name": pos[1]}})
}

func normTag(t string) string {
	t = strings.TrimSpace(t)
	if t != "" && !strings.HasPrefix(t, "tag:") {
		t = "tag:" + t
	}
	return t
}

func splitList(s string) []string {
	var out []string
	for _, x := range strings.Split(s, ",") {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

func nodesTags(r *runner, args []string) error {
	v := r.newFlags("nodes tags", "")
	set := v.fs.String("set", "", "replace the tags with this comma-separated list (\"\" clears)")
	var add, rm multiFlag
	v.fs.Var(&add, "add", "add a tag (repeatable, or comma-separated)")
	v.fs.Var(&rm, "rm", "remove a tag (repeatable, or comma-separated)")
	pos, err := v.args(args, 1, 1, "nodes tags ID|NAME [--set a,b | --add a | --rm a]")
	if err != nil {
		return err
	}
	setGiven := flagGiven(v, "set")
	if setGiven == (len(add)+len(rm) > 0) {
		return usagef("give either --set, or --add/--rm")
	}
	st, err := r.load(store.SliceNodes)
	if err != nil {
		return err
	}
	n, err := resolveNode(st, pos[0])
	if err != nil {
		return err
	}
	var tags []string
	if setGiven {
		tags = splitList(*set)
	} else {
		drop := map[string]bool{}
		for _, t := range rm.list() {
			drop[normTag(t)] = true
		}
		seen := map[string]bool{}
		for _, t := range append(append([]string(nil), n.Tags...), add.list()...) {
			t = normTag(t)
			if !drop[t] && !seen[t] {
				seen[t] = true
				tags = append(tags, t)
			}
		}
	}
	return r.nodeAction(v, pos[0], call{id: "machines.tags", values: module.Values{"set": strings.Join(tags, ", ")}})
}

func nodesExpire(r *runner, args []string) error {
	v := r.newFlags("nodes expire", "")
	disable := v.fs.Bool("disable", false, "disable key expiry instead of expiring now")
	pos, err := v.args(args, 1, 1, "nodes expire ID|NAME [--disable]")
	if err != nil {
		return err
	}
	id := "machines.expire"
	if *disable {
		id = "machines.noexpire"
	}
	return r.nodeAction(v, pos[0], call{id: id})
}

func nodesDelete(r *runner, args []string) error {
	v := r.newFlags("nodes delete", "")
	pos, err := v.args(args, 1, 1, "nodes delete ID|NAME [--yes]")
	if err != nil {
		return err
	}
	return r.nodeAction(v, pos[0], call{id: "machines.delete"})
}

func nodesRegister(r *runner, args []string) error {
	v := r.newFlags("nodes register", "")
	user := v.fs.String("user", "", "the user who owns the machine (required)")
	pos, err := v.args(args, 1, 1, "nodes register --user U KEY|URL")
	if err != nil {
		return err
	}
	if *user == "" {
		return usagef("nodes register needs --user")
	}
	_, _, err = r.runAction("pending", false, store.SliceUsers, nil,
		call{id: "pending.paste", yes: v.yes, values: module.Values{"text": pos[0], "user": *user}})
	if err != nil {
		return err
	}
	if n, ok := r.rec.lastNode(); ok && v.out != "" {
		return emit(r.stdout, v.out, nodeDTO(n, r.now()), nodeDetail(n, r.now()))
	}
	return nil
}
