package derp

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/tonhe/headsup/internal/api"
	apifake "github.com/tonhe/headsup/internal/api/fake"
	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/host/derpmap"
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
	ex, _ := h.NodeExtras(context.Background(), h.Nodes())
	x.ctx.st.Extras = ex
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
		msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		switch k {
		case "tab":
			msg = tea.KeyMsg{Type: tea.KeyTab}
		case "shift+tab":
			msg = tea.KeyMsg{Type: tea.KeyShiftTab}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
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
	return x.results[len(x.results)-1]
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

// cursorTo puts the Map files cursor on the given region/node.
func (x *harness) cursorTo(region, node int) {
	x.t.Helper()
	x.m.sec = SecMaps
	for i, r := range x.m.mrows {
		if r.region == region && r.node == node {
			x.m.cursor[SecMaps] = i
			return
		}
	}
	x.t.Fatalf("no map row %d/%d in %+v", region, node, x.m.mrows)
}

func file(t *testing.T, h *fake.Host, path string) string {
	t.Helper()
	b, ok := h.File(path)
	if !ok {
		t.Fatalf("no file %s", path)
	}
	return string(b)
}

const labYAML = `# custom relays
regions:
  900:
    regionid: 900
    regioncode: lab
    regionname: Lab
    nodes:
      - name: 900a
        regionid: 900
        hostname: derp.lab.example.com
`

const labPath = "/etc/headscale/derp.yaml"

func withLabFile() []fake.Option {
	cfg := strings.Replace(string(fake.SeedConfig), "  paths: []\n", "  paths:\n    - "+labPath+"\n", 1)
	return []fake.Option{fake.WithFile(fake.ConfigPath, []byte(cfg)), fake.WithFile(labPath, []byte(labYAML))}
}

func TestSectionsRender(t *testing.T) {
	x := newHarness(t, withLabFile()...)
	want := map[Section][]string{
		SecServer:  {"Embedded DERP server", "Region ID", "999", "needs restart"},
		SecSources: {"DERP map sources", "controlplane.tailscale.com", labPath},
		SecMaps:    {"DERP map files", labPath, "(yaml)", "900", "lab", "derp.lab.example.com"},
		SecLive:    {"Live DERP map", "nyc", "fra", "PREFER", "ms"},
	}
	for sec := SecServer; sec <= SecLive; sec++ {
		x.m.sec = sec
		for _, sz := range [][2]int{{80, 24}, {120, 40}} {
			out := x.view(sz[0], sz[1])
			for _, w := range want[sec] {
				if !strings.Contains(out, w) {
					t.Errorf("section %d %dx%d missing %q:\n%s", sec, sz[0], sz[1], w, out)
				}
			}
		}
		if len(x.m.Hints()) == 0 {
			t.Error("hints")
		}
	}
	// Live map: the lab region comes from the file, with its source named.
	x.m.sec = SecLive
	if out := x.view(120, 40); !strings.Contains(out, "derp.yaml") {
		t.Errorf("live map should attribute region 900 to its file:\n%s", out)
	}
	x.keys("tab")
	if x.m.sec != SecServer {
		t.Error("tab wraps around")
	}
	x.keys("shift+tab")
	if x.m.sec != SecLive {
		t.Error("shift+tab goes back")
	}
	if x.m.ID() != "derp" || x.m.Chord() != "D" || x.m.Modes() != module.ModeHost || x.m.Icon(icons.ASCII) == "" || x.m.Order() == 0 || x.m.Title() == "" || x.m.Capturing() {
		t.Error("metadata")
	}
}

func TestRemoteModeExplains(t *testing.T) {
	m := New()
	ctx := &tctx{st: store.New(), s: uitest.StyleFor("")}
	if m.Init(ctx) != nil {
		t.Error("nothing to load without a host")
	}
	if out := ansi.Strip(m.View(ctx, module.Rect{W: 80, H: 20})); !strings.Contains(out, "host mode") {
		t.Errorf("remote view:\n%s", out)
	}
}

// US15 independent test: add a custom region to a DERP map file; it
// validates and diffs, and the live map shows it after the restart the app
// says is needed.
func TestNewMapFileShowsInLiveMapAfterRestart(t *testing.T) {
	x := newHarness(t)
	x.m.sec = SecMaps
	if out := x.view(100, 30); !strings.Contains(out, "Press N") {
		t.Errorf("empty map files should point at N:\n%s", out)
	}
	path := "/etc/headscale/derp-lab.json"
	r := x.invoke("derp.newfile", map[string]string{"path": path, "id": "901", "code": "lab", "name": "Home lab",
		"node": "901a", "hostname": "derp.home.example.com", "ipv4": "198.51.100.9", "stunport": "3478"})
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	if len(x.m.changes) != 2 || !x.m.changes[0].File.New || x.m.changes[1].File.Kind != host.FileConfig {
		t.Fatalf("one change set, new file first: %+v", x.m.changes)
	}
	if out := x.view(120, 40); !strings.Contains(out, "Pending changes") || !strings.Contains(out, "new file") || !strings.Contains(out, "derp-lab.json") {
		t.Errorf("pending pane:\n%s", out)
	}
	a := x.action("derp.apply")
	if len(a.Targets(x.ctx)) != 2 || !strings.Contains(a.Consequence, "restarts") || a.Danger < module.DangerConfirm {
		t.Errorf("apply: %+v", a)
	}
	if r := x.invoke("derp.apply", nil); r.Err != nil || !strings.Contains(r.Message, "Restart") {
		t.Fatalf("apply: %+v", r)
	}
	if got := file(t, x.h, path); !strings.Contains(got, `"RegionCode": "lab"`) || !strings.Contains(got, `"HostName": "derp.home.example.com"`) {
		t.Errorf("new map file:\n%s", got)
	}
	if !strings.Contains(file(t, x.h, fake.ConfigPath), "paths: ["+path+"]") {
		t.Error("derp.paths should list the new file")
	}
	if x.m.Badge(nil) != "restart" {
		t.Error("DERP changes need a restart")
	}
	time.Sleep(2 * time.Millisecond)
	if err := x.h.Service().Restart(context.Background()); err != nil {
		t.Fatal(err)
	}
	x.send(store.UpdatedMsg{})
	if x.m.Badge(nil) != "" {
		t.Error("badge clears after a restart")
	}
	if _, ok := x.m.live.Region(901); !ok {
		t.Errorf("live map should show region 901 after the restart: %+v", x.m.live)
	}
	// And roll back: the file goes away, the config is restored.
	if r := x.invoke("derp.rollback", nil); r.Err != nil {
		t.Fatal(r.Err)
	}
	if _, ok := x.h.File(path); ok {
		t.Error("rollback removes the new file")
	}
}

func TestRegionAndNodeEditor(t *testing.T) {
	x := newHarness(t, withLabFile()...)
	x.cursorTo(0, -1) // the file header
	if ok, _ := x.enabled("derp.edit"); ok {
		t.Error("the file row is not editable")
	}
	if spec := x.action("derp.add").Params(x.ctx); spec.Fields[0].Value != "901" {
		t.Errorf("next free region ID should be suggested: %q", spec.Fields[0].Value)
	}
	if r := x.invoke("derp.add", map[string]string{"code": "LAB", "hostname": "d.example.com"}); r.Err == nil {
		t.Error("a duplicate region code must be refused")
	}
	if r := x.invoke("derp.add", map[string]string{"code": "home", "name": "Home", "hostname": "derp.home.example.com", "derpport": "8443"}); r.Err != nil {
		t.Fatal(r.Err)
	}
	// Add a node to region 900, edit it, then delete it.
	x.cursorTo(900, -1)
	if r := x.invoke("derp.add", map[string]string{"hostname": "derp2.lab.example.com", "stunport": "-1"}); r.Err != nil {
		t.Fatal(r.Err)
	}
	if r := x.invoke("derp.add", map[string]string{"hostname": "x", "ipv4": "fd7a::1"}); r.Err == nil {
		t.Error("an IPv6 address in the IPv4 field must be refused")
	}
	x.cursorTo(900, 1)
	if r := x.invoke("derp.edit", map[string]string{"ipv6": "2001:db8::9"}); r.Err != nil {
		t.Fatal(r.Err)
	}
	x.cursorTo(900, -1)
	if r := x.invoke("derp.edit", map[string]string{"name": "Basement lab"}); r.Err != nil {
		t.Fatal(r.Err)
	}
	if ok, why := x.enabled("derp.apply"); !ok {
		t.Fatalf("apply: %s %+v", why, x.m.checks)
	}
	x.invoke("derp.apply", nil)
	got := file(t, x.h, labPath)
	for _, w := range []string{"# custom relays", "regionname: Basement lab", "hostname: derp2.lab.example.com", "stunport: -1", "ipv6: 2001:db8::9", "901:", "derpport: 8443"} {
		if !strings.Contains(got, w) {
			t.Errorf("map file missing %q:\n%s", w, got)
		}
	}
	f, err := derpmap.ParseFile(labPath, []byte(got))
	if err != nil || len(f.Map.Regions) != 2 {
		t.Fatalf("written map: %v", err)
	}
	// Delete a node, refuse deleting the last one, delete a region.
	x.cursorTo(900, 1)
	if r := x.invoke("derp.delete", nil); r.Err != nil {
		t.Fatal(r.Err)
	}
	x.cursorTo(900, 0)
	if ok, _ := x.enabled("derp.delete"); ok {
		t.Error("a region's last node cannot be deleted")
	}
	x.cursorTo(901, -1)
	if r := x.invoke("derp.delete", nil); r.Err != nil {
		t.Fatal(r.Err)
	}
	x.invoke("derp.apply", nil)
	f, _ = derpmap.ParseFile(labPath, []byte(file(t, x.h, labPath)))
	if len(f.Map.Regions) != 1 || len(f.Map.Regions[0].Nodes) != 1 {
		t.Errorf("after deletes: %+v", f.Map)
	}
}

func TestEmbeddedServerForm(t *testing.T) {
	x := newHarness(t, withLabFile()...)
	x.m.sec = SecServer
	spec := x.action("derp.edit").Params(x.ctx)
	if len(spec.Fields) != len(serverFields) || spec.Fields[0].Kind != module.FieldToggle {
		t.Fatalf("form: %+v", spec.Fields)
	}
	if r := x.invoke("derp.edit", map[string]string{"derp.server.enabled": "true", "derp.server.region_id": "900", "derp.server.ipv6": ""}); r.Err != nil {
		t.Fatal(r.Err)
	}
	warned := false
	for _, i := range x.m.checks {
		if i.Severity == host.SevWarn && strings.Contains(i.Message, "embedded") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("region 900 collides with the embedded server: %+v", x.m.checks)
	}
	x.invoke("derp.apply", nil)
	cfg := file(t, x.h, fake.ConfigPath)
	if !strings.Contains(cfg, "    enabled: true") || !strings.Contains(cfg, "region_id: 900") || strings.Contains(cfg, "ipv6: 2001:db8::1") {
		t.Errorf("config:\n%s", cfg)
	}
	if !strings.Contains(cfg, "# Region ID to use for the embedded DERP server.") {
		t.Error("comments must survive")
	}
	x.m.sec = SecLive
	x.invoke("derp.refresh", nil)
	if ok, _ := x.enabled("derp.edit"); ok {
		t.Error("live map is read-only")
	}
}

func TestSources(t *testing.T) {
	x := newHarness(t)
	x.m.sec = SecSources
	if r := x.invoke("derp.add", map[string]string{"kind": "URL", "value": "ftp://nope"}); r.Err == nil {
		t.Error("non-http URL refused")
	}
	if r := x.invoke("derp.add", map[string]string{"kind": "URL", "value": "https://controlplane.tailscale.com/derpmap/default"}); r.Err == nil {
		t.Error("duplicate refused")
	}
	if r := x.invoke("derp.add", map[string]string{"kind": "file", "value": "/etc/headscale/x.yaml"}); r.Err != nil {
		t.Fatal(r.Err)
	}
	x.m.cursor[SecSources] = 0
	if r := x.invoke("derp.edit", map[string]string{"value": "https://derp.example.com/map.json"}); r.Err != nil {
		t.Fatal(r.Err)
	}
	if r := x.invoke("derp.delete", nil); r.Err != nil {
		t.Fatal(r.Err)
	}
	// Removing the only URL leaves the file path; still a valid source set.
	if errorCount(x.m.checks) != 0 {
		t.Errorf("checks: %+v", x.m.checks)
	}
	x.m.cursor[SecSources] = 0
	x.invoke("derp.delete", nil)
	if errorCount(x.m.checks) == 0 {
		t.Error("no DERP source at all is an error")
	}
	if ok, _ := x.enabled("derp.apply"); ok {
		t.Error("apply blocked by errors")
	}
	x.invoke("derp.diff", nil)
	if out := x.view(100, 30); !strings.Contains(out, "Diff") {
		t.Errorf("diff view:\n%s", out)
	}
	x.keys("j", "esc")
	if r := x.invoke("derp.discard", nil); r.Err != nil || x.m.dirty() {
		t.Fatal("discard")
	}
}

func TestReadOnlyAndUnreadableFile(t *testing.T) {
	cfg := strings.Replace(string(fake.SeedConfig), "  paths: []\n", "  paths:\n    - /etc/headscale/missing.yaml\n", 1)
	x := newHarness(t, fake.WithFile(fake.ConfigPath, []byte(cfg)))
	x.m.sec = SecMaps
	if out := x.view(100, 30); !strings.Contains(out, "missing.yaml") {
		t.Errorf("unreadable file should be listed:\n%s", out)
	}
	if ok, _ := x.enabled("derp.add"); ok {
		t.Error("cannot add to an unreadable file")
	}
	x.ctx.ro = true
	x.m.sec = SecServer
	if ok, _ := x.enabled("derp.edit"); ok {
		t.Error("read-only")
	}
}

func TestLiveMapUnavailable(t *testing.T) {
	x := newHarness(t)
	if err := x.h.Service().Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	x.invoke("derp.refresh", nil)
	x.m.sec = SecLive
	if out := x.view(100, 20); !strings.Contains(out, "debug endpoint") {
		t.Errorf("live map without the server:\n%s", out)
	}
}
