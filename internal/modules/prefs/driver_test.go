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
	"github.com/tonhe/headsup/internal/ui/uitest"
)

// driver runs the app headlessly on the shared flow-test driver (fake clock,
// no dropped commands); see uitest.Driver.
type driver struct {
	*uitest.Driver
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
	m := app.New(opt)
	return &driver{Driver: uitest.NewDriver(t, m, w, h), t: t, m: m}
}

func (d *driver) send(msg tea.Msg) { d.Send(msg) }

// key sends named keys, or types each rune of any other string as one key.
func (d *driver) key(keys ...string) {
	for _, k := range keys {
		if uitest.KeyMsg(k).Type != tea.KeyRunes {
			d.Key(k)
			continue
		}
		d.Type(k)
	}
}

func (d *driver) view() string { return d.View() }
