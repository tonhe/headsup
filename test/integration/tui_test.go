//go:build integration

package integration

import (
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/tonhe/headsup/internal/activity"
	"github.com/tonhe/headsup/internal/api"
	"github.com/tonhe/headsup/internal/app"
	"github.com/tonhe/headsup/internal/config"
	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/modules/registry"
	"github.com/tonhe/headsup/internal/ui/uitest"

	_ "github.com/tonhe/headsup/internal/modules/all"
)

// tui runs a Bubble Tea model headlessly in real time.
//
// The unit tests' uitest.Driver runs the model inside a testing/synctest
// bubble with a fake clock. That cannot work against a real server: an HTTP
// client's idle keep-alive goroutines block on the network forever, which a
// bubble never counts as idle, so it would never settle. This driver is the
// plain version of the same loop: commands run on their own goroutines as
// Bubble Tea runs them, results are fed back on one goroutine that owns the
// model, and tests wait for what they expect on screen with a deadline.
type tui struct {
	t    *testing.T
	ops  chan func()
	done chan struct{}
	m    tea.Model // owned by the loop goroutine
}

// startTUI starts m at w x h and runs Init.
func startTUI(t *testing.T, m tea.Model, w, h int) *tui {
	t.Helper()
	d := &tui{t: t, ops: make(chan func(), 256), done: make(chan struct{}), m: m}
	go func() {
		for {
			select {
			case f := <-d.ops:
				f()
			case <-d.done:
				return
			}
		}
	}()
	t.Cleanup(func() { close(d.done) })
	d.Send(tea.WindowSizeMsg{Width: w, Height: h})
	d.do(func() { d.spawn(d.m.Init()) })
	return d
}

// do runs f on the loop goroutine and waits for it.
func (d *tui) do(f func()) {
	ack := make(chan struct{})
	select {
	case d.ops <- func() { defer close(ack); f() }:
	case <-d.done:
		return
	}
	<-ack
}

// handle feeds one message to the model (on the loop goroutine).
func (d *tui) handle(msg tea.Msg) {
	switch msg := msg.(type) {
	case nil, tea.QuitMsg:
		return
	case tea.BatchMsg:
		for _, c := range msg {
			d.spawn(c)
		}
		return
	}
	if cmds, ok := sequence(msg); ok {
		go func() {
			for _, c := range cmds {
				if c != nil {
					m := c()
					d.deliver(m)
				}
			}
		}()
		return
	}
	var cmd tea.Cmd
	d.m, cmd = d.m.Update(msg)
	d.spawn(cmd)
}

func (d *tui) spawn(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	go func() { d.deliver(cmd()) }()
}

func (d *tui) deliver(msg tea.Msg) {
	select {
	case d.ops <- func() { d.handle(msg) }:
	case <-d.done:
	}
}

// Send delivers msg to the model.
func (d *tui) Send(msg tea.Msg) { d.do(func() { d.handle(msg) }) }

// Key sends one key per argument (names as uitest.KeyMsg takes them).
func (d *tui) Key(keys ...string) {
	for _, k := range keys {
		d.Send(uitest.KeyMsg(k))
	}
}

// Type sends each rune of s as its own key.
func (d *tui) Type(s string) {
	for _, r := range s {
		if r == ' ' {
			d.Send(uitest.KeyMsg("space"))
			continue
		}
		d.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

// View renders the model; Screen strips ANSI.
func (d *tui) View() string {
	var out string
	d.do(func() { out = d.m.View() })
	return out
}

func (d *tui) Screen() string { return ansi.Strip(d.View()) }

// Model returns the current model (read it only for assertions).
func (d *tui) Model() tea.Model {
	var m tea.Model
	d.do(func() { m = d.m })
	return m
}

// Wait waits until every sub is on screen.
func (d *tui) Wait(timeout time.Duration, subs ...string) string {
	d.t.Helper()
	return d.WaitFunc(timeout, "screen to show "+strings.Join(quoteAll(subs), ", "), func(s string) bool {
		for _, sub := range subs {
			if !strings.Contains(s, sub) {
				return false
			}
		}
		return true
	})
}

// WaitFunc waits until ok(screen) holds.
func (d *tui) WaitFunc(timeout time.Duration, what string, ok func(string) bool) string {
	d.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		s := d.Screen()
		if ok(s) {
			return s
		}
		if time.Now().After(deadline) {
			d.t.Fatalf("waited %v for %s:\n%s", timeout, what, s)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// WantNot fails if any sub is on screen.
func (d *tui) WantNot(subs ...string) {
	d.t.Helper()
	s := d.Screen()
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			d.t.Fatalf("screen should not show %q:\n%s", sub, s)
		}
	}
}

func quoteAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = "\"" + s + "\""
	}
	return out
}

var cmdType = reflect.TypeOf((tea.Cmd)(nil))

// sequence recognises Bubble Tea's unexported sequenceMsg ([]tea.Cmd).
func sequence(msg tea.Msg) ([]tea.Cmd, bool) {
	v := reflect.ValueOf(msg)
	if !v.IsValid() || v.Kind() != reflect.Slice || v.Type().Elem() != cmdType {
		return nil, false
	}
	out := make([]tea.Cmd, v.Len())
	for i := range out {
		out[i] = v.Index(i).Interface().(tea.Cmd)
	}
	return out, true
}

// appOptions are what cmd/headsup passes the app, against b (and h in host
// mode), with the config file and activity log under the isolated HOME.
type appOpts struct {
	backend   api.Backend
	host      host.Host
	label     string
	serverURL string
	readOnly  bool
	connector app.Connector
	cfg       *config.Config
}

func (s *suite) newApp(t *testing.T, o appOpts) *app.Model {
	t.Helper()
	if o.cfg == nil {
		c, err := config.Load(s.cfgFile)
		if err != nil {
			t.Fatal(err)
		}
		o.cfg = &c
	}
	logger, err := activity.OpenDefault()
	if err != nil {
		t.Fatal(err)
	}
	if o.serverURL == "" {
		o.serverURL = s.url
	}
	return app.New(app.Options{
		Backend:     o.backend,
		Host:        o.host,
		Modules:     registry.All(o.cfg.Modules.Disabled),
		Profile:     o.label,
		ServerURL:   o.serverURL,
		ReadOnly:    o.readOnly,
		Theme:       o.cfg.UI.Theme,
		Icons:       o.cfg.UI.Icons,
		Refresh:     2 * time.Second,
		RefreshIdle: 2 * time.Second,
		ExpiryWarn:  o.cfg.UI.ExpiryWarnDuration(),
		Activity:    logger,
		Connector:   o.connector,
		Config:      o.cfg,
		ConfigPath:  s.cfgFile,
		ThemesDir:   config.ThemesDir(),
		Notifier:    nopNotifier{},
	})

}

type nopNotifier struct{}

func (nopNotifier) Desktop(string, string) error { return nil }
func (nopNotifier) Bell() error                  { return nil }
