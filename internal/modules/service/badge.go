package service

import (
	"github.com/tonhe/headsup/internal/store"
	"github.com/tonhe/headsup/internal/ui/badge"
)

// Badge is the sidebar/tab badge: ● running, ○ stopped, ◐ restarting. It
// reflects this module's last status poll (refreshed with every store
// update) and any transition it started itself.
func (m *Model) Badge(*store.Store) string {
	p := m.phase()
	if p == badge.ServiceUnknown {
		return ""
	}
	return badge.ServiceGlyph(m.ic, p)
}
