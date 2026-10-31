package server

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tonhe/headsup/internal/api"
	"github.com/tonhe/headsup/internal/config"
	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/host/doctor"
	hostfake "github.com/tonhe/headsup/internal/host/fake"
	"github.com/tonhe/headsup/internal/host/upgrade"
	"github.com/tonhe/headsup/internal/icons"
	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/ui/uitest"
)

func row(t *testing.T, rows []CapRow, feature string) CapRow {
	t.Helper()
	for _, r := range rows {
		if r.Feature == feature {
			return r
		}
	}
	t.Fatalf("no matrix row %q", feature)
	return CapRow{}
}

// The capability matrix is accurate for 0.29 and 0.30 in both modes (US16).
func TestMatrixByVersionAndMode(t *testing.T) {
	for _, tt := range []struct {
		ver      string
		hostMode bool
	}{{"0.29.6", false}, {"0.29.6", true}, {"0.30.0", false}, {"0.30.0", true}, {"0.29.0", false}} {
		var h host.Host
		if tt.hostMode {
			h = hostfake.New()
		}
		rows := Matrix(api.CapsForVersion(tt.ver), tt.ver, h)
		caps := api.CapsForVersion(tt.ver)
		for _, c := range api.AllCaps() {
			r := row(t, rows, capTitle(c))
			want := AvailNo
			if caps.Has(c) {
				want = AvailYes
			}
			if c == api.CapPolicyWritable && !caps.Has(c) && tt.hostMode {
				want = AvailPartial
			}
			if r.Now != want {
				t.Errorf("%s host=%v %s: now %v, want %v (%s)", tt.ver, tt.hostMode, r.Feature, r.Now, want, r.Why)
			}
		}
		tag := row(t, rows, "Tag-only pre-auth keys")
		if strings.HasPrefix(tt.ver, "0.29") && tag.Why != "needs headscale 0.30" {
			t.Errorf("%s: tag-only reason %q", tt.ver, tag.Why)
		}
		de := row(t, rows, "Disable node expiry")
		if tt.ver == "0.29.0" && de.Why != "needs headscale 0.29.6" {
			t.Errorf("0.29.0: disable expiry reason %q", de.Why)
		}
		dns := row(t, rows, "DNS: MagicDNS, nameservers, records")
		if dns.Remote != AvailNo || dns.Host != AvailYes || !strings.Contains(dns.Note, "Config file only") || !strings.Contains(dns.Note, "https://headscale.net/stable/ref/dns/") {
			t.Errorf("dns row %+v", dns)
		}
		if tt.hostMode != (dns.Now == AvailYes) {
			t.Errorf("%s host=%v: dns now %v (%s)", tt.ver, tt.hostMode, dns.Now, dns.Why)
		}
		if !tt.hostMode && dns.Why != "needs host mode" {
			t.Errorf("remote dns reason %q", dns.Why)
		}
		up := row(t, rows, "Upgrade assistant")
		if !tt.hostMode && up.Now != AvailPartial {
			t.Errorf("remote upgrade: %+v", up)
		}
	}
}

func TestMatrixMissingPermission(t *testing.T) {
	h := hostfake.New()
	h.FailPermission(host.NeedService, "sudo systemctl restart headscale")
	r := row(t, Matrix(api.CapsForVersion("0.30.0"), "0.30.0", h), "Service start, stop, restart")
	if r.Now != AvailNo || r.Why != "no access" || !strings.Contains(r.Note, "Fix: sudo systemctl restart headscale") {
		t.Errorf("no service access: %+v", r)
	}
}

func TestModuleBasics(t *testing.T) {
	m := New()
	if m.ID() != "server" || m.Title() != "Server" || m.Icon(icons.ASCII) != icons.ASCII.Server || m.Modes() != module.ModeAPIHost || m.Chord() != "S" || m.Capturing() {
		t.Error("metadata")
	}
	if m.Badge(nil) != "" {
		t.Error("badge before the doctor ran")
	}
	seen := map[string]bool{}
	for _, a := range m.Actions() {
		for _, k := range a.Keys {
			if seen[k] {
				t.Errorf("key %q bound twice", k)
			}
			seen[k] = true
		}
	}
	for _, k := range []string{"c", "b", "B", "U", "F", "r", "o"} {
		if !seen[k] {
			t.Errorf("contract key %q missing", k)
		}
	}
	if a := action(t, m, "server.backfill"); a.Danger != module.DangerCritical || a.Cap != api.CapBackfillIPs || !a.Mutating {
		t.Errorf("backfill gating %+v", a)
	}
	if a := action(t, m, "server.restore"); a.Danger != module.DangerCritical || !a.Mutating || a.Needs != host.NeedConfigW {
		t.Errorf("restore gating %+v", a)
	}
	if a := action(t, m, "server.backup"); a.Mutating || a.Needs != host.NeedConfigR {
		t.Errorf("backup does not change the server: %+v", a)
	}
}

func TestRemoteModeGates(t *testing.T) {
	c := newCtx(t, "0.29.6", false)
	m := New()
	if cmd := m.Init(c); cmd != nil {
		t.Error("remote mode needs no host commands at start")
	}
	for _, id := range []string{"server.doctor", "server.backup", "server.openconfig"} {
		if ok, why := action(t, m, id).Enabled(c); ok || why == "" {
			t.Errorf("%s should be disabled with a reason in remote mode", id)
		}
	}
	if got := action(t, m, "server.backfill").Targets(c); len(got) != 1 || got[0].Name != "hs.example.com" {
		t.Errorf("backfill target %+v", got)
	}
	// Backfill runs through the backend.
	res := drain(t, c, m, action(t, m, "server.backfill").Run(c, nil))
	if len(res) != 1 || res[0].Err != nil || !strings.Contains(res[0].Message, "Backfill done") {
		t.Errorf("backfill %+v", res)
	}
}

func TestDoctorFlow(t *testing.T) {
	uitest.Screen(t, func(t *testing.T, ic icons.Set) string {
		c := newCtx(t, "", true)
		c.ic = ic
		c.h = hostfake.New(hostfake.WithFile(hostfake.ConfigPath, append(append([]byte{}, hostfake.SeedConfig...), "\ntls_letsencrypt_hostname: hs.example.com\n"...)))
		m := New()
		m.root = filepath.Join(t.TempDir(), "bundles")
		drain(t, c, m, m.Init(c))
		if !m.docRan || m.docFull {
			t.Fatal("Init runs a quick doctor pass without the clock check")
		}
		if b := m.Badge(c.st); b != "2" {
			t.Errorf("badge %q (deprecated key + demo WAL)", b)
		}
		clock := false
		for _, f := range m.findings {
			if f.Check == doctor.CheckClock && f.Severity == doctor.SevSkipped {
				clock = true
			}
		}
		if !clock {
			t.Error("the quick pass must skip the clock check")
		}
		res := drain(t, c, m, action(t, m, "server.doctor").Run(c, nil))
		if len(res) != 1 || !strings.Contains(res[0].Message, "2 warnings") || !m.docFull || m.tab != tabDoctor {
			t.Errorf("explicit run %+v", res)
		}
		// Move to the deprecated-key finding and follow its fix.
		for i := 0; i < len(m.findings); i++ {
			if f, _ := m.finding(); f.Action != "" {
				break
			}
			m.Update(c, keyMsg("j"))
		}
		fix := action(t, m, "server.fix")
		if ok, why := fix.Enabled(c); !ok {
			t.Fatalf("fix disabled: %s", why)
		}
		fix.Run(c, nil)
		if len(c.navs) != 1 || c.navs[0].id != "serverconfig" {
			t.Errorf("navs %+v", c.navs)
		}
		if ok, _ := action(t, m, "server.copyfix").Enabled(c); !ok {
			t.Error("copy fix should be enabled")
		}
		// A command-only fix cannot be "opened".
		m.Update(c, keyMsg("k"))
		for f, _ := m.finding(); f.Action != ""; f, _ = m.finding() {
			m.Update(c, keyMsg("k"))
		}
		if ok, why := fix.Enabled(c); ok || !strings.Contains(why, "copies") {
			t.Errorf("command-only fix: %v %q", ok, why)
		}
		return m.View(c, module.Rect{W: 100, H: 34})
	})
}

func TestDoctorUsesInjectedProbesAndThresholds(t *testing.T) {
	c := newCtx(t, "", true)
	cfg := config.Default().Host
	cfg.Doctor.DiskWarnPct = 60
	c.cfg = &cfg
	sc := settingsCtx{c}
	m := New()
	p := doctor.SimulatedProbes(c.h, c.Now)
	p.Stat = func(string) (fs.FileInfo, error) { return nil, fs.ErrNotExist }
	m.probes = &p
	drain(t, sc, m, action(t, m, "server.doctor").Run(sc, nil))
	var disk doctor.Finding
	for _, f := range m.findings {
		if f.Check == doctor.CheckDisk {
			disk = f
		}
	}
	if disk.Severity != doctor.SevWarn {
		t.Errorf("configured threshold ignored: %+v", disk)
	}
}

func TestBackupRestoreFlow(t *testing.T) {
	c := newCtx(t, "", true)
	m := New()
	m.root = filepath.Join(t.TempDir(), "b", "bundles")
	drain(t, c, m, m.Init(c))
	if len(m.bundles) != 0 {
		t.Fatal("no bundles yet")
	}
	b := action(t, m, "server.backup")
	if ok, why := b.Enabled(c); !ok {
		t.Fatal(why)
	}
	if tg := b.Targets(c); len(tg) != 1 || tg[0].Name != m.root {
		t.Errorf("targets %+v", tg)
	}
	res := drain(t, c, m, b.Run(c, nil))
	if len(res) != 1 || res[0].Err != nil || !strings.Contains(res[0].Message, "3 files + database") {
		t.Fatalf("backup %+v", res)
	}
	if len(m.bundles) != 1 || m.tab != tabBackups {
		t.Fatalf("bundles %+v", m.bundles)
	}
	out := m.View(c, module.Rect{W: 100, H: 30})
	for _, want := range []string{"20261028T120000Z", "3 (2 missing)", "simulated", "Backups (1)"} {
		if !strings.Contains(out, want) {
			t.Errorf("backups view lacks %q:\n%s", want, out)
		}
	}

	// Change the policy, then restore it.
	c.h.ChangeOnDisk(hostfake.PolicyPath, []byte(`{"acls": []}`))
	r := action(t, m, "server.restore")
	if ok, why := r.Enabled(c); !ok {
		t.Fatal(why)
	}
	if tg := r.Targets(c); len(tg) != 1 || tg[0].Name != "20261028T120000Z" {
		t.Errorf("restore target %+v", tg)
	}
	res = drain(t, c, m, r.Run(c, nil))
	if len(res) != 1 || res[0].ActionID != "server.restore" || res[0].Err != nil || !strings.Contains(res[0].Message, "Restored 1 files") {
		t.Fatalf("restore %+v", res)
	}
	if got, _ := c.h.File(hostfake.PolicyPath); string(got) != string(hostfake.SeedPolicy) {
		t.Error("policy not restored")
	}
	// The safety backup was taken first (same second, so -2).
	if len(m.bundles) != 2 {
		t.Errorf("safety backup missing: %+v", m.bundles)
	}
	if out := m.View(c, module.Rect{W: 100, H: 30}); !strings.Contains(out, "restored 1 files") {
		t.Errorf("restore result not shown:\n%s", out)
	}
}

func TestBackupFailureIsReported(t *testing.T) {
	c := newCtx(t, "", true)
	c.h.FailPermission(host.NeedConfigR, "sudo usermod -aG headscale $USER")
	m := New()
	m.root = filepath.Join(t.TempDir(), "b", "bundles")
	drain(t, c, m, m.Init(c))
	m.bkBusy = ""
	res := drain(t, c, m, action(t, m, "server.backup").Run(c, nil))
	var pe *host.ErrPermission
	if len(res) != 1 || !errors.As(res[0].Err, &pe) {
		t.Fatalf("backup failure %+v", res)
	}
	if m.bkBusy != "" {
		t.Error("busy flag left set")
	}
	// A failing safety backup stops the restore.
	m.bundles = nil
	msg := backupMsg{err: errors.New("disk full"), then: &restoreReq{id: "x"}}
	got := drain(t, c, m, m.onBackup(msg))
	if len(got) != 1 || got[0].ActionID != "server.restore" || !strings.Contains(got[0].Err.Error(), "restore not started") {
		t.Errorf("safety failure %+v", got)
	}
}

type staticSource []upgrade.Release

func (s staticSource) Releases(context.Context) ([]upgrade.Release, error) { return s, nil }

type errSource struct{}

func (errSource) Releases(context.Context) ([]upgrade.Release, error) {
	return nil, errors.New("GitHub answered 403 rate limit exceeded")
}

func TestUpgradeCard(t *testing.T) {
	card := func(t *testing.T, ic icons.Set) (*Model, *testCtx, []module.ActionResult) {
		c := newCtx(t, "0.29.6", true)
		c.ic = ic
		m := New()
		m.arch = "amd64" // the golden shows linux_amd64 whatever the runner is
		m.src = staticSource{
			{Tag: "v0.29.6", Body: "old"},
			{Tag: "v0.30.0", Published: testNow.Add(-30 * 24 * time.Hour), Body: "## Breaking\n- `tls_*` keys move under `tls:`"},
			{Tag: "v0.30.1", Published: testNow.Add(-24 * time.Hour), Body: "## Fixes\n- [Crash](https://x) fixed"},
		}
		res := drain(t, c, m, action(t, m, "server.upgrade").Run(c, nil))
		if len(res) != 1 || res[0].Message != "Upgrade available: v0.30.1." {
			t.Fatalf("upgrade %+v", res)
		}
		return m, c, res
	}
	uitest.Screen(t, func(t *testing.T, ic icons.Set) string {
		m, c, _ := card(t, ic)
		return m.View(c, module.Rect{W: 110, H: 40})
	})
	m, c, _ := card(t, icons.Unicode)
	before := m.upTop
	for range 3 {
		m.Update(c, keyMsg("j"))
	}
	m.View(c, module.Rect{W: 110, H: 20})
	if m.upTop <= before {
		t.Error("the card should scroll")
	}
	m.Update(c, keyMsg("home"))
	if m.upTop != 0 {
		t.Error("home")
	}
}

func TestUpgradeStates(t *testing.T) {
	c := newCtx(t, "0.30.0", false)
	m := New()
	if out := m.upgradeLines(c, 80); !strings.Contains(strings.Join(out, " "), "Press U") {
		t.Errorf("idle card %q", out)
	}
	m.src = errSource{}
	res := drain(t, c, m, action(t, m, "server.upgrade").Run(c, nil))
	if len(res) != 1 || res[0].Err == nil || !strings.Contains(strings.Join(m.upgradeLines(c, 80), " "), "rate limit") {
		t.Errorf("error card %+v", res)
	}
	m.src = staticSource{{Tag: "v0.30.0"}}
	res = drain(t, c, m, action(t, m, "server.upgrade").Run(c, nil))
	if res[0].Message != "headscale 0.30.0 is up to date." || !strings.Contains(strings.Join(m.upgradeLines(c, 80), " "), "Up to date") {
		t.Errorf("up to date %+v", res)
	}
	// Turned off in the config: no fetch.
	cfg := config.Default().Host
	cfg.CheckUpdates = false
	c.cfg = &cfg
	sc := settingsCtx{c}
	m.src = errSource{}
	res = drain(t, sc, m, action(t, m, "server.upgrade").Run(sc, nil))
	if res[0].Err != nil || !strings.Contains(res[0].Message, "turned off") {
		t.Errorf("disabled %+v", res)
	}
	// headscale's own disable_check_updates is honoured in host mode.
	h := newCtx(t, "0.30.0", true)
	h.h = hostfake.New(hostfake.WithFile(hostfake.ConfigPath, []byte("disable_check_updates: true\n")))
	res = drain(t, h, m, action(t, m, "server.upgrade").Run(h, nil))
	if !strings.Contains(res[0].Message, "turned off") {
		t.Errorf("headscale disabled %+v", res)
	}
	// The demo host never contacts GitHub.
	d := newCtx(t, "0.30.0", true)
	m2 := New()
	m2.root = t.TempDir()
	res = drain(t, d, m2, action(t, m2, "server.upgrade").Run(d, nil))
	if res[0].Message != "Upgrade available: v0.30.1." {
		t.Errorf("demo releases %+v", res)
	}
	// A development build cannot be planned.
	dev := newCtx(t, "dev", false)
	m2.src = staticSource{{Tag: "v0.30.1"}}
	res = drain(t, dev, m2, action(t, m2, "server.upgrade").Run(dev, nil))
	if !strings.Contains(res[0].Message, "not a release version") {
		t.Errorf("dev %+v", res)
	}
}

func TestViewsAndTabs(t *testing.T) {
	for _, tt := range []struct {
		name     string
		ver      string
		hostMode bool
		w, h     int
	}{
		{"remote_029_80x24", "0.29.6", false, 80, 24},
		{"host_030_120x40", "0.30.0", true, 120, 40},
		{"host_030_80x24", "0.30.0", true, 80, 24},
	} {
		t.Run(tt.name, func(t *testing.T) {
			load := func(t *testing.T, ic icons.Set) (*Model, *testCtx) {
				c := newCtx(t, tt.ver, tt.hostMode)
				c.ic = ic
				m := New()
				m.root = filepath.Join(t.TempDir(), "bundles")
				drain(t, c, m, m.Init(c))
				return m, c
			}
			uitest.Screen(t, func(t *testing.T, ic icons.Set) string {
				m, c := load(t, ic)
				out := m.View(c, module.Rect{W: tt.w, H: tt.h})
				checkFrame(t, out, tt.w, tt.h)
				return out
			})
			m, c := load(t, icons.Unicode)
			for range tabCount {
				m.Update(c, keyMsg("tab"))
				checkFrame(t, m.View(c, module.Rect{W: tt.w, H: tt.h}), tt.w, tt.h)
			}
			if m.tab != tabCaps {
				t.Error("tab should wrap around")
			}
			m.Update(c, keyMsg("shift+tab"))
			if m.tab != tabUpgrade {
				t.Error("shift+tab")
			}
			if !tt.hostMode {
				m.tab = tabDoctor
				if out := m.View(c, module.Rect{W: tt.w, H: tt.h}); !strings.Contains(out, "needs host mode") {
					t.Errorf("remote doctor tab:\n%s", out)
				}
			}
			if len(m.Hints()) == 0 {
				t.Error("hints")
			}
		})
	}
	if New().View(newCtx(t, "", false), module.Rect{W: 10, H: 3}) != "" {
		t.Error("tiny area renders nothing")
	}
}

func TestHintsPerTab(t *testing.T) {
	m := New()
	for tb := tab(0); tb < tabCount; tb++ {
		m.tab = tb
		if len(m.Hints()) < 2 {
			t.Errorf("tab %d hints", tb)
		}
	}
}

func TestOpenConfig(t *testing.T) {
	c := newCtx(t, "", true)
	m := New()
	a := action(t, m, "server.openconfig")
	if ok, _ := a.Enabled(c); !ok {
		t.Fatal("config found on disk: open should be enabled")
	}
	a.Run(c, nil)
	if len(c.navs) != 1 || c.navs[0].id != "serverconfig" {
		t.Errorf("navs %+v", c.navs)
	}
}

func TestWrap(t *testing.T) {
	got := wrap("aaa bbb ccc https://example.com/a/very/long/path", 10)
	for _, l := range got {
		if len(l) > 10 {
			t.Errorf("line %q longer than 10", l)
		}
	}
	if strings.Join(got, "") != "aaa bbbccchttps://example.com/a/very/long/path" {
		t.Errorf("wrap lost text: %q", got)
	}
	if wrap("x", 0) != nil {
		t.Error("zero width")
	}
}

func checkFrame(t *testing.T, out string, w, h int) {
	t.Helper()
	lines := strings.Split(out, "\n")
	if len(lines) > h {
		t.Errorf("frame has %d lines, area is %d", len(lines), h)
	}
	for i, l := range lines {
		if lw := uitest.MaxWidth(l); lw > w {
			t.Errorf("line %d is %d wide, area is %d", i, lw, w)
		}
	}
}
