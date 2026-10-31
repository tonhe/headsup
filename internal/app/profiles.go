package app

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/tonhe/headsup/internal/config"
)

// Per-profile theme override (T124): a [[profile]] may set theme = "slug", so
// a production server looks different from a homelab. The override wins over
// [ui] theme while that profile is connected, and follows profile switches.

// configProfile returns the config entry for the connected profile. Host
// mode and ad-hoc --url connections have none.
func (m *Model) configProfile() (config.Profile, bool) {
	if m.opt.Config == nil || m.opt.Profile == "" {
		return config.Profile{}, false
	}
	return m.opt.Config.FindProfile(m.opt.Profile)
}

// profileTheme is the connected profile's theme override, if any.
func (m *Model) profileTheme() string {
	if p, ok := m.configProfile(); ok {
		return p.Theme
	}
	return ""
}

// effectiveTheme is what the current connection should look like: the
// profile override, else [ui] theme.
func (m *Model) effectiveTheme() string {
	if t := m.profileTheme(); t != "" {
		return t
	}
	return m.opt.Config.UI.Theme
}

// setProfileTheme writes (or, with "", clears) the override on the connected
// profile. It reports false when there is no config profile to write to.
func (m *Model) setProfileTheme(slug string) bool {
	p, ok := m.configProfile()
	if !ok {
		return false
	}
	p.Theme = slug
	m.opt.Config.UpsertProfile(p)
	return true
}

// applyProfileTheme switches to the effective theme after a connection change.
func (m *Model) applyProfileTheme() tea.Cmd {
	if t := m.effectiveTheme(); t != "" && t != m.styles.Theme.Slug {
		return m.applyTheme(t)
	}
	return nil
}
