// Package palette is the fuzzy command palette: a query line over a list of
// commands (module actions, "go to" jumps, themes, app commands). It knows
// nothing about where the commands come from; the app builds the items.
package palette

import (
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/sahilm/fuzzy"

	"github.com/tonhe/headsup/internal/icons"
	"github.com/tonhe/headsup/internal/ui/pane"
	"github.com/tonhe/headsup/internal/ui/styles"
)

// Item is one command in the palette.
type Item struct {
	ID    string // returned in ChosenMsg
	Title string // "Expire machine"
	Group string // "Machines", "Go to", "Theme"
	Keys  string // "e" or "1 / g m"; shown right-aligned
	// Disabled items are listed but dimmed; choosing one still emits
	// ChosenMsg so the caller can explain why (the gate's toast).
	Disabled bool
	Reason   string
}

func (it Item) label() string {
	if it.Group == "" {
		return it.Title
	}
	return it.Group + ": " + it.Title
}

// ChosenMsg is emitted when the user picks an item.
type ChosenMsg struct{ ID string }

// CloseMsg is emitted when the palette is dismissed without a choice.
type CloseMsg struct{}

// Model is the palette state.
type Model struct {
	s       *styles.Styles
	ic      icons.Set
	items   []Item
	recent  map[string]int // id -> rank (0 = most recent)
	query   string
	matches []int // indexes into items, in display order
	cursor  int
	offset  int
	w, h    int
}

// New builds a palette over items. recent lists item IDs, most recent first;
// with an empty query those come first.
func New(s *styles.Styles, ic icons.Set, items []Item, recent []string) *Model {
	m := &Model{s: s, ic: ic, items: items, recent: map[string]int{}}
	for i, id := range recent {
		if _, dup := m.recent[id]; !dup {
			m.recent[id] = i
		}
	}
	m.refilter()
	return m
}

// SetSize sets the outer box size.
func (m *Model) SetSize(w, h int) { m.w, m.h = w, h; m.clamp() }

// Query returns the current query text.
func (m *Model) Query() string { return m.query }

// SetQuery replaces the query.
func (m *Model) SetQuery(q string) { m.query = q; m.refilter() }

// Matches returns the visible items in display order.
func (m *Model) Matches() []Item {
	out := make([]Item, len(m.matches))
	for i, ix := range m.matches {
		out[i] = m.items[ix]
	}
	return out
}

// Selected returns the item under the cursor.
func (m *Model) Selected() (Item, bool) {
	if m.cursor < 0 || m.cursor >= len(m.matches) {
		return Item{}, false
	}
	return m.items[m.matches[m.cursor]], true
}

type labels []Item

func (l labels) String(i int) string { return l[i].label() + " " + l[i].Keys }
func (l labels) Len() int            { return len(l) }

func (m *Model) refilter() {
	m.cursor, m.offset = 0, 0
	m.matches = m.matches[:0]
	q := strings.TrimSpace(m.query)
	if q == "" {
		for i := range m.items {
			m.matches = append(m.matches, i)
		}
		sort.SliceStable(m.matches, func(a, b int) bool {
			ra, oka := m.recent[m.items[m.matches[a]].ID]
			rb, okb := m.recent[m.items[m.matches[b]].ID]
			switch {
			case oka && okb:
				return ra < rb
			default:
				return oka && !okb
			}
		})
		return
	}
	res := fuzzy.FindFrom(q, labels(m.items))
	// Recent items get a boost, so a repeated command floats to the top.
	score := func(r fuzzy.Match) int {
		s := r.Score
		if rk, ok := m.recent[m.items[r.Index].ID]; ok {
			s += 20 - min(rk, 10)
		}
		return s
	}
	sort.SliceStable(res, func(a, b int) bool { return score(res[a]) > score(res[b]) })
	for _, r := range res {
		m.matches = append(m.matches, r.Index)
	}
}

func (m *Model) listH() int { return max(1, m.h-4) } // border x2, query, rule

func (m *Model) clamp() {
	n := len(m.matches)
	m.cursor = max(0, min(m.cursor, n-1))
	lh := m.listH()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+lh {
		m.offset = m.cursor - lh + 1
	}
	m.offset = max(0, min(m.offset, max(0, n-lh)))
}

// Update handles typing, movement, enter and esc. Mouse wheel scrolls.
func (m *Model) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.MouseMsg:
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			m.cursor--
		case tea.MouseButtonWheelDown:
			m.cursor++
		}
		m.clamp()
		return nil
	case tea.KeyMsg:
		return m.key(msg)
	}
	return nil
}

func (m *Model) key(k tea.KeyMsg) tea.Cmd {
	switch k.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		if m.query != "" && k.Type == tea.KeyEsc {
			m.SetQuery("")
			return nil
		}
		return func() tea.Msg { return CloseMsg{} }
	case tea.KeyEnter:
		it, ok := m.Selected()
		if !ok {
			return nil
		}
		return func() tea.Msg { return ChosenMsg{ID: it.ID} }
	case tea.KeyUp, tea.KeyCtrlP, tea.KeyCtrlK, tea.KeyShiftTab:
		m.cursor--
	case tea.KeyDown, tea.KeyCtrlN, tea.KeyCtrlJ, tea.KeyTab:
		m.cursor++
	case tea.KeyPgUp:
		m.cursor -= m.listH()
	case tea.KeyPgDown:
		m.cursor += m.listH()
	case tea.KeyHome:
		m.cursor = 0
	case tea.KeyEnd:
		m.cursor = len(m.matches) - 1
	case tea.KeyBackspace:
		if m.query != "" {
			_, n := utf8.DecodeLastRuneInString(m.query)
			m.SetQuery(m.query[:len(m.query)-n])
		}
		return nil
	case tea.KeyCtrlU:
		m.SetQuery("")
		return nil
	case tea.KeyCtrlW:
		q := strings.TrimRightFunc(m.query, unicode.IsSpace)
		if i := strings.LastIndexFunc(q, unicode.IsSpace); i >= 0 {
			m.SetQuery(q[:i+1])
		} else {
			m.SetQuery("")
		}
		return nil
	case tea.KeySpace:
		m.SetQuery(m.query + " ")
		return nil
	case tea.KeyRunes:
		m.SetQuery(m.query + string(k.Runes))
		return nil
	}
	m.clamp()
	return nil
}

// View renders the palette box at exactly w x h.
func (m *Model) View() string {
	if m.w < 10 || m.h < 5 {
		return ""
	}
	iw, _ := pane.Inner(m.w, m.h)
	prompt := m.s.Accent.Render(": ")
	q := m.s.Text.Render(m.query) + m.s.Accent.Render("▏")
	if m.query == "" {
		q = m.s.Accent.Render("▏") + m.s.Muted.Render("type a command, a module, or a theme")
	}
	lines := []string{fit(prompt+q, iw), m.s.Muted.Render(strings.Repeat("─", iw))}

	lh := m.listH()
	end := min(len(m.matches), m.offset+lh)
	for i := m.offset; i < end; i++ {
		lines = append(lines, m.row(m.items[m.matches[i]], i == m.cursor, iw))
	}
	if len(m.matches) == 0 {
		lines = append(lines, m.s.Muted.Render("No matching commands."))
	}
	right := "↑↓ move · enter run · esc close"
	if m.ic.Name == "ascii" {
		right = "up/down move · enter run · esc close"
	}
	count := m.s.Muted.Render(strings.TrimSpace(strings.Join([]string{strconv.Itoa(len(m.matches)), "of", strconv.Itoa(len(m.items))}, " ")))
	return pane.Render(m.s, "Commands", strings.Join(lines, "\n"), m.w, m.h, true,
		pane.WithExtra(count), pane.WithBottom(right))
}

func (m *Model) row(it Item, cur bool, w int) string {
	marker := "  "
	if cur {
		marker = m.ic.Arrow + " "
	}
	keys := it.Keys
	kw := lipgloss.Width(keys)
	group := ""
	if it.Group != "" {
		group = it.Group + ": "
	}
	room := w - lipgloss.Width(marker) - kw - 1
	if kw == 0 {
		room = w - lipgloss.Width(marker)
	}
	label := group + it.Title
	if it.Disabled && it.Reason != "" {
		label += " (" + it.Reason + ")"
	}
	truncated := lipgloss.Width(label) > room
	if truncated {
		label = ansi.Truncate(label, max(0, room), m.ic.Ellipsis)
	}
	pad := strings.Repeat(" ", max(0, room-lipgloss.Width(label)))
	title := m.s.Text
	if cur {
		title = m.s.Strong
	}
	var body string
	switch {
	case it.Disabled:
		body = m.s.Muted.Render(label)
	case truncated || group == "":
		body = title.Render(label)
	default:
		body = m.s.Muted.Render(group) + title.Render(strings.TrimPrefix(label, group))
	}
	line := marker + body + pad
	if kw > 0 {
		line += " " + m.s.FooterKey.Render(keys)
	}
	if cur {
		line = m.s.Accent.Render(marker) + strings.TrimPrefix(line, marker)
	}
	return fit(line, w)
}

func fit(s string, w int) string {
	if lipgloss.Width(s) > w {
		return ansi.Truncate(s, w, "")
	}
	return s
}
