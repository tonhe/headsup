package policy

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tonhe/headsup/internal/module"
	pol "github.com/tonhe/headsup/internal/policy"
	"github.com/tonhe/headsup/internal/ui/pane"
)

// expItem is one row of the explorer tree: a section header, a named entity
// (group, tag, host, user, autogroup) that has where-used links, or a rule.
type expItem struct {
	header string
	name   string // entity name; "" for rules
	label  string
	line   int
	names  []string // for rules: the names the rule mentions
}

type explorer struct {
	src    string
	items  []expItem
	cursor int
	offset int
}

func (m *Model) openExplore() {
	if m.exp.src != m.cur || m.exp.items == nil {
		m.exp = explorer{src: m.cur, items: buildTree(m.doc)}
		m.exp.cursor = m.exp.next(-1, 1)
	}
	m.back, m.mode = m.mode, modeExplore
}

func buildTree(d *pol.Document) []expItem {
	if d == nil {
		return nil
	}
	var out []expItem
	section := func(title string, n int) {
		if n > 0 {
			out = append(out, expItem{header: fmt.Sprintf("%s (%d)", title, n)})
		}
	}
	firstLine := func(name string) int {
		if refs := d.WhereUsed(name); len(refs) > 0 {
			return refs[0].Line
		}
		return 0
	}
	names := d.Names()

	section("Groups", len(d.Groups))
	for _, g := range d.Groups {
		out = append(out, expItem{name: g.Name, label: g.Name + "  " + strings.Join(g.Members, ", "), line: g.Line})
	}
	section("Tags", len(d.TagOwners))
	for _, t := range d.TagOwners {
		out = append(out, expItem{name: t.Tag, label: t.Tag + "  owners: " + strings.Join(t.Owners, ", "), line: t.Line})
	}
	section("Hosts", len(d.Hosts))
	for _, h := range d.Hosts {
		out = append(out, expItem{name: h.Name, label: h.Name + "  " + h.Addr, line: h.Line})
	}
	section("Users", len(names["user"]))
	for _, u := range names["user"] {
		out = append(out, expItem{name: u, label: u, line: firstLine(u)})
	}
	section("Autogroups", len(names["autogroup"]))
	for _, a := range names["autogroup"] {
		out = append(out, expItem{name: a, label: a, line: firstLine(a)})
	}
	section("ACL rules", len(d.ACLs))
	for i, a := range d.ACLs {
		var ns []string
		for _, x := range a.Dst {
			ns = append(ns, pol.AliasOf(x))
		}
		out = append(out, expItem{label: fmt.Sprintf("#%d %s", i+1, a.Summary()), line: a.Line, names: append(append([]string(nil), a.Src...), ns...)})
	}
	section("Grants", len(d.Grants))
	for i, g := range d.Grants {
		out = append(out, expItem{label: fmt.Sprintf("#%d %s", i+1, g.Summary()), line: g.Line, names: append(append([]string(nil), g.Src...), g.Dst...)})
	}
	section("SSH rules", len(d.SSH))
	for i, s := range d.SSH {
		out = append(out, expItem{label: fmt.Sprintf("#%d %s", i+1, s.Summary()), line: s.Line, names: append(append([]string(nil), s.Src...), s.Dst...)})
	}
	aa := d.AutoApprovers
	n := len(aa.Routes)
	if len(aa.ExitNode) > 0 {
		n++
	}
	section("Auto-approvers", n)
	for _, r := range aa.Routes {
		out = append(out, expItem{label: "route " + r.Prefix + " ← " + strings.Join(r.Approvers, ", "), line: r.Line, names: r.Approvers})
	}
	if len(aa.ExitNode) > 0 {
		out = append(out, expItem{label: "exit node ← " + strings.Join(aa.ExitNode, ", "), line: aa.Line, names: aa.ExitNode})
	}
	section("Tests", len(d.Tests))
	for i, t := range d.Tests {
		out = append(out, expItem{label: fmt.Sprintf("#%d %s", i+1, t.Summary()), line: t.Line, names: []string{t.Src}})
	}
	return out
}

// next finds the next selectable row from i in direction dir, or i if none.
func (e *explorer) next(i, dir int) int {
	for j := i + dir; j >= 0 && j < len(e.items); j += dir {
		if e.items[j].header == "" {
			return j
		}
	}
	if i < 0 {
		return 0
	}
	return i
}

func (e *explorer) selected() (expItem, bool) {
	if e.cursor >= 0 && e.cursor < len(e.items) && e.items[e.cursor].header == "" {
		return e.items[e.cursor], true
	}
	return expItem{}, false
}

func (m *Model) exploreKey(ctx module.Context, k tea.KeyMsg) (tea.Cmd, bool) {
	e := &m.exp
	switch k.String() {
	case "j", "down":
		e.cursor = e.next(e.cursor, 1)
	case "k", "up":
		e.cursor = e.next(e.cursor, -1)
	case "enter":
		if it, ok := e.selected(); ok && it.line > 0 {
			m.mode, m.cursor, m.offset = modeView, it.line, max(0, it.line-6)
		}
	case "m":
		it, ok := e.selected()
		if !ok {
			return nil, true
		}
		switch pol.KindOf(it.name) {
		case "tag":
			return ctx.Navigate("machines", module.FilterArgs{Query: it.name}), true
		case "user":
			return ctx.Navigate("machines", module.FilterArgs{Query: "user:" + strings.TrimSuffix(it.name, "@")}), true
		}
		return ctx.Toast(module.ToastInfo, "Pick a tag or a user to list its machines."), true
	default:
		return nil, false
	}
	return nil, true
}

func (m *Model) exploreView(ctx module.Context, w, h int) string {
	s := ctx.Styles()
	e := &m.exp
	side := w >= 100
	treeW, treeH := w, h
	if side {
		treeW = w * 11 / 20
	} else {
		treeH = max(6, h*3/5)
	}
	iw, ih := pane.Inner(treeW, treeH)
	if e.cursor < e.offset {
		e.offset = max(0, e.cursor-1)
	}
	if e.cursor >= e.offset+ih {
		e.offset = e.cursor - ih + 1
	}
	var rows []string
	for i := e.offset; i < min(len(e.items), e.offset+ih); i++ {
		it := e.items[i]
		switch {
		case it.header != "":
			rows = append(rows, s.TableHeader.Render(it.header))
		case i == e.cursor:
			rows = append(rows, s.RowSel.Render(pane.Fit("  "+it.label, iw)))
		default:
			rows = append(rows, "  "+it.label)
		}
	}
	if len(e.items) == 0 {
		rows = append(rows, s.Muted.Render("The policy is empty."))
	}
	tree := pane.Render(s, "Explore", strings.Join(rows, "\n"), treeW, treeH, true,
		pane.WithRight(fmt.Sprintf("%d items", len(e.items))))

	dw, dh := w, h-treeH
	if side {
		dw, dh = w-treeW, h
	}
	title, body := m.whereUsed(ctx, dw)
	detail := pane.Render(s, title, body, dw, dh, false)
	if side {
		return lipgloss.JoinHorizontal(lipgloss.Top, tree, detail)
	}
	return lipgloss.JoinVertical(lipgloss.Left, tree, detail)
}

// whereUsed is the cross-link panel for the selected row.
func (m *Model) whereUsed(ctx module.Context, w int) (string, string) {
	s := ctx.Styles()
	it, ok := m.exp.selected()
	if !ok {
		return "Where used", ""
	}
	if it.name == "" {
		lines := []string{s.Strong.Render(it.label), s.Muted.Render(fmt.Sprintf("line %d · enter shows it in the policy", it.line)), ""}
		if len(it.names) > 0 {
			lines = append(lines, s.Muted.Render("Mentions:"))
			seen := map[string]bool{}
			for _, n := range it.names {
				if seen[n] || n == "*" {
					continue
				}
				seen[n] = true
				uses := len(m.doc.WhereUsed(n))
				lines = append(lines, fmt.Sprintf("  %-24s %s", n, s.Muted.Render(fmt.Sprintf("%s, used in %d place(s)", orKind(n), uses))))
			}
		}
		return "Rule", strings.Join(lines, "\n")
	}
	refs := m.doc.WhereUsed(it.name)
	lines := []string{s.Muted.Render(fmt.Sprintf("%s is mentioned in %d place(s):", it.name, len(refs))), ""}
	for _, r := range refs {
		lines = append(lines, fmt.Sprintf("%s %s  %s",
			s.Accent.Render(fmt.Sprintf("%4d", r.Line)), s.Tag.Render(r.Section+"."+r.Role), r.Summary))
	}
	if k := pol.KindOf(it.name); k == "tag" || k == "user" {
		lines = append(lines, "", s.Muted.Render("m lists its machines"))
	}
	return "Where used: " + it.name, strings.Join(lines, "\n")
}

func orKind(n string) string {
	if k := pol.KindOf(n); k != "" {
		return k
	}
	return "address"
}
