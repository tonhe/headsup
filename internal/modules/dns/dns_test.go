package dns

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/tonhe/headsup/internal/api"
	apifake "github.com/tonhe/headsup/internal/api/fake"
	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/host/fake"
	"github.com/tonhe/headsup/internal/icons"
	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/store"
	"github.com/tonhe/headsup/internal/ui/styles"
	"github.com/tonhe/headsup/internal/ui/uitest"
)

type tctx struct {
	h  host.Host
	st *store.Store
	s  *styles.Styles
	ro bool
}

func (c *tctx) Backend() api.Backend                    { return apifake.Demo() }
func (c *tctx) Host() host.Host                         { return c.h }
func (c *tctx) Store() *store.Store                     { return c.st }
func (c *tctx) Styles() *styles.Styles                  { return c.s }
func (c *tctx) Icons() icons.Set                        { return icons.Unicode }
func (c *tctx) ReadOnly() bool                          { return c.ro }
func (c *tctx) HostMode() bool                          { return c.h != nil }
func (c *tctx) ServerURL() string                       { return "https://hs.demo.example.com" }
func (c *tctx) Profile() string                         { return "test" }
func (c *tctx) Now() time.Time                          { return time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC) }
func (c *tctx) Navigate(string, any) tea.Cmd            { return nil }
func (c *tctx) Toast(module.ToastLevel, string) tea.Cmd { return nil }
func (c *tctx) Refresh() tea.Cmd                        { return nil }

type harness struct {
	t       *testing.T
	m       *Model
	ctx     *tctx
	h       *fake.Host
	results []module.ActionResult
}

func newHarness(t *testing.T, opts ...fake.Option) *harness {
	t.Helper()
	h := fake.New(opts...)
	x := &harness{t: t, m: New(), h: h, ctx: &tctx{h: h, st: store.New(), s: uitest.StyleFor("tokyo-night")}}
	x.run(x.m.Init(x.ctx))
	if !x.m.loaded || x.m.loadErr != nil {
		t.Fatalf("load: %v", x.m.loadErr)
	}
	return x
}

func (x *harness) run(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case nil:
	case tea.BatchMsg:
		for _, c := range msg {
			x.run(c)
		}
	case module.ActionResult:
		x.results = append(x.results, msg)
	default:
		x.send(msg)
	}
}

func (x *harness) send(msg tea.Msg) {
	_, cmd := x.m.Update(x.ctx, msg)
	x.run(cmd)
}

func (x *harness) keys(ks ...string) {
	for _, k := range ks {
		var msg tea.KeyMsg
		switch k {
		case "tab":
			msg = tea.KeyMsg{Type: tea.KeyTab}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		x.send(msg)
	}
}

func (x *harness) action(id string) module.Action {
	for _, a := range x.m.Actions() {
		if a.ID == id {
			return a
		}
	}
	x.t.Fatalf("no action %s", id)
	return module.Action{}
}

// enabled reports the action's gate verdict (including read-only for mutating actions).
func (x *harness) enabled(id string) (bool, string) {
	a := x.action(id)
	if a.Mutating && x.ctx.ro {
		return false, "read-only"
	}
	if a.Enabled == nil {
		return true, ""
	}
	return a.Enabled(x.ctx)
}

// invoke runs an action like the app: gate, form defaults overridden by vals, run.
func (x *harness) invoke(id string, vals map[string]string) module.ActionResult {
	x.t.Helper()
	if ok, why := x.enabled(id); !ok {
		x.t.Fatalf("%s disabled: %s", id, why)
	}
	a := x.action(id)
	v := module.Values{}
	if a.Params != nil {
		if spec := a.Params(x.ctx); spec != nil {
			for _, f := range spec.Fields {
				v[f.ID] = f.Value
				if f.Kind == module.FieldSelect && f.Value == "" && len(f.Options) > 0 {
					v[f.ID] = f.Options[0]
				}
			}
		}
	}
	for k, val := range vals {
		v[k] = val
	}
	n := len(x.results)
	x.run(a.Run(x.ctx, v))
	if len(x.results) == n {
		return module.ActionResult{ActionID: id}
	}
	r := x.results[len(x.results)-1]
	return r
}

func (x *harness) view(w, h int) string {
	x.t.Helper()
	out := x.m.View(x.ctx, module.Rect{W: w, H: h})
	lines := strings.Split(out, "\n")
	if len(lines) > h {
		x.t.Errorf("view is %d lines, area is %d", len(lines), h)
	}
	for i, l := range lines {
		if lw := lipgloss.Width(l); lw > w {
			x.t.Errorf("line %d is %d wide, area is %d: %q", i, lw, w, ansi.Strip(l))
		}
	}
	return ansi.Strip(out)
}

func (x *harness) selectRow(kind rowKind, domain string) {
	x.t.Helper()
	x.m.focus = focusSettings
	for i, r := range x.m.rows {
		if r.kind == kind && (domain == "" || r.domain == domain) {
			x.m.row = i
			return
		}
	}
	x.t.Fatalf("no row %v %q", kind, domain)
}

func file(t *testing.T, h *fake.Host, path string) string {
	t.Helper()
	b, ok := h.File(path)
	if !ok {
		t.Fatalf("no file %s", path)
	}
	return string(b)
}

func TestLoadAndView(t *testing.T) {
	x := newHarness(t)
	for _, sz := range [][2]int{{80, 24}, {120, 40}, {60, 14}} {
		out := x.view(sz[0], sz[1])
		for _, want := range []string{"DNS settings", "MagicDNS", "Extra records", "live"} {
			if !strings.Contains(out, want) {
				t.Errorf("%dx%d view missing %q:\n%s", sz[0], sz[1], want, out)
			}
		}
	}
	out := x.view(120, 40)
	for _, want := range []string{"tail.demo.example.com", "1.1.1.1", "grafana.tail.demo.example.com", "needs restart", fake.RecordsPath} {
		if !strings.Contains(out, want) {
			t.Errorf("view missing %q", want)
		}
	}
	if x.m.dirty() || x.m.Badge(nil) != "" {
		t.Error("a fresh load is clean")
	}
	if ok, _ := x.enabled("dns.apply"); ok {
		t.Error("apply with nothing staged")
	}
	if ok, _ := x.enabled("dns.move"); ok {
		t.Error("move is only for inline records")
	}
	if x.m.ID() != "dns" || x.m.Chord() != "d" || x.m.Modes() != module.ModeHost || x.m.Title() == "" || x.m.Icon(icons.ASCII) == "" || x.m.Order() == 0 || x.m.Capturing() {
		t.Error("module metadata")
	}
	if len(x.m.Hints()) == 0 {
		t.Error("hints")
	}
}

func TestRemoteMode(t *testing.T) {
	m := New()
	ctx := &tctx{st: store.New(), s: uitest.StyleFor("")}
	if m.Init(ctx) != nil {
		t.Error("no host: nothing to load")
	}
	if out := ansi.Strip(m.View(ctx, module.Rect{W: 80, H: 20})); !strings.Contains(out, "host mode") {
		t.Errorf("remote view should explain host mode:\n%s", out)
	}
}

// US13 independent test: add an A record; the file is written atomically
// and applies live (no restart needed).
func TestAddRecordAppliesLive(t *testing.T) {
	x := newHarness(t)
	x.keys("tab")
	if x.m.focus != focusRecords {
		t.Fatal("tab should focus records")
	}
	r := x.invoke("dns.add", map[string]string{"name": "wiki.tail.demo.example.com", "type": "A", "value": "100.64.0.7"})
	if r.Err != nil || !strings.Contains(r.Message, "Staged") {
		t.Fatalf("stage: %+v", r)
	}
	if !x.m.dirty() || len(x.m.changes) != 1 || x.m.changes[0].File.Path != fake.RecordsPath {
		t.Fatalf("pending: %+v", x.m.changes)
	}
	out := x.view(120, 40)
	if !strings.Contains(out, "Pending changes") || !strings.Contains(out, "+ ") || !strings.Contains(out, "wiki.tail.demo.example.com") {
		t.Errorf("pending pane should show the diff:\n%s", out)
	}
	a := x.action("dns.apply")
	if a.Danger != module.DangerConfirm || len(a.Targets(x.ctx)) != 1 || !strings.Contains(a.Consequence, "apply immediately") || a.Needs != host.NeedRecordsW {
		t.Errorf("apply action: %+v", a)
	}
	if ok, why := x.enabled("dns.apply"); !ok {
		t.Fatalf("apply disabled: %s", why)
	}
	backups := x.h.Backups()
	r = x.invoke("dns.apply", nil)
	if r.Err != nil || !strings.Contains(r.Message, "Records apply now") {
		t.Fatalf("apply: %+v", r)
	}
	if got := file(t, x.h, fake.RecordsPath); !strings.Contains(got, `"name": "wiki.tail.demo.example.com"`) || !strings.Contains(got, `"value": "100.64.0.5"`) {
		t.Errorf("records file:\n%s", got)
	}
	if x.h.Backups() != backups+1 {
		t.Error("apply should back the file up")
	}
	if x.m.Badge(nil) != "" {
		t.Error("records apply live: no restart badge")
	}
	if x.m.dirty() {
		t.Error("clean after apply and reload")
	}
	// Roll back.
	if ok, _ := x.enabled("dns.rollback"); !ok {
		t.Fatal("rollback should be offered")
	}
	if r := x.invoke("dns.rollback", nil); r.Err != nil {
		t.Fatal(r.Err)
	}
	if got := file(t, x.h, fake.RecordsPath); got != string(fake.SeedRecords) {
		t.Errorf("rollback should restore the seed:\n%s", got)
	}
}

func TestEditAndDeleteRecord(t *testing.T) {
	x := newHarness(t)
	x.keys("tab", "j")
	if r := x.invoke("dns.edit", map[string]string{"value": "100.64.0.33"}); r.Err != nil {
		t.Fatal(r.Err)
	}
	if r := x.invoke("dns.edit", map[string]string{"type": "AAAA", "value": "100.64.0.1"}); r.Err == nil {
		t.Error("an AAAA record with an IPv4 value must be refused")
	}
	x.keys("j")
	if r := x.invoke("dns.delete", nil); r.Err != nil {
		t.Fatal(r.Err)
	}
	x.invoke("dns.apply", nil)
	got := file(t, x.h, fake.RecordsPath)
	if !strings.Contains(got, "100.64.0.33") || strings.Contains(got, "AAAA") {
		t.Errorf("records file:\n%s", got)
	}
}

func TestSettingNeedsRestart(t *testing.T) {
	x := newHarness(t)
	x.selectRow(rowMagic, "")
	if r := x.invoke("dns.edit", map[string]string{"value": "false"}); r.Err != nil || !strings.Contains(r.Message, "restart") {
		t.Fatalf("stage: %+v", r)
	}
	// MagicDNS off is fine; override without nameservers is not.
	x.selectRow(rowGlobal, "")
	x.invoke("dns.edit", map[string]string{"value": ""})
	if ok, why := x.enabled("dns.apply"); ok || !strings.Contains(why, "errors") {
		t.Errorf("override_local_dns without nameservers should block apply: %v %q", ok, why)
	}
	if out := x.view(120, 40); !strings.Contains(out, "global nameserver") {
		t.Errorf("the check should be shown:\n%s", out)
	}
	x.invoke("dns.edit", map[string]string{"value": "9.9.9.9, https://dns.quad9.net/dns-query"})
	a := x.action("dns.apply")
	if a.Needs != host.NeedConfigW || !strings.Contains(a.Consequence, "restarts") {
		t.Errorf("config change: %+v", a)
	}
	if r := x.invoke("dns.apply", nil); r.Err != nil || !strings.Contains(r.Message, "Restart") {
		t.Fatalf("apply: %+v", r)
	}
	cfg := file(t, x.h, fake.ConfigPath)
	if !strings.Contains(cfg, "magic_dns: false") || !strings.Contains(cfg, "9.9.9.9") || !strings.Contains(cfg, "# Whether to use MagicDNS") {
		t.Errorf("config edit lost structure:\n%s", cfg)
	}
	if x.m.Badge(nil) != "restart" {
		t.Error("restart badge after a config change")
	}
	// Still pending while the service has not restarted.
	x.send(store.UpdatedMsg{})
	if x.m.Badge(nil) != "restart" {
		t.Error("badge should stay until headscale restarts")
	}
	time.Sleep(2 * time.Millisecond)
	if err := x.h.Service().Restart(t.Context()); err != nil {
		t.Fatal(err)
	}
	x.send(store.UpdatedMsg{})
	if x.m.Badge(nil) != "" {
		t.Error("badge should clear after a restart")
	}
}

func TestBaseDomainMustDifferFromServerURL(t *testing.T) {
	x := newHarness(t)
	x.selectRow(rowBase, "")
	x.invoke("dns.edit", map[string]string{"value": "demo.example.com"})
	if ok, _ := x.enabled("dns.apply"); ok {
		t.Fatal("base domain containing the server_url host must block apply")
	}
	if out := x.view(120, 40); !strings.Contains(out, "base_domain") {
		t.Errorf("issue not shown:\n%s", out)
	}
	if r := x.invoke("dns.discard", nil); r.Err != nil {
		t.Fatal(r.Err)
	}
	if x.m.dirty() {
		t.Error("discard should drop staged edits")
	}
}

func TestSplitDNS(t *testing.T) {
	x := newHarness(t)
	if r := x.invoke("dns.add", map[string]string{"domain": "corp.example.com", "servers": "10.0.0.53, 10.0.0.54"}); r.Err != nil {
		t.Fatal(r.Err)
	}
	x.selectRow(rowSplit, "corp.example.com")
	if r := x.invoke("dns.edit", map[string]string{"domain": "corp.example.org", "servers": "10.0.0.53"}); r.Err != nil {
		t.Fatal(r.Err)
	}
	if c := x.m.config(); len(c.SplitOrder) != 1 || c.SplitOrder[0] != "corp.example.org" {
		t.Fatalf("split after rename: %+v", c.Split)
	}
	x.invoke("dns.add", map[string]string{"domain": "lab.example.com", "servers": "10.1.0.1"})
	x.selectRow(rowSplit, "corp.example.org")
	if r := x.invoke("dns.delete", nil); r.Err != nil {
		t.Fatal(r.Err)
	}
	x.invoke("dns.apply", nil)
	cfg := file(t, x.h, fake.ConfigPath)
	if !strings.Contains(cfg, "lab.example.com:\n        - 10.1.0.1") || strings.Contains(cfg, "corp.example") {
		t.Errorf("split DNS in config:\n%s", cfg)
	}
	x.selectRow(rowRecordsFile, "")
	if ok, _ := x.enabled("dns.edit"); ok {
		t.Error("the records file row is not edited here")
	}
	x.selectRow(rowMagic, "")
	if ok, _ := x.enabled("dns.delete"); ok {
		t.Error("only split domains and records delete")
	}
}

func inlineConfig() []byte {
	return []byte(strings.Replace(string(fake.SeedConfig),
		"  extra_records_path: /var/lib/headscale/extra-records.json\n",
		"  extra_records:\n    - name: grafana.tail.demo.example.com\n      type: A\n      value: 100.64.0.5\n    - name: nas.tail.demo.example.com\n      type: AAAA\n      value: fd7a:115c:a1e0::3\n", 1))
}

// T139: moving inline records creates the file and updates the config in
// one confirmed change set.
func TestMoveInlineRecordsToFile(t *testing.T) {
	x := newHarness(t, fake.WithFile(fake.ConfigPath, inlineConfig()))
	out := x.view(120, 40)
	if !strings.Contains(out, "inline") || !strings.Contains(out, "grafana.tail.demo.example.com") {
		t.Fatalf("inline records view:\n%s", out)
	}
	// Editing inline records stages a config change (restart).
	x.keys("tab")
	x.invoke("dns.add", map[string]string{"name": "pi.tail.demo.example.com", "value": "100.64.0.8"})
	if len(x.m.changes) != 1 || x.m.changes[0].File.Kind != host.FileConfig {
		t.Fatalf("inline add should stage a config change: %+v", x.m.changes)
	}
	x.invoke("dns.discard", nil)

	path := "/var/lib/headscale/extra-records.json"
	x.h.ChangeOnDisk(path, nil) // a file is already there
	if r := x.invoke("dns.move", map[string]string{"path": path}); r.Err == nil || !strings.Contains(r.Err.Error(), "exists") {
		t.Fatalf("an existing target must be refused: %+v", r)
	}

	path = "/var/lib/headscale/records.yaml"
	if spec := x.action("dns.move").Params(x.ctx); spec.Fields[0].Value != "/var/lib/headscale/extra-records.json" {
		t.Errorf("default path should sit next to the database: %q", spec.Fields[0].Value)
	}
	r := x.invoke("dns.move", map[string]string{"path": path})
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	if len(x.m.changes) != 2 || !x.m.changes[0].File.New || x.m.changes[1].File.Kind != host.FileConfig {
		t.Fatalf("one change set with the new file first: %+v", x.m.changes)
	}
	a := x.action("dns.apply")
	if len(a.Targets(x.ctx)) != 2 || !strings.Contains(a.Consequence, "(new)") {
		t.Errorf("confirm should name both files: %+v", a.Targets(x.ctx))
	}
	if out := x.view(120, 40); !strings.Contains(out, "new file") {
		t.Errorf("pending pane should flag the new file:\n%s", out)
	}
	if r := x.invoke("dns.apply", nil); r.Err != nil {
		t.Fatal(r.Err)
	}
	recs := file(t, x.h, path)
	if !strings.Contains(recs, "name: grafana.tail.demo.example.com") || !strings.Contains(recs, "type: AAAA") {
		t.Errorf("moved records:\n%s", recs)
	}
	cfg := file(t, x.h, fake.ConfigPath)
	if strings.Contains(cfg, "  extra_records:\n") || !strings.Contains(cfg, "extra_records_path: "+path) {
		t.Errorf("config after move:\n%s", cfg)
	}
	if x.m.recs == nil || x.m.recF == nil || x.m.recF.New {
		t.Error("after the move the module edits the file")
	}
	// Rolling back removes the new file and restores the inline config.
	if r := x.invoke("dns.rollback", nil); r.Err != nil {
		t.Fatal(r.Err)
	}
	if _, ok := x.h.File(path); ok {
		t.Error("rollback should remove the created records file")
	}
	if got := file(t, x.h, fake.ConfigPath); got != string(inlineConfig()) {
		t.Error("rollback should restore the config")
	}
}

func TestChangedOnDiskRefusesAndKeepsFilesWhole(t *testing.T) {
	x := newHarness(t, fake.WithFile(fake.ConfigPath, inlineConfig()))
	x.invoke("dns.move", map[string]string{"path": "/var/lib/headscale/r.json"})
	x.h.ChangeOnDisk(fake.ConfigPath, append(inlineConfig(), "# edited elsewhere\n"...))
	r := x.invoke("dns.apply", nil)
	if r.Err == nil || !strings.Contains(r.Err.Error(), "changed on disk") || !strings.Contains(r.Err.Error(), "x to reload") {
		t.Fatalf("want a changed-on-disk refusal, got %+v", r)
	}
	if _, ok := x.h.File("/var/lib/headscale/r.json"); ok {
		t.Error("the records file written first must be rolled back")
	}
}

func TestPermissionAndReadOnly(t *testing.T) {
	x := newHarness(t)
	x.ctx.ro = true
	if ok, _ := x.enabled("dns.edit"); ok {
		t.Error("read-only blocks editing")
	}
	if ok, _ := x.enabled("dns.delete"); ok {
		t.Error("read-only blocks staging deletes")
	}
	x.ctx.ro = false
	x.h.FailPermission(host.NeedRecordsW, "")
	x.keys("tab")
	x.invoke("dns.add", map[string]string{"name": "a.tail.demo.example.com", "value": "100.64.0.9"})
	if r := x.invoke("dns.apply", nil); r.Err == nil {
		t.Error("a permission failure should surface")
	}
}

func TestDiffView(t *testing.T) {
	x := newHarness(t)
	if ok, _ := x.enabled("dns.diff"); ok {
		t.Error("no diff when clean")
	}
	x.selectRow(rowSearch, "")
	x.invoke("dns.edit", map[string]string{"value": "corp.example.com"})
	x.invoke("dns.diff", nil)
	out := x.view(100, 30)
	if !strings.Contains(out, "Diff") || !strings.Contains(out, "search_domains") {
		t.Errorf("diff view:\n%s", out)
	}
	x.keys("j", "k")
	if len(x.m.Hints()) == 0 {
		t.Error("diff hints")
	}
	if ok, _ := x.enabled("dns.edit"); ok {
		t.Error("editing is off while the diff is open")
	}
	x.keys("esc")
	if x.m.diff {
		t.Error("esc closes the diff")
	}
}
