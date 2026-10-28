package confirm

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/tonhe/headsup/internal/icons"
	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/ui/uitest"
)

func keys(s string) []tea.Msg {
	var out []tea.Msg
	for _, r := range s {
		out = append(out, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return out
}

func run(m *Model, msgs ...tea.Msg) (ResultMsg, bool) {
	for _, msg := range msgs {
		if cmd := m.Update(msg); cmd != nil && m.Done() {
			if r, ok := cmd().(ResultMsg); ok {
				return r, true
			}
		}
	}
	return ResultMsg{}, false
}

func TestNeeded(t *testing.T) {
	if Needed(module.DangerNone) || !Needed(module.DangerConfirm) || !Needed(module.DangerCritical) {
		t.Fatal("Needed")
	}
}

func TestFlows(t *testing.T) {
	s := uitest.StyleFor("")
	one := []module.Target{{Kind: "node", ID: "7", Name: "nas-01"}}
	enter := tea.KeyMsg{Type: tea.KeyEnter}
	cases := []struct {
		name   string
		danger module.Danger
		msgs   []tea.Msg
		ok     bool
		done   bool
	}{
		{"y", module.DangerConfirm, keys("y"), true, true},
		{"n", module.DangerConfirm, keys("n"), false, true},
		{"enter default cancel", module.DangerConfirm, []tea.Msg{enter}, false, true},
		{"tab enter", module.DangerConfirm, []tea.Msg{tea.KeyMsg{Type: tea.KeyTab}, enter}, true, true},
		{"critical exact", module.DangerCritical, append(keys("nas-01"), enter), true, true},
		{"critical wrong", module.DangerCritical, append(keys("nas-0"), enter), false, false},
		{"critical y ignored", module.DangerCritical, keys("y"), false, false},
		{"critical esc", module.DangerCritical, []tea.Msg{tea.KeyMsg{Type: tea.KeyEsc}}, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := New(s, icons.Unicode, Request{ID: "x", Title: "Delete", Danger: c.danger, Targets: one})
			r, done := run(m, c.msgs...)
			if done != c.done || r.OK != c.ok || (done && r.ID != "x") {
				t.Fatalf("got %+v done=%v", r, done)
			}
		})
	}
}

func TestViewTruncatesTargets(t *testing.T) {
	uitest.ForceColor()
	s := uitest.StyleFor("")
	var ts []module.Target
	for i := 0; i < 25; i++ {
		ts = append(ts, module.Target{Kind: "node", Name: fmt.Sprintf("node-%02d-%s", i, strings.Repeat("long", 20))})
	}
	for _, d := range []module.Danger{module.DangerConfirm, module.DangerCritical} {
		m := New(s, icons.Unicode, Request{Title: "Delete nodes", Danger: d, Targets: ts, Consequence: "They will be gone for good and must re-register from scratch."})
		for _, w := range []int{80, 40} {
			v := m.View(w)
			if mw := uitest.MaxWidth(v); mw > w {
				t.Errorf("w=%d width %d", w, mw)
			}
			plain := ansi.Strip(v)
			if !strings.Contains(plain, "and 15 more") || strings.Contains(plain, "node-10") {
				t.Errorf("target list wrong:\n%s", plain)
			}
		}
	}
}
