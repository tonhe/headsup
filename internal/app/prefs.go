package app

import (
	"fmt"
	"maps"
	"slices"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tonhe/headsup/internal/config"
	"github.com/tonhe/headsup/internal/icons"
	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/store"
	"github.com/tonhe/headsup/internal/theme"
	"github.com/tonhe/headsup/internal/ui/styles"
)

// This file is the app side of the Preferences module: applying settings live
// and persisting them. The prefs module reaches it through an interface it
// declares itself (prefs.Settings), so neither package imports the other.

// ColumnLister lets a table module advertise its column keys for the
// Preferences columns editor ([ui.columns]).
type ColumnLister interface{ AvailableColumns() []string }

type savedMsg struct{ err error }

// applyTheme switches the live theme without saving. The shared Styles value
// is rewritten in place, so every component holding the pointer repaints;
// modules then get a store update so they rebuild cached, pre-styled rows.
func (m *Model) applyTheme(slug string) tea.Cmd {
	t := theme.ByName(slug)
	if t == nil {
		return nil
	}
	theme.Set(*t)
	*m.styles = *styles.New(*t)
	return m.restyle()
}

// restyle asks every module to rebuild what it rendered with old styles.
func (m *Model) restyle() tea.Cmd {
	if m.store.Loaded == 0 {
		return nil
	}
	return m.broadcast(store.UpdatedMsg{Slices: m.store.Loaded})
}

// chooseTheme applies and saves a theme. It is saved where the current theme
// comes from: the profile override when the connected profile has one (or
// profileOnly asks for one), else [ui] theme.
func (m *Model) chooseTheme(slug string, profileOnly bool) tea.Cmd {
	t := theme.ByName(slug)
	if t == nil {
		return m.toasts.Push(module.ToastWarn, fmt.Sprintf("Unknown theme %q.", slug))
	}
	cmds := []tea.Cmd{m.applyTheme(slug)}
	where := ""
	switch {
	case profileOnly || m.profileTheme() != "":
		if !m.setProfileTheme(slug) {
			return tea.Batch(append(cmds, m.toasts.Push(module.ToastWarn,
				"This connection has no config profile; the theme is kept for this session only."))...)
		}
		where = " for profile " + m.opt.Profile
	default:
		m.opt.Config.UI.Theme = slug
	}
	cmds = append(cmds, m.saveConfig(), m.toasts.Push(module.ToastSuccess, "Theme "+t.Name+where+m.savedSuffix()))
	return tea.Batch(cmds...)
}

func (m *Model) savedSuffix() string {
	if m.opt.ConfigPath == "" {
		return " (this session only)."
	}
	return "."
}

// reloadThemes re-reads the user theme directory and refreshes the picker.
func (m *Model) reloadThemes() tea.Cmd {
	if m.opt.ThemesDir == "" {
		return m.toasts.Push(module.ToastInfo, "No user theme directory is configured.")
	}
	n, warns := theme.RegisterUserThemes(m.opt.ThemesDir)
	if m.picker != nil {
		m.picker.SetThemes(theme.All())
	}
	cmds := []tea.Cmd{m.toasts.Push(module.ToastInfo, fmt.Sprintf("Loaded %d user theme(s) from %s.", n, m.opt.ThemesDir))}
	for _, w := range warns {
		cmds = append(cmds, m.toasts.Push(module.ToastWarn, "Theme "+w))
	}
	// A reloaded file may have changed the active theme's colours.
	cmds = append(cmds, m.applyTheme(m.styles.Theme.Slug))
	return tea.Batch(cmds...)
}

// saveConfig writes a snapshot of the config off the UI goroutine.
func (m *Model) saveConfig() tea.Cmd {
	if m.opt.ConfigPath == "" {
		return nil
	}
	snap, path := cloneConfig(*m.opt.Config), m.opt.ConfigPath
	return func() tea.Msg { return savedMsg{config.Save(path, snap)} }
}

func cloneConfig(c config.Config) config.Config {
	c.Profiles = slices.Clone(c.Profiles)
	c.UI.Columns = cloneColumns(c.UI.Columns)
	c.Keys = maps.Clone(c.Keys)
	return c
}

func cloneColumns(in map[string][]string) map[string][]string {
	if in == nil {
		return nil
	}
	out := make(map[string][]string, len(in))
	for k, v := range in {
		out[k] = slices.Clone(v)
	}
	return out
}

// --- prefs.Settings ---------------------------------------------------------

// UIPrefs returns the saved [ui] settings (not session flag overrides).
func (m *Model) UIPrefs() config.UI {
	u := m.opt.Config.UI
	u.Columns = cloneColumns(u.Columns)
	return u
}

// SetUIPrefs changes [ui] settings, applies what can change live (theme,
// icons, sidebar, mouse, refresh intervals, expiry warning), and saves. An
// invalid change is rejected with a toast and nothing is saved.
func (m *Model) SetUIPrefs(change func(*config.UI)) tea.Cmd {
	cfg := m.opt.Config
	old := cfg.UI
	cfg.UI.Columns = cloneColumns(old.Columns)
	change(&cfg.UI)
	if err := cfg.Validate(); err != nil {
		cfg.UI = old
		return m.toasts.Push(module.ToastError, err.Error())
	}
	u := cfg.UI
	var cmds []tea.Cmd
	if u.Theme != old.Theme && m.profileTheme() == "" {
		if theme.ByName(u.Theme) == nil {
			cfg.UI = old
			return m.toasts.Push(module.ToastError, fmt.Sprintf("Unknown theme %q.", u.Theme))
		}
		cmds = append(cmds, m.applyTheme(u.Theme))
	}
	if u.Icons != old.Icons {
		cmds = append(cmds, m.setIcons(u.Icons))
	}
	if u.Sidebar != old.Sidebar {
		m.opt.Sidebar = u.Sidebar
		m.showSide = u.Sidebar != "never"
	}
	if u.Mouse != old.Mouse {
		m.mouse = u.Mouse
		if u.Mouse {
			cmds = append(cmds, tea.EnableMouseCellMotion)
		} else {
			cmds = append(cmds, tea.DisableMouse)
		}
	}
	m.opt.Refresh = u.RefreshInterval()
	m.opt.RefreshIdle = u.RefreshIdleInterval()
	m.opt.ExpiryWarn = u.ExpiryWarnDuration()
	// Density, time format and columns are read by modules from UIPrefs.
	cmds = append(cmds, m.restyle(), m.saveConfig())
	return tea.Batch(cmds...)
}

// setIcons switches the glyph set. Modules copy glyphs into their widgets at
// Init, so they are re-initialised, the same as after a connection switch.
func (m *Model) setIcons(name string) tea.Cmd {
	m.icons = icons.ByName(name)
	m.toasts.SetStyles(m.styles, m.icons)
	cmds := make([]tea.Cmd, 0, len(m.modules))
	for _, mod := range m.modules {
		cmds = append(cmds, mod.Init(m))
	}
	return tea.Batch(cmds...)
}

// ProfileTheme reports the connected config profile and its theme override.
// ok is false when the connection has no config profile (host mode, --url).
func (m *Model) ProfileTheme() (profile, slug string, ok bool) {
	p, ok := m.configProfile()
	return p.Name, p.Theme, ok
}

// SetProfileTheme sets the connected profile's theme override; "" clears it
// and falls back to [ui] theme.
func (m *Model) SetProfileTheme(slug string) tea.Cmd {
	if slug != "" && theme.ByName(slug) == nil {
		return m.toasts.Push(module.ToastError, fmt.Sprintf("Unknown theme %q.", slug))
	}
	if !m.setProfileTheme(slug) {
		return m.toasts.Push(module.ToastWarn, "This connection has no config profile to set a theme on.")
	}
	return tea.Batch(m.applyTheme(m.effectiveTheme()), m.saveConfig())
}

// OpenThemePicker opens the theme picker modal (the same as T).
func (m *Model) OpenThemePicker() tea.Cmd { return m.openThemePicker() }

// ColumnSets returns, per module ID, the column keys a module offers.
func (m *Model) ColumnSets() map[string][]string {
	out := map[string][]string{}
	for _, mod := range m.modules {
		if c, ok := mod.(ColumnLister); ok {
			out[mod.ID()] = c.AvailableColumns()
		}
	}
	return out
}

// ConfigSaved reports whether preference changes are written to disk.
func (m *Model) ConfigSaved() bool { return m.opt.ConfigPath != "" }
