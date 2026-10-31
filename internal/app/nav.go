package app

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/ui/help"
	"github.com/tonhe/headsup/internal/ui/sidebar"
)

// Two-level navigation. Modules are grouped into sections (module.SectionOf);
// a section's digit (app.section.<slug>, default "1", "2", "3") enters jump
// mode, and the next key picks a page: the page's letter (its "g x" chord
// letter, or a fallback from its title), its position as a digit, enter or
// the section digit again for the first page. Any other key cancels and is
// swallowed. The "g x" chords and the palette work as before.

// navSection is one section as currently shown: its visible pages in
// order, with the key that picks each one during a jump.
type navSection struct {
	sec   module.Section
	key   string // the digit that starts the jump ("1")
	mods  []int  // module indexes, in sidebar order
	pkeys []string
}

// hidden reports whether a module is left out of navigation: host-only
// pages in remote mode. Its "g x" chord and palette entry still work and
// explain that the page needs host mode.
func (m *Model) hidden(mod module.Module) bool {
	return mod.Modes() == module.ModeHost && !m.HostMode()
}

func sectionActionID(s module.Section) string { return "app.section." + s.Slug() }

// navSections lists the non-empty sections with their visible pages and
// page keys.
func (m *Model) navSections() []navSection {
	var out []navSection
	for _, sec := range module.Sections {
		ns := navSection{sec: sec}
		if ks := m.keymap.Keys(sectionActionID(sec)); len(ks) > 0 {
			ns.key = ks[0]
		}
		for i, mod := range m.modules {
			if module.SectionOf(mod) == sec && !m.hidden(mod) {
				ns.mods = append(ns.mods, i)
			}
		}
		if len(ns.mods) == 0 {
			continue
		}
		sort.SliceStable(ns.mods, func(a, b int) bool {
			ma, mb := m.modules[ns.mods[a]], m.modules[ns.mods[b]]
			if ma.Order() != mb.Order() {
				return ma.Order() < mb.Order()
			}
			return ma.ID() < mb.ID()
		})
		ns.pkeys = m.pageKeys(ns.mods)
		out = append(out, ns)
	}
	return out
}

// pageKeys picks each page's key within its section, deterministically in
// sidebar order: the letter of its live "g x" chord, else its Chord(), else
// the first free letter of its title (lower case first). A page whose
// candidates are all taken gets no letter and is picked by digit or click.
func (m *Model) pageKeys(mods []int) []string {
	used := map[string]bool{"enter": true, "esc": true}
	out := make([]string, len(mods))
	for n, i := range mods {
		mod := m.modules[i]
		var cands []string
		if k := m.chordLetter(mod.ID()); k != "" {
			cands = append(cands, k)
		}
		if c, ok := mod.(Chorder); ok && c.Chord() != "" {
			cands = append(cands, c.Chord())
		}
		for _, r := range strings.ToLower(mod.Title()) + mod.Title() {
			if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
				cands = append(cands, string(r))
			}
		}
		for _, c := range cands {
			if utf8.RuneCountInString(c) != 1 || (c[0] >= '0' && c[0] <= '9') || used[c] {
				continue
			}
			out[n], used[c] = c, true
			break
		}
	}
	return out
}

// chordLetter is the second key of the module's live "g x" binding, if any.
func (m *Model) chordLetter(id string) string {
	for _, k := range m.keymap.Keys("app.goto." + id) {
		if f := strings.Fields(k); len(f) == 2 && f[0] == "g" {
			return f[1]
		}
	}
	return ""
}

// chordLabel is the module's direct chord for display ("g m"), if any.
func (m *Model) chordLabel(id string) string {
	for _, k := range m.keymap.Keys("app.goto." + id) {
		if strings.Contains(k, " ") {
			return k
		}
	}
	return ""
}

func (m *Model) jumpSection() (navSection, int, bool) {
	for i, ns := range m.navSections() {
		if ns.sec == m.jump {
			return ns, i, true
		}
	}
	return navSection{}, -1, false
}

// startJump enters jump mode for a section; an empty section does nothing.
func (m *Model) startJump(sec module.Section) tea.Cmd {
	m.jump = sec
	if _, _, ok := m.jumpSection(); !ok {
		m.jump = 0
	}
	return nil
}

// jumpKey handles the key after a section digit. It always consumes the
// key: a page key navigates, anything else just cancels.
func (m *Model) jumpKey(key string) tea.Cmd {
	ns, _, ok := m.jumpSection()
	m.jump = 0
	if !ok {
		return nil
	}
	switch {
	case key == "enter" || key == ns.key:
		return m.Navigate(m.modules[ns.mods[0]].ID(), nil)
	case len(key) == 1 && key[0] >= '1' && key[0] <= '9':
		if n := int(key[0] - '1'); n < len(ns.mods) {
			return m.Navigate(m.modules[ns.mods[n]].ID(), nil)
		}
		return nil
	}
	for n, k := range ns.pkeys {
		if k != "" && k == key {
			return m.Navigate(m.modules[ns.mods[n]].ID(), nil)
		}
	}
	return nil
}

// navGroups is the sidebar content; jump is the index of the pending
// section's group, or -1.
func (m *Model) navGroups() (groups []sidebar.Group, jump int) {
	jump = -1
	for gi, ns := range m.navSections() {
		g := sidebar.Group{Key: ns.key, Title: ns.sec.Title()}
		for n, i := range ns.mods {
			mod := m.modules[i]
			g.Items = append(g.Items, sidebar.Item{
				ID:    mod.ID(),
				Title: mod.Title(),
				Icon:  mod.Icon(m.icons),
				Badge: mod.Badge(m.store),
				Key:   ns.pkeys[n],
				Chord: m.chordLabel(mod.ID()),
			})
		}
		if ns.sec == m.jump {
			jump = gi
		}
		groups = append(groups, g)
	}
	return groups, jump
}

// tabItems is the tab bar content (narrow terminals, or the sidebar
// toggled off): every visible page, or during a jump only the pending
// section's pages, each labelled with its key.
func (m *Model) tabItems() []sidebar.Item {
	groups, jump := m.navGroups()
	var items []sidebar.Item
	for gi, g := range groups {
		if jump >= 0 && gi != jump {
			continue
		}
		for _, it := range g.Items {
			if jump < 0 {
				it.Key = ""
			} else if it.Key == "" {
				it.Key = " "
			}
			items = append(items, it)
		}
	}
	return items
}

// visibleOrder is the module indexes in navigation order, for ] and [.
func (m *Model) visibleOrder() []int {
	var out []int
	for _, ns := range m.navSections() {
		out = append(out, ns.mods...)
	}
	return out
}

// cycle moves to the next (+1) or previous (-1) visible page.
func (m *Model) cycle(dir int) {
	order := m.visibleOrder()
	if len(order) == 0 {
		return
	}
	pos := -1
	for p, i := range order {
		if i == m.active {
			pos = p
		}
	}
	if pos < 0 {
		if dir < 0 {
			pos = 0
		} else {
			pos = len(order) - 1
		}
	}
	m.active = order[(pos+dir+len(order))%len(order)]
}

// jumpHints is the which-key status line content while a jump is pending.
func (m *Model) jumpHints() (lead module.Hint, items []module.Hint, ok bool) {
	ns, _, ok := m.jumpSection()
	if !ok {
		return lead, nil, false
	}
	lead = module.Hint{Key: ns.key, Desc: ns.sec.Title()}
	for n, i := range ns.mods {
		k := ns.pkeys[n]
		if k == "" {
			k = fmt.Sprint(n + 1)
		}
		items = append(items, module.Hint{Key: k, Desc: m.modules[i].Title()})
	}
	return lead, items, true
}

// navHelp is the help screen's navigation sections, from the same data as
// the sidebar.
func (m *Model) navHelp() []help.Section {
	secs := m.navSections()
	var digits []string
	for _, ns := range secs {
		digits = append(digits, ns.key+" "+ns.sec.Title())
	}
	nav := help.Section{Title: "Navigation"}
	if len(secs) > 0 {
		first := secs[0]
		nav.Keys = append(nav.Keys,
			module.Hint{Key: joinKeys(secs), Desc: "Pick a section (" + strings.Join(digits, ", ") + "), then a page key"},
			module.Hint{Key: first.key + " " + first.key + " / " + first.key + " enter", Desc: "First page of the section"},
			module.Hint{Key: first.key + " 1-9", Desc: "Nth page of the section (its own digit: first page)"},
			module.Hint{Key: "esc", Desc: "Cancel a section jump (any other key does too)"},
			module.Hint{Key: "g <key>", Desc: "Go straight to a page (shown dimmed in the sidebar)"},
		)
	}
	if k := m.keyLabel("app.next"); k != "" {
		nav.Keys = append(nav.Keys, module.Hint{Key: k + " / " + m.keyLabel("app.prev"), Desc: "Next / previous page"})
	}
	nav.Keys = append(nav.Keys, module.Hint{Key: "j / k  g / G", Desc: "Move, top, bottom"})
	nav.Keys = append(nav.Keys, module.Hint{Key: "ctrl+d / ctrl+u", Desc: "Half page down / up"})
	out := []help.Section{nav}

	for _, ns := range secs {
		sec := help.Section{Title: "Go to: " + ns.key + " " + ns.sec.Title()}
		for n, i := range ns.mods {
			mod := m.modules[i]
			k := ns.key + " " + ns.pkeys[n]
			if ns.pkeys[n] == "" {
				k = ns.key + " " + fmt.Sprint(n+1)
			}
			if c := m.otherGotoKeys(mod.ID()); c != "" {
				k += "  ·  " + c
			}
			sec.Keys = append(sec.Keys, module.Hint{Key: k, Desc: mod.Title()})
		}
		out = append(out, sec)
	}
	// Pages left out of the sidebar (host-only in remote mode) keep their chords.
	var rest help.Section
	for _, mod := range m.modules {
		if m.hidden(mod) {
			if c := m.otherGotoKeys(mod.ID()); c != "" {
				rest.Keys = append(rest.Keys, module.Hint{Key: c, Desc: mod.Title() + " (host mode)"})
			}
		}
	}
	if len(rest.Keys) > 0 {
		rest.Title = "Go to: host-only pages"
		out = append(out, rest)
	}
	return out
}

func joinKeys(secs []navSection) string {
	ks := make([]string, len(secs))
	for i, ns := range secs {
		ks[i] = ns.key
	}
	return strings.Join(ks, " / ")
}

// otherGotoKeys are a page's direct bindings ("g m", ",") for help.
func (m *Model) otherGotoKeys(id string) string {
	return strings.Join(m.keymap.Keys("app.goto."+id), " / ")
}
