package app

import (
	"strings"

	"github.com/tonhe/headsup/internal/module"
)

// appAction is an app-wide command. Like module Actions, this table is the one
// source for the default keymap, the palette, and the help screen.
type appAction struct {
	id, title string
	keys      []string
}

var appActions = []appAction{
	{"app.palette", "Command palette", []string{":", "ctrl+p"}},
	{"app.help", "Help", []string{"?"}},
	{"app.theme", "Switch theme", []string{"T"}},
	{"app.switch", "Switch connection profile", []string{"P"}},
	{"app.conn", "Edit connection", []string{"C"}},
	{"app.refresh", "Refresh now", []string{"R", "ctrl+r"}},
	{"app.sidebar", "Toggle sidebar", []string{"ctrl+b"}},
	{"app.next", "Next module", []string{"]"}},
	{"app.prev", "Previous module", []string{"["}},
	{"app.quit", "Quit", []string{"q"}},
}

// Jumper lets a module declare extra direct jump keys besides its section
// jump and its "g x" chord, e.g. "," for Preferences.
type Jumper interface{ JumpKeys() []string }

// keyLabel joins an action's live keys for display ("R / ctrl+r").
func (m *Model) keyLabel(id string) string { return strings.Join(m.keymap.Keys(id), " / ") }

// globalHints are the status-bar hints after the module's own.
func (m *Model) globalHints() []module.Hint {
	var hs []module.Hint
	for _, h := range []struct{ id, desc string }{
		{"app.help", "help"}, {"app.palette", "commands"}, {"app.quit", "quit"},
	} {
		if ks := m.keymap.Keys(h.id); len(ks) > 0 {
			hs = append(hs, module.Hint{Key: ks[0], Desc: h.desc})
		}
	}
	return hs
}

// moduleIndex finds a module by ID.
func (m *Model) moduleIndex(id string) int {
	for i, mod := range m.modules {
		if mod.ID() == id {
			return i
		}
	}
	return -1
}

// findAnyAction finds an action in any module, current first.
func (m *Model) findAnyAction(id string) (int, module.Action, bool) {
	if a, ok := m.findAction(id); ok {
		return m.active, a, true
	}
	for i, mod := range m.modules {
		for _, a := range mod.Actions() {
			if a.ID == id {
				return i, a, true
			}
		}
	}
	return -1, module.Action{}, false
}
