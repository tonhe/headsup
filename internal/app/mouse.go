package app

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/ui/sidebar"
)

// Optional mouse support (T129), on when ui.mouse is true:
//
//   - clicking a sidebar entry or a tab switches module; clicking a section
//     header goes to that section's first page;
//   - the wheel scrolls the active module (sent as up/down keys, so every
//     list works) and the help, palette and theme picker;
//   - a click in the module area is forwarded to the module as a tea.MouseMsg
//     with X/Y relative to the module's top-left corner, so a module can map
//     it to a row (e.g. select the clicked table row).
//
// The header is one line; the body starts at row 1. In tab-bar layout the
// tab bar takes row 1 and the module starts at row 2.

func (m *Model) handleMouse(msg tea.MouseMsg) tea.Cmd {
	if !m.mouse {
		return nil
	}
	switch {
	case m.confirm != nil || m.form != nil:
		return nil
	case m.help != nil:
		return m.help.Update(msg)
	case m.palette != nil:
		return m.palette.Update(msg)
	case m.picker != nil:
		return m.picker.Update(msg)
	}
	if m.w < MinWidth || m.h < MinHeight || len(m.modules) == 0 {
		return nil
	}
	ox, oy := 0, 2
	if m.useSidebar() {
		ox, oy = sidebarWidth, 1
	}
	click := msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft

	// Navigation.
	if click {
		m.jump = 0
		if m.useSidebar() && msg.X < sidebarWidth && msg.Y >= 1 && msg.Y < m.h-1 {
			return m.sidebarClick(msg.Y - 1)
		}
		if !m.useSidebar() && msg.Y == 1 {
			if i := m.tabAt(msg.X); i >= 0 {
				return m.Navigate(m.tabItems()[i].ID, nil)
			}
			return nil
		}
	}

	// Module area.
	if msg.X < ox || msg.Y < oy || msg.Y >= m.h-1 {
		return nil
	}
	cur := m.current()
	if cur.Capturing() {
		return nil
	}
	if msg.Action == tea.MouseActionPress {
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			return m.updateModule(m.active, tea.KeyMsg{Type: tea.KeyUp})
		case tea.MouseButtonWheelDown:
			return m.updateModule(m.active, tea.KeyMsg{Type: tea.KeyDown})
		}
	}
	rel := msg
	rel.X, rel.Y = msg.X-ox, msg.Y-oy
	return m.updateModule(m.active, rel)
}

// tabAt maps a column on the tab bar to a module index, or -1. It mirrors
// tabs.Render's layout: " key title (badge) " segments joined by a
// one-cell separator, scrolled so the active tab is visible.
func (m *Model) tabAt(x int) int {
	items := m.tabItems()
	n := len(items)
	if n == 0 {
		return -1
	}
	widths := make([]int, n)
	for i, it := range items {
		label := it.Title
		if it.Key != "" {
			label = it.Key + " " + label
		}
		if it.Badge != "" {
			label += " (" + it.Badge + ")"
		}
		widths[i] = lipgloss.Width(" " + label + " ")
	}
	ew := lipgloss.Width(m.icons.Ellipsis)
	total := func(lo, hi int) int {
		t := hi - lo
		for i := lo; i <= hi; i++ {
			t += widths[i]
		}
		if lo > 0 {
			t += ew
		}
		if hi < n-1 {
			t += ew
		}
		return t
	}
	lo, hi := 0, n-1
	if total(lo, hi) > m.w {
		lo, hi = m.active, m.active
		for {
			grew := false
			if hi+1 < n && total(lo, hi+1) <= m.w {
				hi++
				grew = true
			}
			if lo > 0 && total(lo-1, hi) <= m.w {
				lo--
				grew = true
			}
			if !grew {
				break
			}
		}
	}
	pos := 0
	if lo > 0 {
		pos = ew
	}
	for i := lo; i <= hi; i++ {
		if i > lo {
			pos++ // separator
		}
		if x >= pos && x < pos+widths[i] {
			return i
		}
		pos += widths[i]
	}
	return -1
}

// MouseEnabled reports whether mouse handling is on (ui.mouse).
func (m *Model) MouseEnabled() bool { return m.mouse }

var _ module.Context = (*Model)(nil)

// sidebarClick handles a click on sidebar row y (0 is the first row).
func (m *Model) sidebarClick(y int) tea.Cmd {
	groups, _ := m.navGroups()
	rows := sidebar.Rows(groups)
	if y < 0 || y >= len(rows) || rows[y].Group < 0 {
		return nil
	}
	g := groups[rows[y].Group]
	i := max(0, rows[y].Item)
	return m.Navigate(g.Items[i].ID, nil)
}
