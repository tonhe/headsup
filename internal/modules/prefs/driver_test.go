package prefs_test

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tonhe/headsup/internal/api/fake"
	"github.com/tonhe/headsup/internal/app"
	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/modules/dashboard"
	"github.com/tonhe/headsup/internal/modules/machines"
	"github.com/tonhe/headsup/internal/modules/pending"
	"github.com/tonhe/headsup/internal/modules/prefs"
)

// driver runs the app headlessly, teatest style: commands run synchronously,
// ones that block (ticks, toast timers) are dropped, results are fed back.
type driver struct {
	t *testing.T
	m *app.Model
}

var clock = func() time.Time { return time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC) }

func newDriver(t *testing.T, w, h int, opt app.Options) *driver {
	t.Helper()
	if opt.Backend == nil {
		opt.Backend = fake.Demo(fake.WithClock(clock))
	}
	if opt.Modules == nil {
		opt.Modules = []module.Module{dashboard.New(), machines.New(), pending.New(), prefs.New()}
	}
	opt.Now = clock
	opt.Refresh = time.Hour
	d := &driver{t: t, m: app.New(opt)}
	d.send(tea.WindowSizeMsg{Width: w, Height: h})
	d.exec(d.m.Init(), 0)
	return d
}

func (d *driver) send(msg tea.Msg) {
	_, cmd := d.m.Update(msg)
	d.exec(cmd, 0)
}

func (d *driver) exec(cmd tea.Cmd, depth int) {
	if cmd == nil || depth > 20 {
		return
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-done:
	case <-time.After(150 * time.Millisecond):
		return
	}
	switch msg := msg.(type) {
	case nil:
	case tea.BatchMsg:
		for _, c := range msg {
			d.exec(c, depth+1)
		}
	case tea.QuitMsg:
	default:
		_, next := d.m.Update(msg)
		d.exec(next, depth+1)
	}
}

var named = map[string]tea.KeyType{
	"enter": tea.KeyEnter, "esc": tea.KeyEsc, "tab": tea.KeyTab, "up": tea.KeyUp, "down": tea.KeyDown,
	"left": tea.KeyLeft, "right": tea.KeyRight, "backspace": tea.KeyBackspace,
	"ctrl+r": tea.KeyCtrlR, "ctrl+t": tea.KeyCtrlT, "ctrl+s": tea.KeyCtrlS, "ctrl+u": tea.KeyCtrlU,
}

// key sends named keys, or each rune of any other string as one key.
func (d *driver) key(keys ...string) {
	for _, k := range keys {
		if kt, ok := named[k]; ok {
			d.send(tea.KeyMsg{Type: kt})
			continue
		}
		if k == "space" {
			d.send(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
			continue
		}
		for _, r := range k {
			if r == ' ' {
				d.send(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
				continue
			}
			d.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		}
	}
}

func (d *driver) view() string { return d.m.View() }
