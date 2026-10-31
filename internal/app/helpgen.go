package app

import (
	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/ui/help"
)

// helpSections generates the help screen from Actions and the live keymap,
// so remapped keys are what is shown. The current module comes first, with
// actions that its gate would refuse right now dimmed; then the global keys,
// navigation, and every other module's actions.
func (m *Model) helpSections() []help.Section {
	var secs []help.Section
	cur := m.current()
	if cur != nil {
		if sec, ok := m.moduleSection(cur, true); ok {
			sec.Title += " (this page)"
			secs = append(secs, sec)
		}
	}

	global := help.Section{Title: "Global"}
	for _, a := range appActions {
		if k := m.keyLabel(a.id); k != "" {
			global.Keys = append(global.Keys, module.Hint{Key: k, Desc: a.title})
		}
	}
	global.Keys = append(global.Keys, module.Hint{Key: "ctrl+c", Desc: "Quit now"})
	secs = append(secs, global)

	secs = append(secs, m.navHelp()...)

	for _, mod := range m.modules {
		if mod == cur {
			continue
		}
		if sec, ok := m.moduleSection(mod, false); ok {
			secs = append(secs, sec)
		}
	}
	return secs
}

func (m *Model) moduleSection(mod module.Module, gate bool) (help.Section, bool) {
	sec := help.Section{Title: mod.Title(), Dim: map[int]bool{}}
	env := m.gateEnv()
	for _, a := range mod.Actions() {
		k := m.keyLabel(a.ID)
		if k == "" {
			continue
		}
		if gate && !Gate(a, m, env).Allowed {
			sec.Dim[len(sec.Keys)] = true
		}
		sec.Keys = append(sec.Keys, module.Hint{Key: k, Desc: a.Title})
	}
	return sec, len(sec.Keys) > 0
}
