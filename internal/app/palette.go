package app

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/theme"
	"github.com/tonhe/headsup/internal/ui/modal"
	"github.com/tonhe/headsup/internal/ui/palette"
	"github.com/tonhe/headsup/internal/ui/themepicker"
)

const maxRecent = 12

// paletteItems lists every command: app commands, the current module's
// actions, "go to" jumps, every other module's actions, then themes. Every
// Action appears, with or without keys (FR-030).
func (m *Model) paletteItems() []palette.Item {
	var items []palette.Item
	for _, a := range appActions {
		if a.id == "app.palette" {
			continue
		}
		items = append(items, palette.Item{ID: a.id, Title: a.title, Keys: m.keyLabel(a.id)})
	}
	env := m.gateEnv()
	addModule := func(mod module.Module) {
		for _, a := range mod.Actions() {
			it := palette.Item{ID: a.ID, Title: a.Title, Group: mod.Title(), Keys: m.keyLabel(a.ID)}
			if a.Mutating && env.ReadOnly {
				it.Disabled, it.Reason = true, "read-only"
			}
			items = append(items, it)
		}
	}
	cur := m.current()
	if cur != nil {
		addModule(cur)
	}
	for _, mod := range m.modules {
		items = append(items, palette.Item{
			ID: "app.goto." + mod.ID(), Title: mod.Title(), Group: "Go to", Keys: m.keyLabel("app.goto." + mod.ID()),
		})
	}
	for _, mod := range m.modules {
		if mod != cur {
			addModule(mod)
		}
	}
	for _, t := range theme.All() {
		title := t.Name
		if t.Slug == m.styles.Theme.Slug {
			title += " (current)"
		}
		items = append(items, palette.Item{ID: "app.theme." + t.Slug, Title: title, Group: "Theme"})
	}
	return items
}

func (m *Model) openPalette() tea.Cmd {
	m.palette = palette.New(m.styles, m.icons, m.paletteItems(), m.recent)
	m.sizeModals()
	return nil
}

func (m *Model) openThemePicker() tea.Cmd {
	prof := ""
	if _, ok := m.configProfile(); ok {
		prof = m.opt.Profile
	}
	m.picker = themepicker.New(m.styles, m.icons, theme.All(), m.styles.Theme.Slug, prof)
	m.sizeModals()
	return nil
}

func (m *Model) sizeModals() {
	if m.palette != nil {
		m.palette.SetSize(min(84, m.w-4), min(22, m.h-4))
	}
	if m.picker != nil {
		m.picker.SetSize(min(86, m.w-4), min(24, m.h-2))
	}
}

// placeTop draws box centred horizontally near the top of a dimmed screen.
func (m *Model) placeTop(screen, box string) string {
	bw := lipgloss.Width(box)
	return modal.Place(modal.Dim(m.styles, screen, m.w, m.h), box, (m.w-bw)/2, min(3, max(0, m.h-lipgloss.Height(box))), m.w, m.h)
}

func (m *Model) pushRecent(id string) {
	out := []string{id}
	for _, r := range m.recent {
		if r != id && len(out) < maxRecent {
			out = append(out, r)
		}
	}
	m.recent = out
}

// runCommand runs a palette choice. A module action from another module
// switches to that module first, so its selection and gate apply.
func (m *Model) runCommand(id string) tea.Cmd {
	m.pushRecent(id)
	if strings.HasPrefix(id, "app.") {
		if cmd, ok := m.global(id); ok {
			return cmd
		}
		return nil
	}
	i, a, ok := m.findAnyAction(id)
	if !ok {
		return m.toasts.Push(module.ToastWarn, "That command is no longer available.")
	}
	var cmds []tea.Cmd
	if i != m.active {
		m.active = i
		cmds = append(cmds, m.updateModule(i, module.NavigateMsg{}))
	}
	return tea.Batch(append(cmds, m.invoke(a))...)
}

// updateOverlays handles messages owned by the palette, the theme picker and
// the preferences plumbing. It reports false for anything else.
func (m *Model) updateOverlays(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case palette.CloseMsg:
		m.palette = nil
		return nil, true
	case palette.ChosenMsg:
		m.palette = nil
		return m.runCommand(msg.ID), true
	case themepicker.PreviewMsg:
		return m.applyTheme(msg.Slug), true
	case themepicker.CancelMsg:
		m.picker = nil
		return m.applyTheme(msg.Orig), true
	case themepicker.SelectedMsg:
		m.picker = nil
		return m.chooseTheme(msg.Slug, msg.ProfileOnly), true
	case themepicker.ReloadMsg:
		return m.reloadThemes(), true
	case savedMsg:
		if msg.err != nil {
			return m.toasts.Push(module.ToastError, "Could not save preferences: "+msg.err.Error()), true
		}
		return nil, true
	}
	return nil, false
}
