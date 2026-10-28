// Package help is the scrollable key-binding overlay.
package help

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/ui/pane"
	"github.com/tonhe/headsup/internal/ui/styles"
)

// Section is a titled group of key hints.
type Section struct {
	Title string
	Keys  []module.Hint
}

// CloseMsg is emitted on esc, q or ?.
type CloseMsg struct{}

// Model is the help overlay.
type Model struct {
	s        *styles.Styles
	sections []Section
	w, h     int
	offset   int
}

// New builds a help overlay.
func New(s *styles.Styles, sections []Section) *Model { return &Model{s: s, sections: sections} }

// SetSections replaces the content and resets scrolling.
func (m *Model) SetSections(sec []Section) { m.sections, m.offset = sec, 0 }

// SetSize sets the outer box size.
func (m *Model) SetSize(w, h int) { m.w, m.h = w, h; m.clamp() }

// Offset returns the scroll offset in lines.
func (m *Model) Offset() int { return m.offset }

func (m *Model) bodyH() int { return max(1, m.h-2) }

func (m *Model) clamp() {
	n := len(m.body())
	m.offset = min(m.offset, max(0, n-m.bodyH()))
	m.offset = max(0, m.offset)
}

// Update scrolls (j/k/up/down/pgup/pgdn/g/G) and closes (esc/q/?).
func (m *Model) Update(msg tea.Msg) tea.Cmd {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	switch km.String() {
	case "esc", "q", "?":
		return func() tea.Msg { return CloseMsg{} }
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

func (m *Model) block(sec Section) []string {
	kw := 0
	for _, k := range sec.Keys {
		kw = max(kw, lipgloss.Width(k.Key))
	}
	out := []string{m.s.Strong.Render(sec.Title)}
	for _, k := range sec.Keys {
		out = append(out, m.s.FooterKey.Render(k.Key)+strings.Repeat(" ", kw-lipgloss.Width(k.Key)+2)+m.s.Text.Render(k.Desc))
	}
	return append(out, "")
}

// body lays sections out in as many columns as fit the inner width.
func (m *Model) body() []string {
	iw, _ := pane.Inner(m.w, m.h)
	var blocks [][]string
	colW, total := 1, 0
	for _, s := range m.sections {
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
	right := "esc close"
	if len(lines) > m.bodyH() {
		right = "j/k scroll · esc close"
	}
	return pane.Render(m.s, "Help", strings.Join(vis, "\n"), m.w, m.h, true, pane.WithRight(right))
}
