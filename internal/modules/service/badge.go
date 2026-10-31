package service

import (
	"github.com/tonhe/headsup/internal/store"
	"github.com/tonhe/headsup/internal/ui/badge"
)

// Badge is the sidebar/tab badge: ● running, ○ stopped, ◐ restarting. It
// reflects this module's last status poll and any transition it started
// itself, falling back to the store's status before the module has polled.
func (m *Model) Badge(st *store.Store) string {
	p := m.phase()
	if p == badge.ServiceUnknown && st != nil && st.Service != nil {
		p = badge.ServicePhaseOf(st.Service.State, st.Service.Running, "")
	}
	if p == badge.ServiceUnknown {
		return ""
	}
	return badge.ServiceGlyph(m.ic, p)
}
