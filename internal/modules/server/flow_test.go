package server

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	apifake "github.com/tonhe/headsup/internal/api/fake"
	"github.com/tonhe/headsup/internal/app"
	hostfake "github.com/tonhe/headsup/internal/host/fake"
	"github.com/tonhe/headsup/internal/icons"
	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/store"
)

// configStub stands in for the Server Settings module so the test can see
// the Navigate a doctor fix sends, without importing that module.
type configStub struct{ got int }

func (*configStub) ID() string                  { return "serverconfig" }
func (*configStub) Title() string               { return "Server Settings" }
func (*configStub) Icon(icons.Set) string       { return "" }
func (*configStub) Order() int                  { return 100 }
func (*configStub) Modes() module.Modes         { return module.ModeHost }
func (*configStub) Badge(*store.Store) string   { return "" }
func (*configStub) Init(module.Context) tea.Cmd { return nil }
func (s *configStub) Update(_ module.Context, msg tea.Msg) (module.Module, tea.Cmd) {
	if _, ok := msg.(module.NavigateMsg); ok {
		s.got++
	}
	return s, nil
}
func (*configStub) View(module.Context, module.Rect) string { return "SERVER SETTINGS STUB" }
func (*configStub) Actions() []module.Action                { return nil }
func (*configStub) Capturing() bool                         { return false }
func (*configStub) Hints() []module.Hint                    { return nil }

// driver runs the app headlessly, executing commands synchronously. A
// command still running after 200ms (slow I/O on a loaded CI runner) is
// parked, not dropped: its result is applied while want waits for the
// screen.
type driver struct {
	t    *testing.T
	m    *app.Model
	srv  *Model
	cfg  *configStub
	h    *hostfake.Host
	b    *apifake.Backend
	late chan tea.Msg
}

func newDriver(t *testing.T, w, ht int) *driver {
	t.Helper()
	clock := func() time.Time { return testNow }
	d := &driver{t: t, srv: New(), cfg: &configStub{}, late: make(chan tea.Msg, 256)}
	d.srv.root = filepath.Join(t.TempDir(), "backups", "bundles")
	d.srv.src = staticSource{{Tag: "v0.30.0"}, {Tag: "v0.30.1", Body: "## Fixes\n- Fewer crashes"}}
	d.b = apifake.Demo(apifake.WithClock(clock))
	cfg := append(append([]byte{}, hostfake.SeedConfig...), "\ntls_letsencrypt_hostname: hs.demo.example.com\n"...)
	d.h = hostfake.New(hostfake.WithClock(clock), hostfake.WithLogInterval(time.Hour), hostfake.WithFile(hostfake.ConfigPath, cfg))
	d.m = app.New(app.Options{Backend: d.b, Host: d.h, Modules: []module.Module{d.srv, d.cfg}, Now: clock,
		Refresh: time.Hour, Theme: "tokyo-night", Icons: "unicode", Profile: "host:hs-demo", ServerURL: "https://hs.demo.example.com"})
	d.send(tea.WindowSizeMsg{Width: w, Height: ht})
	d.exec(d.m.Init(), 0)
	return d
}

func (d *driver) send(msg tea.Msg) {
	_, cmd := d.m.Update(msg)
	d.exec(cmd, 0)
}

func (d *driver) exec(cmd tea.Cmd, depth int) {
	if cmd == nil || depth > 30 {
		return
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-done:
	case <-time.After(200 * time.Millisecond):
		go func() { d.late <- <-done }()
		return
	}
	d.dispatch(msg, depth)
}

func (d *driver) dispatch(msg tea.Msg, depth int) {
	switch msg := msg.(type) {
	case nil, tea.QuitMsg:
	case tea.BatchMsg:
		for _, c := range msg {
			d.exec(c, depth+1)
		}
	default:
		_, next := d.m.Update(msg)
		d.exec(next, depth+1)
	}
}

func (d *driver) key(keys ...string) {
	for _, k := range keys {
		var msg tea.KeyMsg
		switch k {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		case "tab":
			msg = tea.KeyMsg{Type: tea.KeyTab}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		d.send(msg)
	}
}

func (d *driver) typeText(s string) {
	for _, r := range s {
		d.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

func (d *driver) view() string { return ansi.Strip(d.m.View()) }

// want waits up to 10s for sub on screen, applying parked results meanwhile.
func (d *driver) want(sub string) {
	d.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		out := d.view()
		if strings.Contains(out, sub) {
			return
		}
		if !time.Now().Before(deadline) {
			d.t.Fatalf("view missing %q:\n%s", sub, out)
		}
		select {
		case msg := <-d.late:
			d.dispatch(msg, 0)
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// settle applies parked results until none arrive for a moment.
func (d *driver) settle() {
	for {
		select {
		case msg := <-d.late:
			d.dispatch(msg, 0)
		case <-time.After(300 * time.Millisecond):
			return
		}
	}
}

func TestFlowDoctorBackupRestoreUpgrade(t *testing.T) {
	d := newDriver(t, 120, 40)
	d.want("Capabilities")
	d.want("unix socket (HTTP)")
	d.settle()
	d.want("Doctor (2)") // badge from the quick pass: deprecated key + WAL

	// Doctor: a full run, then follow the deprecated-key fix to Server Settings.
	d.key("c")
	d.want("Doctor: 2 warnings")
	d.want("tls_letsencrypt_hostname is deprecated")
	for range 10 {
		if f, _ := d.srv.finding(); f.Action != "" {
			break
		}
		d.key("j")
	}
	d.want("f opens the fix in the app")
	d.key("f")
	d.want("SERVER SETTINGS STUB")
	if d.cfg.got != 1 {
		t.Fatalf("fix navigated %d times", d.cfg.got)
	}
	d.key("1") // back to the Server page

	// Backup: one key, listed with a summary toast.
	d.key("b")
	d.want("Backup 20261028T120000Z: 3 files")
	d.want("Backups (1)")

	// Restore: typed confirmation of the bundle ID, after a safety backup.
	d.h.ChangeOnDisk(hostfake.PolicyPath, []byte(`{"acls": []}`))
	d.key("r")
	d.want("Restore files from the selected backup")
	d.typeText("20261028T120000Z")
	d.key("enter")
	d.want("Restored 1 files")
	if got, _ := d.h.File(hostfake.PolicyPath); string(got) != string(hostfake.SeedPolicy) {
		t.Error("policy not restored")
	}
	d.want("Backups (2)")

	// Upgrade: compared with the injected releases, commands printed.
	d.key("U")
	d.want("Upgrade available")
	d.want("sudo apt install ./headscale_0.30.1_linux_")
	d.want("Fewer crashes")
}

func TestFlowBackfillNeedsTypedHostname(t *testing.T) {
	d := newDriver(t, 120, 40)
	d.want("Capabilities")
	d.key("F")
	d.want("Backfill machine IPs")
	d.typeText("wrong")
	d.key("enter")
	d.key("esc")
	d.settle()
	if strings.Contains(d.view(), "Backfill done") {
		t.Fatal("backfill ran without the typed hostname")
	}
	d.key("F")
	d.want("Backfill machine IPs")
	d.typeText("hs.demo.example.com")
	d.key("enter")
	d.want("Backfill done")
}
