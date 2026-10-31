// Package help is the scrollable key-binding overlay. The app generates its
// sections from module Actions and the live keymap, so remapped keys show up
// here automatically; "/" filters entries by key, description or section.
package help

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/ui/pane"
	"github.com/tonhe/headsup/internal/ui/styles"
)

// Section is a titled group of key hints. Dim marks entries (by index into
// Keys) that are not available right now, e.g. a mutation in read-only mode.
type Section struct {
	Title string
	Keys  []module.Hint
	Dim   map[int]bool
}

// CloseMsg is emitted on esc, q or ?.
type CloseMsg struct{}

// Model is the help overlay.
type Model struct {
	s        *styles.Styles
	sections []Section
	w, h     int
	offset   int
	query    string
	typing   bool
}

// New builds a help overlay.
func New(s *styles.Styles, sections []Section) *Model { return &Model{s: s, sections: sections} }

// SetSections replaces the content and resets scrolling.
func (m *Model) SetSections(sec []Section) { m.sections, m.offset = sec, 0 }

// SetSize sets the outer box size.
func (m *Model) SetSize(w, h int) { m.w, m.h = w, h; m.clamp() }

// Query returns the filter text.
func (m *Model) Query() string { return m.query }

// Sections returns the content.
func (m *Model) Sections() []Section { return m.sections }

// Offset returns the scroll offset in lines.
func (m *Model) Offset() int { return m.offset }

func (m *Model) bodyH() int { return max(1, m.h-2) }

func (m *Model) clamp() {
	n := len(m.body())
	m.offset = min(m.offset, max(0, n-m.bodyH()))
	m.offset = max(0, m.offset)
}

// Update scrolls (j/k/up/down/pgup/pgdn/g/G and the mouse wheel), filters
// ("/" then type; enter keeps, esc clears) and closes (esc/q/?).
func (m *Model) Update(msg tea.Msg) tea.Cmd {
	if mm, ok := msg.(tea.MouseMsg); ok {
		switch mm.Button {
		case tea.MouseButtonWheelUp:
			m.offset -= 3
		case tea.MouseButtonWheelDown:
			m.offset += 3
		}
		m.clamp()
		return nil
	}
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	if m.typing {
		switch km.Type {
		case tea.KeyEsc:
			m.typing, m.query = false, ""
		case tea.KeyEnter:
			m.typing = false
		case tea.KeyBackspace:
			if r := []rune(m.query); len(r) > 0 {
				m.query = string(r[:len(r)-1])
			}
		case tea.KeySpace:
			m.query += " "
		case tea.KeyRunes:
			m.query += string(km.Runes)
		}
		m.offset = 0
		m.clamp()
		return nil
	}
	switch km.String() {
	case "esc":
		if m.query != "" {
			m.query, m.offset = "", 0
			return nil
		}
		return func() tea.Msg { return CloseMsg{} }
	case "q", "?":
		return func() tea.Msg { return CloseMsg{} }
	case "/":
		m.typing = true
		return nil
	case "j", "down":
		m.offset++
	case "k", "up":
		m.offset--
	case "pgdown", "ctrl+d", " ":
		m.offset += m.bodyH()
	case "pgup", "ctrl+u":
		m.offset -= m.bodyH()
	case "g", "home":
		m.offset = 0
	case "G", "end":
		m.offset = 1 << 30
	}
	m.clamp()
	return nil
}

// filtered applies the query: a section whose title matches is kept whole,
// otherwise only its matching entries are.
func (m *Model) filtered() []Section {
	q := strings.ToLower(strings.TrimSpace(m.query))
	if q == "" {
		return m.sections
	}
	var out []Section
	for _, sec := range m.sections {
		if strings.Contains(strings.ToLower(sec.Title), q) {
			out = append(out, sec)
			continue
		}
		ns := Section{Title: sec.Title, Dim: map[int]bool{}}
		for i, k := range sec.Keys {
			if strings.Contains(strings.ToLower(k.Desc), q) || strings.Contains(strings.ToLower(k.Key), q) {
				if sec.Dim[i] {
					ns.Dim[len(ns.Keys)] = true
				}
				ns.Keys = append(ns.Keys, k)
			}
		}
		if len(ns.Keys) > 0 {
			out = append(out, ns)
		}
	}
	return out
}

func (m *Model) block(sec Section) []string {
	kw := 0
	for _, k := range sec.Keys {
		kw = max(kw, lipgloss.Width(k.Key))
	}
	out := []string{m.s.Strong.Render(sec.Title)}
	for i, k := range sec.Keys {
		ks, ds := m.s.FooterKey, m.s.Text
		if sec.Dim[i] {
			ks, ds = m.s.Muted, m.s.Muted
		}
		out = append(out, ks.Render(k.Key)+strings.Repeat(" ", kw-lipgloss.Width(k.Key)+2)+ds.Render(k.Desc))
	}
	return append(out, "")
}

// body lays sections out in as many columns as fit the inner width.
func (m *Model) body() []string {
	iw, _ := pane.Inner(m.w, m.h)
	var blocks [][]string
	colW, total := 1, 0
	for _, s := range m.filtered() {
		b := m.block(s)
		for _, l := range b {
			colW = max(colW, lipgloss.Width(l))
		}
		total += len(b)
		blocks = append(blocks, b)
	}
	const gap = 3
	ncols := max(1, min(len(blocks), (iw+gap)/(colW+gap)))
	if ncols == 1 {
		var out []string
		for _, b := range blocks {
			out = append(out, b...)
		}
		return out
	}
	target := (total + ncols - 1) / ncols
	cols := make([][]string, 0, ncols)
	var cur []string
	for _, b := range blocks {
		if len(cur) > 0 && len(cur)+len(b) > target && len(cols) < ncols-1 {
			cols = append(cols, cur)
			cur = nil
		}
		cur = append(cur, b...)
	}
	cols = append(cols, cur)
	rows := 0
	for _, c := range cols {
		rows = max(rows, len(c))
	}
	out := make([]string, rows)
	for r := 0; r < rows; r++ {
		var b strings.Builder
		for ci, c := range cols {
			cell := ""
			if r < len(c) {
				cell = c[r]
			}
			if ci < len(cols)-1 {
				cell += strings.Repeat(" ", colW-lipgloss.Width(cell)+gap)
			}
			b.WriteString(cell)
		}
		out[r] = b.String()
	}
	return out
}

// View renders the overlay box at exactly w x h.
func (m *Model) View() string {
	lines := m.body()
	end := min(len(lines), m.offset+m.bodyH())
	vis := lines[min(m.offset, end):end]
	right := "/ filter · esc close"
	if len(lines) > m.bodyH() {
		right = "j/k scroll · / filter · esc close"
	}
	var opts []pane.Opt
	switch {
	case m.typing:
		opts = append(opts, pane.WithExtra(m.s.Accent.Render("/ ")+m.s.Text.Render(m.query)+m.s.Accent.Render("▏")))
	case m.query != "":
		opts = append(opts, pane.WithExtra(m.s.Muted.Render("filter: ")+m.s.Text.Render(m.query)))
	}
	if len(lines) == 0 {
		vis = []string{m.s.Muted.Render("No keys match.")}
	}
	opts = append(opts, pane.WithRight(right))
	return pane.Render(m.s, "Help", strings.Join(vis, "\n"), m.w, m.h, true, opts...)
}
