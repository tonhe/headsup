package policy

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"
)

// PreviewNote labels every access preview: it is worked out locally and is
// best-effort (research R6).
const PreviewNote = "Best-effort preview worked out here from the policy text. It covers accept rules, " +
	"ports, protocols, and autogroup:self, member, and tagged; it does not cover posture rules or via routing. " +
	"The policy's own tests, run by the server, are the source of truth."

// Machine is a node as the preview sees it.
type Machine struct {
	ID   uint64
	Name string
	User string // owner's user name; ignored for policy when the machine is tagged
	Tags []string
	IPs  []netip.Addr
}

// Tagged reports whether the machine has tags (and so no user identity).
func (m Machine) Tagged() bool { return len(m.Tags) > 0 }

// SubjectKind is what a preview source is.
type SubjectKind int

const (
	SubjectUser SubjectKind = iota
	SubjectTag
	SubjectMachine
)

func (k SubjectKind) String() string { return [...]string{"user", "tag", "machine"}[k] }

// Subject is who the preview is for: a user ("alice" or "alice@"), a tag
// ("tag:web"), or a machine (by ID; Name is for display).
type Subject struct {
	Kind SubjectKind
	Name string
	ID   uint64 // SubjectMachine
}

// Label is the source as shown to the user.
func (s Subject) Label() string {
	if s.Kind == SubjectUser {
		return normUser(s.Name) + "@"
	}
	return s.Name
}

// Destination kinds.
const (
	DestMachine  = "machine"
	DestAddress  = "address" // a host alias or IP/CIDR no machine owns
	DestInternet = "internet"
	DestAny      = "any" // "*": every address, including subnet routes
)

// Access is one destination the source can reach.
type Access struct {
	Kind    string // DestMachine, DestAddress, DestInternet, DestAny
	Dest    string // machine name, alias or prefix, "autogroup:internet", "*"
	ID      uint64 // machine ID for DestMachine
	Ports   []string
	Lines   []int // policy lines of the rules that allow it
	Machine *Machine
}

// Preview is what a source can reach.
type Preview struct {
	Subject Subject
	// From are the machines the source stands for.
	From   []Machine
	Access []Access
	// Notes are parts of the policy the preview skipped or could not judge.
	Notes []string
}

// identity is what src selectors are matched against.
type identity struct {
	users map[string]bool // normalised user names (untagged machines only)
	tags  map[string]bool
	ips   []netip.Addr
	self  string // owner for autogroup:self; "" when none
}

// Preview works out what src can reach under d, given the tailnet's machines.
func (d *Document) Preview(machines []Machine, src Subject) Preview {
	p := Preview{Subject: src}
	if d == nil {
		return p
	}
	id := identity{users: map[string]bool{}, tags: map[string]bool{}}
	switch src.Kind {
	case SubjectUser:
		u := normUser(src.Name)
		id.users[u], id.self = true, u
		for _, m := range machines {
			if !m.Tagged() && normUser(m.User) == u {
				p.From = append(p.From, m)
			}
		}
	case SubjectTag:
		id.tags[src.Name] = true
		for _, m := range machines {
			if contains(m.Tags, src.Name) {
				p.From = append(p.From, m)
			}
		}
	case SubjectMachine:
		for _, m := range machines {
			if m.ID != src.ID {
				continue
			}
			p.From = append(p.From, m)
			if m.Tagged() {
				for _, t := range m.Tags {
					id.tags[t] = true
				}
			} else {
				u := normUser(m.User)
				id.users[u], id.self = true, u
			}
		}
	}
	for _, m := range p.From {
		id.ips = append(id.ips, m.IPs...)
	}
	ev := evaluator{d: d, machines: machines, id: id, acc: map[string]*Access{}, notes: map[string]bool{}}
	for _, a := range d.ACLs {
		ev.acl(a)
	}
	for _, g := range d.Grants {
		ev.grant(g)
	}
	p.Access = ev.result()
	for n := range ev.notes {
		p.Notes = append(p.Notes, n)
	}
	sort.Strings(p.Notes)
	return p
}

type evaluator struct {
	d        *Document
	machines []Machine
	id       identity
	acc      map[string]*Access
	notes    map[string]bool
}

func (e *evaluator) note(format string, args ...any) { e.notes[fmt.Sprintf(format, args...)] = true }

func (e *evaluator) acl(a ACL) {
	if act := orDefault(a.Action, "accept"); act != "accept" {
		e.note("line %d: action %q is not evaluated", a.Line, act)
		return
	}
	if !e.srcMatches(a.Src, a.Line) {
		return
	}
	for _, dst := range a.Dst {
		alias, ports := splitDst(dst)
		var spec []string
		for _, pt := range strings.Split(ports, ",") {
			spec = append(spec, portSpec(a.Proto, strings.TrimSpace(pt)))
		}
		e.allow(alias, spec, a.Line)
	}
}

func (e *evaluator) grant(g Grant) {
	if len(g.IP) == 0 {
		if len(g.App) > 0 {
			e.note("line %d: application capability grant (no ip) is not network access; skipped", g.Line)
		}
		return
	}
	if !e.srcMatches(g.Src, g.Line) {
		return
	}
	if len(g.Via) > 0 {
		e.note("line %d: via routing (%s) is not evaluated; listed as if direct", g.Line, strings.Join(g.Via, ", "))
	}
	var spec []string
	for _, ip := range g.IP {
		proto, ports := "", strings.TrimSpace(ip)
		if i := strings.Index(ports, ":"); i >= 0 {
			proto, ports = ports[:i], ports[i+1:]
		}
		for _, pt := range strings.Split(ports, ",") {
			spec = append(spec, portSpec(proto, strings.TrimSpace(pt)))
		}
	}
	for _, dst := range g.Dst {
		e.allow(dst, spec, g.Line)
	}
}

// srcMatches reports whether any selector in src stands for the source.
func (e *evaluator) srcMatches(src []string, line int) bool {
	for _, s := range src {
		if e.selects(s, line) {
			return true
		}
	}
	return false
}

func (e *evaluator) selects(s string, line int) bool {
	id := e.id
	switch KindOf(s) {
	case "":
		return s == "*"
	case "autogroup":
		switch s {
		case "autogroup:member":
			return len(id.users) > 0
		case "autogroup:tagged":
			return len(id.tags) > 0
		case "autogroup:self":
			e.note("line %d: autogroup:self is only valid as a destination", line)
			return false
		}
		e.note("line %d: %s is not evaluated", line, s)
		return false
	case "group":
		for _, g := range e.d.Groups {
			if g.Name == s {
				for _, mem := range g.Members {
					if id.users[normUser(mem)] {
						return true
					}
				}
			}
		}
		return false
	case "tag":
		return id.tags[s]
	case "user":
		return id.users[normUser(s)]
	}
	pfx, ok := e.prefix(s)
	if !ok {
		return id.users[normUser(s)] // a bare user name without "@"
	}
	for _, ip := range id.ips {
		if pfx.Contains(ip) {
			return true
		}
	}
	return false
}

// prefix resolves a host alias or IP/CIDR.
func (e *evaluator) prefix(s string) (netip.Prefix, bool) {
	for _, h := range e.d.Hosts {
		if h.Name == s {
			s = h.Addr
			break
		}
	}
	if p, err := netip.ParsePrefix(s); err == nil {
		return p.Masked(), true
	}
	if a, err := netip.ParseAddr(s); err == nil {
		return netip.PrefixFrom(a, a.BitLen()), true
	}
	return netip.Prefix{}, false
}

// allow grants spec on every destination alias stands for.
func (e *evaluator) allow(alias string, spec []string, line int) {
	add := func(kind, dest string, m *Machine) {
		key := kind + "\x00" + dest
		if m != nil {
			key = fmt.Sprintf("%s\x00%d", kind, m.ID)
		}
		a := e.acc[key]
		if a == nil {
			a = &Access{Kind: kind, Dest: dest}
			if m != nil {
				mm := *m
				a.ID, a.Machine = m.ID, &mm
			}
			e.acc[key] = a
		}
		a.Ports = append(a.Ports, spec...)
		a.Lines = append(a.Lines, line)
	}
	each := func(f func(Machine) bool) {
		for i := range e.machines {
			if f(e.machines[i]) {
				add(DestMachine, e.machines[i].Name, &e.machines[i])
			}
		}
	}
	switch KindOf(alias) {
	case "":
		if alias == "*" {
			each(func(Machine) bool { return true })
			add(DestAny, "*", nil)
		}
		return
	case "autogroup":
		switch alias {
		case "autogroup:member":
			each(func(m Machine) bool { return !m.Tagged() })
		case "autogroup:tagged":
			each(Machine.Tagged)
		case "autogroup:self":
			if self := e.id.self; self != "" {
				each(func(m Machine) bool { return !m.Tagged() && normUser(m.User) == self })
			}
		case "autogroup:internet":
			add(DestInternet, alias, nil)
		default:
			e.note("line %d: destination %s is not evaluated", line, alias)
		}
		return
	case "group":
		members := map[string]bool{}
		for _, g := range e.d.Groups {
			if g.Name == alias {
				for _, mem := range g.Members {
					members[normUser(mem)] = true
				}
			}
		}
		each(func(m Machine) bool { return !m.Tagged() && members[normUser(m.User)] })
		return
	case "tag":
		each(func(m Machine) bool { return contains(m.Tags, alias) })
		return
	case "user":
		u := normUser(alias)
		each(func(m Machine) bool { return !m.Tagged() && normUser(m.User) == u })
		return
	}
	pfx, ok := e.prefix(alias)
	if !ok {
		u := normUser(alias)
		each(func(m Machine) bool { return !m.Tagged() && normUser(m.User) == u })
		return
	}
	hit := false
	each(func(m Machine) bool {
		for _, ip := range m.IPs {
			if pfx.Contains(ip) {
				hit = true
				return true
			}
		}
		return false
	})
	if !hit || pfx.Bits() < pfx.Addr().BitLen() {
		add(DestAddress, alias, nil)
	}
}

// result is the merged, sorted access list: machines by name, then the rest.
func (e *evaluator) result() []Access {
	out := make([]Access, 0, len(e.acc))
	for _, a := range e.acc {
		a.Ports = mergePorts(a.Ports)
		a.Lines = uniqInts(a.Lines)
		out = append(out, *a)
	}
	rank := map[string]int{DestMachine: 0, DestAddress: 1, DestInternet: 2, DestAny: 3}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return rank[out[i].Kind] < rank[out[j].Kind]
		}
		if out[i].Dest != out[j].Dest {
			return out[i].Dest < out[j].Dest
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// splitDst splits an ACL destination into alias and ports.
func splitDst(dst string) (alias, ports string) {
	alias = AliasOf(dst)
	switch {
	case strings.HasPrefix(dst, "[") && strings.Contains(dst, "]:"):
		ports = dst[strings.Index(dst, "]:")+2:]
	case len(alias) < len(dst):
		ports = dst[len(alias)+1:]
	default:
		ports = "*"
	}
	return alias, ports
}

// portSpec joins a protocol and a port range: "tcp:22", "443", "*".
func portSpec(proto, port string) string {
	if port == "" {
		port = "*"
	}
	proto = strings.ToLower(proto)
	if proto == "" || proto == "*" {
		return port
	}
	return proto + ":" + port
}

// mergePorts dedupes and sorts; any-protocol "*" swallows everything.
func mergePorts(ps []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range ps {
		if p == "*" {
			return []string{"*"}
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

func uniqInts(xs []int) []int {
	sort.Ints(xs)
	out := xs[:0]
	for i, x := range xs {
		if i == 0 || x != xs[i-1] {
			out = append(out, x)
		}
	}
	return out
}

func normUser(s string) string { return strings.TrimSuffix(strings.TrimSpace(s), "@") }

// Subjects lists every source the preview can start from: users (owners of
// untagged machines and users the policy names), tags (from tagOwners and
// machines), then machines.
func (d *Document) Subjects(machines []Machine) []Subject {
	users, tags := map[string]bool{}, map[string]bool{}
	for _, m := range machines {
		if m.Tagged() {
			for _, t := range m.Tags {
				tags[t] = true
			}
		} else if m.User != "" {
			users[normUser(m.User)] = true
		}
	}
	if d != nil {
		for _, u := range d.Names()["user"] {
			users[normUser(u)] = true
		}
		for _, t := range d.TagOwners {
			tags[t.Tag] = true
		}
	}
	var out []Subject
	for _, u := range sortedKeys(users) {
		out = append(out, Subject{Kind: SubjectUser, Name: u})
	}
	for _, t := range sortedKeys(tags) {
		out = append(out, Subject{Kind: SubjectTag, Name: t})
	}
	ms := append([]Machine(nil), machines...)
	sort.SliceStable(ms, func(i, j int) bool { return ms[i].Name < ms[j].Name })
	for _, m := range ms {
		out = append(out, Subject{Kind: SubjectMachine, Name: m.Name, ID: m.ID})
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
