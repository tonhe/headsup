package serverconfig

import (
	"bytes"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/tonhe/headsup/internal/api/fake"
	"github.com/tonhe/headsup/internal/app"
	"github.com/tonhe/headsup/internal/brand"
	"github.com/tonhe/headsup/internal/host"
	hostfake "github.com/tonhe/headsup/internal/host/fake"
	"github.com/tonhe/headsup/internal/module"
)

var flowNow = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

// driver runs the real app headlessly with only this module: commands run
// synchronously and results are fed back. A command still running after
// 150ms (a timer, or slow I/O on a loaded CI runner) is parked: its result
// lands in late and is applied only while want is waiting for the screen.
type driver struct {
	t    *testing.T
	m    *app.Model
	mod  *Model
	h    *hostfake.Host
	late chan tea.Msg
}

func newDriver(t *testing.T, h *hostfake.Host, readOnly bool) *driver {
	t.Helper()
	return newSizedDriver(t, h, readOnly, 120, 40, "unicode")
}

func newSizedDriver(t *testing.T, h *hostfake.Host, readOnly bool, w, ht int, ic string) *driver {
	t.Helper()
	clock := func() time.Time { return flowNow }
	mod := New()
	mod.settle = 0
	if h == nil {
		h = hostfake.New(hostfake.WithClock(clock))
	}
	m := app.New(app.Options{
		Backend: fake.Demo(fake.WithClock(clock)), Host: h, Modules: []module.Module{mod},
		Now: clock, Refresh: time.Hour, ReadOnly: readOnly, Theme: "tokyo-night", Profile: "host:hs-demo",
		ServerURL: "https://hs.demo.example.com", Icons: ic,
	})
	d := &driver{t: t, m: m, mod: mod, h: h, late: make(chan tea.Msg, 256)}
	d.send(tea.WindowSizeMsg{Width: w, Height: ht})
	d.exec(m.Init(), 0)
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
	case <-time.After(150 * time.Millisecond):
		go func() { d.late <- <-done }()
		return
	}
	d.dispatch(msg, depth)
}

// dispatch applies one command result and runs what it returns.
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
		case "backspace":
			msg = tea.KeyMsg{Type: tea.KeyBackspace}
		case "ctrl+s":
			msg = tea.KeyMsg{Type: tea.KeyCtrlS}
		case "ctrl+u":
			msg = tea.KeyMsg{Type: tea.KeyCtrlU}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		d.send(msg)
	}
}

func (d *driver) view() string { return ansi.Strip(d.m.View()) }

// want waits up to 5s for every sub to be on screen, applying the results
// of parked commands while it waits.
func (d *driver) want(subs ...string) {
	d.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		out, missing := d.view(), ""
		for _, s := range subs {
			if !strings.Contains(out, s) {
				missing = s
				break
			}
		}
		if missing == "" {
			return
		}
		if !time.Now().Before(deadline) {
			d.t.Fatalf("screen is missing %q:\n%s", missing, out)
		}
		select {
		case msg := <-d.late:
			d.dispatch(msg, 0)
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (d *driver) config() []byte {
	b, _ := d.h.File(hostfake.ConfigPath)
	return b
}

// gotoSection moves the module to a named section.
func (d *driver) gotoSection(name string) {
	d.t.Helper()
	for i := 0; i < len(sections); i++ {
		if d.mod.section().name == name {
			return
		}
		d.key("tab")
	}
	d.t.Fatalf("no section %s", name)
}

// gotoField puts the cursor on key within the current section.
func (d *driver) gotoField(key string) {
	d.t.Helper()
	for i, r := range d.mod.rows() {
		if r.field.Key == key {
			d.mod.cur = i
			return
		}
	}
	d.t.Fatalf("no field %s in %s", key, d.mod.section().name)
}

// editField opens key's editor, replaces its text with value and submits.
func (d *driver) editField(section, key, value string) {
	d.t.Helper()
	d.gotoSection(section)
	d.gotoField(key)
	d.key("enter", "ctrl+u", value, "enter")
}

func TestFlowEditDiffConfirmRestartRestore(t *testing.T) {
	d := newDriver(t, nil, false)
	d.want("Server Settings", "server_url", "needs restart")

	d.editField("Nodes", "node.expiry", "180d")
	d.want("unsaved changes", "180d")
	if !bytes.Equal(d.config(), hostfake.SeedConfig) {
		t.Fatal("staging an edit must not write the file")
	}

	// Diff of the staged edit.
	d.key("d")
	d.want("Unsaved changes", "+1 -1", "-   expiry: 0", "+   expiry: 180d")
	d.key("esc")

	// ctrl+s validates (configtest on the host) and shows the review.
	d.key("ctrl+s")
	d.want("Review config.yaml", "Valid.", "Needs a restart", "+   expiry: 180d")
	if d.h.Backups() != 0 {
		t.Fatal("review must not write")
	}

	// ctrl+s on the review asks for confirmation naming the file; n cancels.
	d.key("ctrl+s")
	d.want("Write config.yaml", "file config.yaml", "A backup")
	d.key("n")
	if d.h.Backups() != 0 || !bytes.Equal(d.config(), hostfake.SeedConfig) {
		t.Fatal("cancelled confirm must not write")
	}
	d.key("ctrl+s", "y")

	got := string(d.config())
	if !strings.Contains(got, "  expiry: 180d\n") {
		t.Fatalf("file not written:\n%s", got)
	}
	if !strings.Contains(got, "# Default key expiry for non-tagged nodes") || !strings.Contains(got, "# Octal, quoted") {
		t.Error("comments must survive the edit")
	}
	if d.h.Backups() != 1 {
		t.Errorf("backups = %d, want 1", d.h.Backups())
	}
	// Exactly one line changed.
	if n := diffCount(hostfake.SeedConfig, d.config()); n != 2 {
		t.Errorf("changed lines = %d, want 1 removed + 1 added", n)
	}

	// Restart offer, saying what it interrupts.
	d.want("Saved", "takes effect after headscale restarts", "reconnects", "X restart now")
	if d.mod.Badge(nil) != "!" {
		t.Errorf("badge = %q, want restart needed", d.mod.Badge(nil))
	}

	// An unhealthy restart shows logs and offers the restore.
	d.h.SetUnhealthyRestart(true)
	d.key("X")
	d.want("Restart headscale", "reconnects")
	d.key("y")
	d.want("Restart failed", "did not come back healthy", "state failed", "u restores the backup", "Last log lines")
	if st, _ := d.h.Service().Status(t.Context()); st.Running {
		t.Fatal("fake should be failed")
	}

	// u restores the backup and restarts again.
	d.h.SetUnhealthyRestart(false)
	d.key("u")
	d.want("Restore the backup and restart")
	d.key("y")
	if !bytes.Equal(d.config(), hostfake.SeedConfig) {
		t.Fatalf("restore should bring back the original:\n%s", d.config())
	}
	if st, _ := d.h.Service().Status(t.Context()); !st.Running {
		t.Error("service should be running after restore and restart")
	}
	d.want("Restored the backup", "headscale restarted")
	if d.mod.Badge(nil) != "" {
		t.Errorf("badge after restore = %q", d.mod.Badge(nil))
	}
	if d.mod.applied != nil {
		t.Error("restore is one-shot")
	}
}

func diffCount(a, b []byte) int {
	al, bl := strings.Split(string(a), "\n"), strings.Split(string(b), "\n")
	if len(al) != len(bl) {
		return -1
	}
	n := 0
	for i := range al {
		if al[i] != bl[i] {
			n += 2
		}
	}
	return n
}

func TestFlowHealthyRestart(t *testing.T) {
	d := newDriver(t, nil, false)
	d.editField("Nodes", "node.ephemeral.inactivity_timeout", "45m")
	d.key("ctrl+s", "ctrl+s", "y", "X", "y")
	d.want("headscale restarted and is")
	if !strings.Contains(string(d.config()), "inactivity_timeout: 45m") {
		t.Fatal("not written")
	}
	// Rollback without a restart pending afterwards: still one key.
	d.key("u")
	d.want("Restore the backup and restart")
	d.key("y")
	if !bytes.Equal(d.config(), hostfake.SeedConfig) {
		t.Fatal("rollback")
	}
}

func TestFlowInvalidValueRefusedBeforeWriting(t *testing.T) {
	d := newDriver(t, nil, false)
	// The form refuses a bad duration with a message and stays open.
	d.gotoSection("Nodes")
	d.gotoField("node.expiry")
	d.key("enter", "ctrl+u", "soon", "enter")
	d.want("invalid duration")
	d.key("esc")
	if d.mod.dirty() {
		t.Fatal("a refused value must not be staged")
	}

	// headscale's own check failing blocks the write.
	d.h.SetConfigtestFailure(host.Issue{Severity: host.SevError, Message: "node.expiry is not supported here"})
	d.editField("Nodes", "node.expiry", "30d")
	d.key("ctrl+s")
	d.want("Validation failed", "node.expiry is not supported here", "configtest")
	d.key("ctrl+s")
	d.want("nothing was written")
	if d.h.Backups() != 0 || !bytes.Equal(d.config(), hostfake.SeedConfig) {
		t.Fatal("an invalid change must leave the file byte-identical")
	}
}

func TestFlowChangedOnDiskShowsThreeWay(t *testing.T) {
	d := newDriver(t, nil, false)
	d.editField("Nodes", "node.expiry", "90d")
	d.key("ctrl+s")
	// Someone else edits another key meanwhile.
	theirs := bytes.Replace(hostfake.SeedConfig, []byte("level: info"), []byte("level: warn"), 1)
	d.h.ChangeOnDisk(hostfake.ConfigPath, theirs)
	d.key("ctrl+s", "y")
	d.want("changed on disk", "On disk now vs. when opened", "Your changes vs. when opened", "level: warn", "expiry: 90d")
	if !bytes.Equal(d.config(), theirs) {
		t.Fatal("a conflict must not overwrite")
	}
	d.key("r")
	d.want("Review config.yaml", "Valid.")
	d.key("ctrl+s", "y")
	got := string(d.config())
	if !strings.Contains(got, "level: warn") || !strings.Contains(got, "expiry: 90d") {
		t.Fatalf("rebased write should keep both changes:\n%s", got)
	}
}

func TestFlowChangedOnDiskSameKeyRefusesRebase(t *testing.T) {
	d := newDriver(t, nil, false)
	d.editField("Nodes", "node.expiry", "90d")
	d.key("ctrl+s")
	d.h.ChangeOnDisk(hostfake.ConfigPath, bytes.Replace(hostfake.SeedConfig, []byte("expiry: 0"), []byte("expiry: 7d"), 1))
	d.key("ctrl+s", "y", "r")
	d.want("also changed on disk: node.expiry")
	d.key("esc")
	if !d.mod.dirty() {
		t.Error("esc keeps the edits unsaved")
	}
}

func TestFlowCriticalFieldNeedsTypedName(t *testing.T) {
	d := newDriver(t, nil, false)
	d.editField("General", "listen_addr", "0.0.0.0:8443")
	d.key("ctrl+s", "ctrl+s")
	d.want("Type", "config.yaml", "to confirm")
	d.key("y", "enter")
	if d.h.Backups() != 0 {
		t.Fatal("a wrong confirmation must not write")
	}
	d.key("ctrl+u", "config.yaml", "enter")
	if !strings.Contains(string(d.config()), "listen_addr: 0.0.0.0:8443") {
		t.Fatal("typed confirmation should write")
	}
}

func TestFlowSecretIsMasked(t *testing.T) {
	cfg := append(bytes.Clone(hostfake.SeedConfig), []byte("oidc:\n  issuer: https://idp.example.com\n  client_id: hs\n  client_secret: hunter2-very-secret\n")...)
	h := hostfake.New(hostfake.WithFile(hostfake.ConfigPath, cfg))
	d := newDriver(t, h, false)
	d.gotoSection("OIDC")
	if strings.Contains(d.view(), "hunter2") {
		t.Fatal("secret shown in the field list")
	}
	d.gotoField("oidc.client_secret")
	d.key("enter")
	if strings.Contains(d.view(), "hunter2") {
		t.Fatal("secret shown in the editor")
	}
	d.key("rotated-secret-value", "enter")
	d.key("d")
	if out := d.view(); strings.Contains(out, "hunter2") || strings.Contains(out, "rotated-secret") {
		t.Fatalf("secret shown in the diff:\n%s", out)
	}
	d.key("esc", "ctrl+s")
	if strings.Contains(d.view(), "rotated-secret") {
		t.Fatal("secret shown in the review")
	}
	d.key("ctrl+s", "y")
	if !strings.Contains(string(d.config()), "client_secret: rotated-secret-value") {
		t.Fatal("secret not written")
	}
}

func TestFlowMigrateFlatTLS(t *testing.T) {
	flat := []byte("server_url: https://hs.example.com\nlisten_addr: 0.0.0.0:8080\n# Certificate\ntls_cert_path: /etc/ssl/hs.pem\ntls_key_path: /etc/ssl/hs.key\nacme_email: admin@example.com\ngrpc_listen_addr: 127.0.0.1:50443\nlog:\n  level: info\n")
	h := hostfake.New(hostfake.WithFile(hostfake.ConfigPath, flat))
	d := newDriver(t, h, false)
	d.gotoSection("TLS")
	d.want("tls_cert_path", "tls.cert_path", "m migrate")
	d.key("m")
	d.want("Review config.yaml", "Valid.", "+   cert_path: /etc/ssl/hs.pem")
	d.key("ctrl+s")
	// listen_addr is not touched, so a y/N is enough.
	d.key("y")
	got := string(d.config())
	for _, want := range []string{"tls:\n  cert_path: /etc/ssl/hs.pem\n  key_path: /etc/ssl/hs.key\n  acme:\n    email: admin@example.com", "# Certificate"} {
		if !strings.Contains(got, want) {
			t.Errorf("migrated file missing %q:\n%s", want, got)
		}
	}
	for _, gone := range []string{"tls_cert_path", "grpc_listen_addr", "acme_email"} {
		if strings.Contains(got, gone) {
			t.Errorf("%s should be gone:\n%s", gone, got)
		}
	}
	d.key("esc")
	d.key("m")
	d.want("No deprecated keys")
}

func TestFlowReadOnlyBlocksEdits(t *testing.T) {
	d := newDriver(t, nil, true)
	d.gotoSection("Nodes")
	d.key("enter")
	d.want("read-only")
	if d.mod.dirty() {
		t.Fatal("read-only must not stage")
	}
}

func TestFlowLinkSectionNavigates(t *testing.T) {
	d := newDriver(t, nil, false)
	d.gotoSection("DNS")
	d.want("DNS settings have their own editor", "g d")
	d.key("enter") // navigates to "dns", which is not registered here: no panic
	d.gotoSection("Database")
	d.want("View only")
	d.key("enter")
	d.want("view only")
}

func TestFlowDiscardAndReset(t *testing.T) {
	d := newDriver(t, nil, false)
	d.editField("Logging", "log.format", "json")
	d.key("U", "y")
	if d.mod.dirty() {
		t.Fatal("discard")
	}
	d.gotoField("log.format")
	d.key("x")
	if !d.mod.dirty() || d.mod.doc.Has("log.format") {
		t.Fatal("reset removes the key")
	}
	d.key("ctrl+s")
	d.want("-   format: text")
}

func TestFlowPermissionDeniedShowsFix(t *testing.T) {
	h := hostfake.New()
	h.FailPermission(host.NeedConfigW, "sudo -u headscale "+brand.Name)
	d := newDriver(t, h, false)
	d.editField("Nodes", "node.expiry", "1d")
	d.key("ctrl+s", "ctrl+s")
	d.want("Fix: sudo -u")
	if d.h.Backups() != 0 {
		t.Fatal("no write without permission")
	}
}

func TestFlowEditorResultGoesThroughPipeline(t *testing.T) {
	d := newDriver(t, nil, false)
	next := bytes.Replace(hostfake.SeedConfig, []byte("format: text"), []byte("format: json"), 1)
	d.send(editedMsg{data: next})
	d.want("Review config.yaml", "+   format: json")
	d.key("ctrl+s", "y")
	if !strings.Contains(string(d.config()), "format: json") {
		t.Fatal("editor change not written")
	}
	// Broken YAML from the editor is refused by validation.
	d.key("esc")
	d.send(editedMsg{data: []byte("log: [unclosed\n")})
	d.want("Validation failed", "YAML")
	d.key("esc")
	d.want("not valid YAML")
	d.send(editedMsg{data: d.mod.file.Raw})
	if d.mod.dirty() {
		t.Error("editor text equal to the file clears the change")
	}
}
