package policy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/tailscale/hujson"
)

// Document is the parsed form of a HuJSON policy. Raw keeps the original text
// byte-for-byte; the typed sections are for display and cross-referencing,
// never for writing back (writing back would lose comments and layout).
type Document struct {
	Raw           string
	Groups        []Group
	TagOwners     []TagOwner
	Hosts         []HostAlias
	ACLs          []ACL
	Grants        []Grant
	SSH           []SSHRule
	AutoApprovers AutoApprovers
	Tests         []Test
	// Other lists top-level keys the model does not interpret (nodeAttrs, postures, ...).
	Other []string
}

// Group is a named set of users.
type Group struct {
	Name    string
	Members []string
	Line    int
}

// TagOwner says who may assign a tag.
type TagOwner struct {
	Tag    string
	Owners []string
	Line   int
}

// HostAlias names an address or prefix.
type HostAlias struct {
	Name string
	Addr string
	Line int
}

// ACL is one legacy "acls" rule.
type ACL struct {
	Action string   `json:"action"`
	Proto  string   `json:"proto,omitempty"`
	Src    []string `json:"src"`
	Dst    []string `json:"dst"`
	Line   int      `json:"-"`
}

// Grant is one "grants" rule. App capabilities are kept opaque.
type Grant struct {
	Src  []string        `json:"src"`
	Dst  []string        `json:"dst"`
	IP   []string        `json:"ip,omitempty"`
	Via  []string        `json:"via,omitempty"`
	App  json.RawMessage `json:"app,omitempty"`
	Line int             `json:"-"`
}

// SSHRule is one "ssh" rule.
type SSHRule struct {
	Action      string   `json:"action"`
	Src         []string `json:"src"`
	Dst         []string `json:"dst"`
	Users       []string `json:"users"`
	CheckPeriod string   `json:"checkPeriod,omitempty"`
	Line        int      `json:"-"`
}

// RouteApprover auto-approves one advertised prefix.
type RouteApprover struct {
	Prefix    string
	Approvers []string
	Line      int
}

// AutoApprovers lists routes and exit nodes approved without an admin.
type AutoApprovers struct {
	Routes   []RouteApprover
	ExitNode []string
	Line     int
}

// Test is one entry of the policy's own "tests".
type Test struct {
	Src    string   `json:"src"`
	Proto  string   `json:"proto,omitempty"`
	Accept []string `json:"accept,omitempty"`
	Deny   []string `json:"deny,omitempty"`
	Line   int      `json:"-"`
}

// ParseError is a policy that could not be read, with a 1-based position.
type ParseError struct {
	Line, Col int
	Msg       string
}

func (e *ParseError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("line %d, column %d: %s", e.Line, e.Col, e.Msg)
	}
	return e.Msg
}

var hujsonPos = regexp.MustCompile(`^hujson: line (\d+), column (\d+): (.*)$`)

func parseErr(err error) *ParseError {
	msg := err.Error()
	if m := hujsonPos.FindStringSubmatch(msg); m != nil {
		l, _ := strconv.Atoi(m[1])
		c, _ := strconv.Atoi(m[2])
		return &ParseError{Line: l, Col: c, Msg: m[3]}
	}
	return &ParseError{Msg: msg}
}

// RoundTrip parses raw as HuJSON and packs it again. The result is
// byte-identical to the input for every input that parses.
func RoundTrip(raw []byte) ([]byte, error) {
	v, err := hujson.Parse(raw)
	if err != nil {
		return nil, parseErr(err)
	}
	return v.Pack(), nil
}

// Parse reads a HuJSON policy (comments and trailing commas allowed) into a
// Document. Errors are *ParseError with the line and column when known.
func Parse(raw []byte) (*Document, error) {
	v, err := hujson.Parse(raw)
	if err != nil {
		return nil, parseErr(err)
	}
	p := &parser{raw: raw}
	obj, ok := v.Value.(*hujson.Object)
	if !ok {
		return nil, p.errAt(v.StartOffset, "policy must be a JSON object")
	}
	d := &Document{Raw: string(raw)}
	for i := range obj.Members {
		m := &obj.Members[i]
		name := m.Name.Value.(hujson.Literal).String()
		val := m.Value
		switch name {
		case "groups":
			err = p.eachMember(val, name, func(k string, line int, mv hujson.Value) error {
				g := Group{Name: k, Line: line}
				return p.decodeInto(mv, &g.Members, func() { d.Groups = append(d.Groups, g) })
			})
		case "tagOwners":
			err = p.eachMember(val, name, func(k string, line int, mv hujson.Value) error {
				t := TagOwner{Tag: k, Line: line}
				return p.decodeInto(mv, &t.Owners, func() { d.TagOwners = append(d.TagOwners, t) })
			})
		case "hosts":
			err = p.eachMember(val, name, func(k string, line int, mv hujson.Value) error {
				h := HostAlias{Name: k, Line: line}
				return p.decodeInto(mv, &h.Addr, func() { d.Hosts = append(d.Hosts, h) })
			})
		case "acls":
			err = p.eachElement(val, name, func(line int, ev hujson.Value) error {
				var a ACL
				return p.decodeInto(ev, &a, func() { a.Line = line; d.ACLs = append(d.ACLs, a) })
			})
		case "grants":
			err = p.eachElement(val, name, func(line int, ev hujson.Value) error {
				var g Grant
				return p.decodeInto(ev, &g, func() { g.Line = line; d.Grants = append(d.Grants, g) })
			})
		case "ssh":
			err = p.eachElement(val, name, func(line int, ev hujson.Value) error {
				var s SSHRule
				return p.decodeInto(ev, &s, func() { s.Line = line; d.SSH = append(d.SSH, s) })
			})
		case "tests":
			err = p.eachElement(val, name, func(line int, ev hujson.Value) error {
				var t Test
				return p.decodeInto(ev, &t, func() { t.Line = line; d.Tests = append(d.Tests, t) })
			})
		case "autoApprovers":
			err = p.autoApprovers(val, &d.AutoApprovers)
		default:
			d.Other = append(d.Other, name)
		}
		if err != nil {
			return nil, err
		}
	}
	return d, nil
}

type parser struct{ raw []byte }

func (p *parser) pos(off int) (line, col int) {
	off = max(0, min(off, len(p.raw)))
	line = 1 + bytes.Count(p.raw[:off], []byte("\n"))
	col = 1 + off - (bytes.LastIndexByte(p.raw[:off], '\n') + 1)
	return line, col
}

func (p *parser) line(off int) int { l, _ := p.pos(off); return l }

func (p *parser) errAt(off int, format string, args ...any) *ParseError {
	l, c := p.pos(off)
	return &ParseError{Line: l, Col: c, Msg: fmt.Sprintf(format, args...)}
}

// decodeInto unmarshals a HuJSON value into out and calls ok on success.
func (p *parser) decodeInto(v hujson.Value, out any, ok func()) error {
	c := v.Clone()
	c.Standardize()
	if err := json.Unmarshal(c.Pack(), out); err != nil {
		msg := err.Error()
		if te, isType := err.(*json.UnmarshalTypeError); isType {
			msg = fmt.Sprintf("expected %s, found %s", jsonKind(te.Type.Kind().String()), te.Value)
			if te.Field != "" {
				msg = fmt.Sprintf("%q: %s", te.Field, msg)
			}
		}
		return p.errAt(v.StartOffset, "%s", msg)
	}
	ok()
	return nil
}

func jsonKind(k string) string {
	switch k {
	case "slice":
		return "a list"
	case "string":
		return "a string"
	case "struct", "map":
		return "an object"
	}
	return k
}

func (p *parser) eachMember(v hujson.Value, section string, f func(k string, line int, mv hujson.Value) error) error {
	obj, ok := v.Value.(*hujson.Object)
	if !ok {
		return p.errAt(v.StartOffset, "%q must be an object", section)
	}
	for _, m := range obj.Members {
		if err := f(m.Name.Value.(hujson.Literal).String(), p.line(m.Name.StartOffset), m.Value); err != nil {
			return err
		}
	}
	return nil
}

func (p *parser) eachElement(v hujson.Value, section string, f func(line int, ev hujson.Value) error) error {
	arr, ok := v.Value.(*hujson.Array)
	if !ok {
		return p.errAt(v.StartOffset, "%q must be a list", section)
	}
	for _, e := range arr.Elements {
		if _, isObj := e.Value.(*hujson.Object); !isObj {
			return p.errAt(e.StartOffset, "each entry of %q must be an object", section)
		}
		if err := f(p.line(e.StartOffset), e); err != nil {
			return err
		}
	}
	return nil
}

func (p *parser) autoApprovers(v hujson.Value, out *AutoApprovers) error {
	out.Line = p.line(v.StartOffset)
	return p.eachMember(v, "autoApprovers", func(k string, line int, mv hujson.Value) error {
		switch k {
		case "routes":
			return p.eachMember(mv, "routes", func(prefix string, line int, rv hujson.Value) error {
				r := RouteApprover{Prefix: prefix, Line: line}
				return p.decodeInto(rv, &r.Approvers, func() { out.Routes = append(out.Routes, r) })
			})
		case "exitNode":
			return p.decodeInto(mv, &out.ExitNode, func() {})
		}
		return nil
	})
}

// --- cross-references ---------------------------------------------------------

// Ref is one place in the policy that mentions a name.
type Ref struct {
	Section string // "groups", "tagOwners", "acls", "grants", "ssh", "autoApprovers", "tests", "hosts"
	Index   int    // entry index within the section
	Line    int
	Role    string // "src", "dst", "member", "owner", "approver", "users", "definition"
	Summary string
}

// AliasOf returns the alias part of an ACL destination ("tag:web:80,443" ->
// "tag:web", "[fd7a::1]:22" -> "fd7a::1", "*:*" -> "*").
func AliasOf(dst string) string {
	if strings.HasPrefix(dst, "[") {
		if i := strings.Index(dst, "]"); i > 0 {
			return dst[1:i]
		}
	}
	if i := strings.LastIndex(dst, ":"); i > 0 {
		return dst[:i]
	}
	return dst
}

// WhereUsed lists every rule and definition that mentions name (a group,
// tag, host alias, user, or autogroup), in document order by section.
func (d *Document) WhereUsed(name string) []Ref {
	if d == nil || name == "" {
		return nil
	}
	var out []Ref
	add := func(sec string, i, line int, role, sum string) {
		out = append(out, Ref{Section: sec, Index: i, Line: line, Role: role, Summary: sum})
	}
	for i, g := range d.Groups {
		if g.Name == name {
			add("groups", i, g.Line, "definition", g.Name)
		}
		if contains(g.Members, name) {
			add("groups", i, g.Line, "member", g.Name)
		}
	}
	for i, t := range d.TagOwners {
		if t.Tag == name {
			add("tagOwners", i, t.Line, "definition", t.Tag)
		}
		if contains(t.Owners, name) {
			add("tagOwners", i, t.Line, "owner", t.Tag)
		}
	}
	for i, h := range d.Hosts {
		if h.Name == name {
			add("hosts", i, h.Line, "definition", h.Name+" = "+h.Addr)
		}
	}
	for i, a := range d.ACLs {
		if contains(a.Src, name) {
			add("acls", i, a.Line, "src", a.Summary())
		}
		for _, x := range a.Dst {
			if AliasOf(x) == name {
				add("acls", i, a.Line, "dst", a.Summary())
				break
			}
		}
	}
	for i, g := range d.Grants {
		if contains(g.Src, name) {
			add("grants", i, g.Line, "src", g.Summary())
		}
		if contains(g.Dst, name) {
			add("grants", i, g.Line, "dst", g.Summary())
		}
	}
	for i, s := range d.SSH {
		if contains(s.Src, name) {
			add("ssh", i, s.Line, "src", s.Summary())
		}
		if contains(s.Dst, name) {
			add("ssh", i, s.Line, "dst", s.Summary())
		}
		if contains(s.Users, name) {
			add("ssh", i, s.Line, "users", s.Summary())
		}
	}
	for i, r := range d.AutoApprovers.Routes {
		if contains(r.Approvers, name) {
			add("autoApprovers", i, r.Line, "approver", "route "+r.Prefix)
		}
	}
	if contains(d.AutoApprovers.ExitNode, name) {
		add("autoApprovers", len(d.AutoApprovers.Routes), d.AutoApprovers.Line, "approver", "exit node")
	}
	for i, t := range d.Tests {
		if t.Src == name {
			add("tests", i, t.Line, "src", t.Summary())
		}
		for _, x := range append(slicesClone(t.Accept), t.Deny...) {
			if AliasOf(x) == name {
				add("tests", i, t.Line, "dst", t.Summary())
				break
			}
		}
	}
	return out
}

// Names lists every alias the policy defines or mentions, grouped by kind:
// "group", "tag", "host", "user", "autogroup". Each list keeps first-seen order.
func (d *Document) Names() map[string][]string {
	out := map[string][]string{}
	seen := map[string]bool{}
	note := func(s string) {
		if s == "" || s == "*" || seen[s] {
			return
		}
		kind := KindOf(s)
		if kind == "" {
			return
		}
		seen[s] = true
		out[kind] = append(out[kind], s)
	}
	for _, g := range d.Groups {
		note(g.Name)
	}
	for _, t := range d.TagOwners {
		note(t.Tag)
	}
	for _, h := range d.Hosts {
		note(h.Name)
	}
	hostNames := map[string]bool{}
	for _, h := range d.Hosts {
		hostNames[h.Name] = true
	}
	all := func(xs []string, dst bool) {
		for _, x := range xs {
			if dst {
				x = AliasOf(x)
			}
			if KindOf(x) == "" && hostNames[x] {
				continue
			}
			note(x)
		}
	}
	for _, g := range d.Groups {
		all(g.Members, false)
	}
	for _, t := range d.TagOwners {
		all(t.Owners, false)
	}
	for _, a := range d.ACLs {
		all(a.Src, false)
		all(a.Dst, true)
	}
	for _, g := range d.Grants {
		all(g.Src, false)
		all(g.Dst, false)
	}
	for _, s := range d.SSH {
		all(s.Src, false)
		all(s.Dst, false)
	}
	return out
}

// KindOf classifies an alias. Host aliases and bare addresses return "host"
// only when they are not obviously something else; "" means unclassified.
func KindOf(s string) string {
	switch {
	case strings.HasPrefix(s, "group:"):
		return "group"
	case strings.HasPrefix(s, "tag:"):
		return "tag"
	case strings.HasPrefix(s, "autogroup:"):
		return "autogroup"
	case strings.Contains(s, "@"):
		return "user"
	case s == "" || s == "*":
		return ""
	}
	return "host"
}

// Summary is a one-line description of the rule.
func (a ACL) Summary() string {
	s := fmt.Sprintf("%s %s -> %s", orDefault(a.Action, "accept"), strings.Join(a.Src, ","), strings.Join(a.Dst, ","))
	if a.Proto != "" {
		s += " (" + a.Proto + ")"
	}
	return s
}

// Summary is a one-line description of the grant.
func (g Grant) Summary() string {
	s := fmt.Sprintf("grant %s -> %s", strings.Join(g.Src, ","), strings.Join(g.Dst, ","))
	if len(g.IP) > 0 {
		s += " ip " + strings.Join(g.IP, ",")
	}
	if len(g.App) > 0 {
		s += " +app"
	}
	return s
}

// Summary is a one-line description of the SSH rule.
func (s SSHRule) Summary() string {
	return fmt.Sprintf("ssh %s %s -> %s as %s", orDefault(s.Action, "accept"), strings.Join(s.Src, ","),
		strings.Join(s.Dst, ","), strings.Join(s.Users, ","))
}

// Summary is a one-line description of the test.
func (t Test) Summary() string {
	s := "test " + t.Src
	if len(t.Accept) > 0 {
		s += " accept " + strings.Join(t.Accept, ",")
	}
	if len(t.Deny) > 0 {
		s += " deny " + strings.Join(t.Deny, ",")
	}
	return s
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func slicesClone(xs []string) []string { return append([]string(nil), xs...) }

// Check is the local fallback validation used when the server cannot check a
// policy: it must parse, and every group, tag, and host it uses must be
// defined. The error is a *ParseError with a position.
func Check(raw []byte) error {
	d, err := Parse(raw)
	if err != nil {
		return err
	}
	defined := map[string]bool{}
	for _, g := range d.Groups {
		defined[g.Name] = true
	}
	for _, t := range d.TagOwners {
		defined[t.Tag] = true
	}
	for _, h := range d.Hosts {
		defined[h.Name] = true
	}
	undefined := func(x string) bool {
		k := KindOf(x)
		return (k == "group" || k == "tag") && !defined[x]
	}
	for _, a := range d.ACLs {
		for _, s := range a.Src {
			if undefined(s) {
				return &ParseError{Line: a.Line, Col: 1, Msg: fmt.Sprintf("%s is not defined", s)}
			}
		}
		for _, x := range a.Dst {
			if undefined(AliasOf(x)) {
				return &ParseError{Line: a.Line, Col: 1, Msg: fmt.Sprintf("%s is not defined", AliasOf(x))}
			}
		}
	}
	for _, s := range d.SSH {
		for _, x := range append(slicesClone(s.Src), s.Dst...) {
			if undefined(x) {
				return &ParseError{Line: s.Line, Col: 1, Msg: fmt.Sprintf("%s is not defined", x)}
			}
		}
	}
	return nil
}
