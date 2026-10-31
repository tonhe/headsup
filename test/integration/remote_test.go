//go:build integration

package integration

// Quickstart section 2: V1-V12 against real headscale in remote mode (the
// HTTP API with an API key). TUI scenarios drive the real app model; CLI
// scenarios run the built binary with no TTY.

import (
	"context"
	"net/netip"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tonhe/headsup/internal/api"
	"github.com/tonhe/headsup/internal/api/httpapi"
	"github.com/tonhe/headsup/internal/app"
	"github.com/tonhe/headsup/internal/config"
	"github.com/tonhe/headsup/internal/ui/uitest"
)

const tuiWait = 15 * time.Second

// remoteTUI starts the app against the API with the suite's key.
func remoteTUI(t *testing.T, w, h int, o appOpts) *tui {
	t.Helper()
	if o.backend == nil {
		o.backend = S.client(t)
	}
	if o.label == "" {
		o.label = "it"
	}
	d := startTUI(t, S.newApp(t, o), w, h)
	d.Wait(tuiWait, "headscale v", "online") // connected, and the dashboard is up
	return d
}

// shared state between the scenarios, rebuilt on demand so each test also
// runs alone (-run V5).
var (
	sharedMu   sync.Mutex
	sharedKey  string    // reusable tag:server key for alice (V2)
	sharedNode *tsClient // the joined client advertising 10.0.0.0/24 (V3)
	sharedID   uint64
)

func reusableKey(t *testing.T) string {
	t.Helper()
	sharedMu.Lock()
	defer sharedMu.Unlock()
	if sharedKey == "" {
		k, err := S.client(t).CreatePreAuthKey(context.Background(), api.NewPreAuthKey{
			User: "alice", Reusable: true, ACLTags: []string{"tag:server"}, Expiration: time.Now().Add(24 * time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		sharedKey = k.Key.Reveal()
	}
	return sharedKey
}

// joinedNode starts (once) a client with the reusable key that advertises
// 10.0.0.0/24, and waits until headscale has it online.
func joinedNode(t *testing.T) (*tsClient, uint64) {
	t.Helper()
	key := reusableKey(t)
	sharedMu.Lock()
	defer sharedMu.Unlock()
	if sharedNode == nil {
		sharedNode = S.startClient(t, "client-1", key, "--advertise-routes=10.0.0.0/24")
		sharedID = waitNode(t, "client-1", 60*time.Second).ID
	}
	return sharedNode, sharedID
}

// waitNode waits until a node with this hostname is online.
func waitNode(t *testing.T, hostname string, d time.Duration) api.Node {
	t.Helper()
	c := S.client(t)
	var found api.Node
	eventually(t, d, hostname+" online", func() error {
		ns, err := c.ListNodes(context.Background(), api.NodeFilter{})
		if err != nil {
			return err
		}
		for _, n := range ns {
			if n.Name == hostname && n.Online {
				found = n
				return nil
			}
		}
		return errf("not online yet (%d nodes)", len(ns))
	})
	return found
}

// V1: launch with the wrong key -> "Unauthorized" with a hint, and the
// connection editor opens; saving a good key there reconnects.
func TestV01WrongKey(t *testing.T) {
	bad, err := httpapi.New(S.url, api.NewSecret("hskey-api-AAAAAAAAAAAA-"+strings.Repeat("x", 40)))
	if err != nil {
		t.Fatal(err)
	}
	var saved app.ProfileInput
	conn := app.Connector{
		Save: func(p app.ProfileInput) (string, error) { saved = p; return "fixed", nil },
		Open: func(string) (app.Conn, error) {
			c, err := httpapi.New(saved.URL, saved.Key)
			if err != nil {
				return app.Conn{}, err
			}
			return app.Conn{Backend: c, Label: "fixed", ServerURL: saved.URL}, nil
		},
	}
	d := startTUI(t, S.newApp(t, appOpts{backend: bad, label: "it", connector: conn}), 120, 40)
	scr := d.WaitFunc(tuiWait, "unauthorized message and the connection editor", func(s string) bool {
		l := strings.ToLower(s)
		return strings.Contains(l, "unauthorized") && strings.Contains(s, "API key")
	})
	if !strings.Contains(strings.ToLower(scr), "key") {
		t.Errorf("the message should hint at the key:\n%s", scr)
	}
	// The editor has name, URL and key fields: tab to the key, type it, save.
	d.Key("tab", "tab")
	d.Key(S.apiKey)
	d.Key("ctrl+s")
	d.Wait(tuiWait, "fixed", "online")
	if saved.URL != S.url || saved.Key.Reveal() != S.apiKey {
		t.Errorf("saved profile %q / key %q", saved.URL, saved.Key.Masked())
	}
}

var authKeyRE = regexp.MustCompile(`hskey-auth-[A-Za-z0-9_-]+`)

// V2: create a reusable pre-auth key for alice with tag:server in the TUI:
// the key is shown once, with the tailscale up command, and Q shows a QR code.
func TestV02CreatePreAuthKey(t *testing.T) {
	d := remoteTUI(t, 120, 40, appOpts{})
	d.Key("1", "k")
	d.Wait(tuiWait, "Pre-auth keys")
	d.Key("n")
	d.Wait(tuiWait, "New pre-auth key", "Reusable")
	d.Key("tab", "space", "tab", "tab", "tab") // user (alice) -> reusable on -> ephemeral -> expiry -> tags
	d.Type("tag:server")
	d.Key("ctrl+s")
	scr := d.Wait(tuiWait, "only time the full key is shown", "tailscale up --login-server="+S.url)
	key := ""
	for _, l := range strings.Split(scr, "\n") {
		if strings.Contains(l, "Key for alice") {
			continue
		}
		if m := authKeyRE.FindString(l); m != "" && !strings.Contains(l, "tailscale up") && len(m) > len(key) {
			key = m
		}
	}
	if key == "" {
		t.Fatalf("no key on screen:\n%s", scr)
	}
	ks, err := S.client(t).ListPreAuthKeys(context.Background(), api.KeyFilter{User: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	var made *api.PreAuthKey
	for i := range ks {
		if strings.HasPrefix(key, strings.TrimRight(ks[i].Key.Masked(), "*")) || ks[i].Key.Reveal() == key {
			made = &ks[i]
		}
	}
	if made == nil {
		// Listed keys are masked by the server; match on the newest instead.
		slices.SortFunc(ks, func(a, b api.PreAuthKey) int { return b.CreatedAt.Compare(a.CreatedAt) })
		made = &ks[0]
	}
	if !made.Reusable || !slices.Contains(made.ACLTags, "tag:server") {
		t.Errorf("created key: %+v", made)
	}
	d.Key("Q")
	d.WaitFunc(tuiWait, "a QR code", func(s string) bool { return strings.Count(s, "█") > 50 || strings.Count(s, "▀") > 50 })
	sharedMu.Lock()
	sharedKey = key
	sharedMu.Unlock()
}

// V3: join a client with that key; 1 m shows it within 5s, tagged and online.
func TestV03JoinShowsMachine(t *testing.T) {
	_, id := joinedNode(t)
	d := remoteTUI(t, 120, 40, appOpts{})
	start := time.Now()
	d.Key("1", "m")
	scr := d.Wait(5*time.Second, "client-1", "tag:server", "online")
	t.Logf("machine on screen %v after the server had it", time.Since(start).Round(time.Millisecond))
	_ = scr
	n, err := S.client(t).GetNode(context.Background(), id)
	if err != nil || !n.Online || !slices.Contains(n.Tags, "tag:server") {
		t.Fatalf("server view: %+v %v", n, err)
	}
}

// V4: rename the machine to nas-01; the table updates and 3 v shows the
// activity entry.
func TestV04RenameAndActivity(t *testing.T) {
	_, id := joinedNode(t)
	// Rename back afterwards so later scenarios see the original name.
	t.Cleanup(func() { _, _ = S.client(t).RenameNode(context.Background(), id, "client-1") })
	d := remoteTUI(t, 120, 40, appOpts{})
	d.Key("1", "m")
	d.Wait(tuiWait, "client-1")
	d.Key("r")
	d.Wait(tuiWait, "New name")
	d.Key("ctrl+u")
	d.Type("nas-01")
	d.Key("enter")
	d.WaitFunc(tuiWait, "the table to show nas-01", func(s string) bool {
		return strings.Contains(s, "nas-01") && !strings.Contains(s, "New name")
	})
	eventually(t, tuiWait, "renamed on the server", func() error {
		n, err := S.client(t).GetNode(context.Background(), id)
		if err != nil {
			return err
		}
		if n.GivenName != "nas-01" {
			return errf("given name %q", n.GivenName)
		}
		return nil
	})
	d.Key("3", "v")
	d.WaitFunc(tuiWait, "the rename in the activity log", func(s string) bool {
		return strings.Contains(s, "nas-01") && strings.Contains(strings.ToLower(s), "rename")
	})
}

// V5: the advertised 10.0.0.0/24 shows as pending; a approves it after a
// confirm.
func TestV05ApproveRoute(t *testing.T) {
	_, id := joinedNode(t)
	pfx := netip.MustParsePrefix("10.0.0.0/24")
	c := S.client(t)
	eventually(t, 30*time.Second, "route advertised", func() error {
		n, err := c.GetNode(context.Background(), id)
		if err != nil {
			return err
		}
		if !slices.Contains(n.AvailableRoutes, pfx) {
			return errf("available %v", n.AvailableRoutes)
		}
		return nil
	})
	// Start from pending.
	if _, err := c.SetApprovedRoutes(context.Background(), id, nil); err != nil {
		t.Fatal(err)
	}
	d := remoteTUI(t, 120, 40, appOpts{})
	d.Key("1", "r")
	d.Wait(tuiWait, "10.0.0.0/24", "pending")
	d.Key("a")
	d.WaitFunc(tuiWait, "a confirm", func(s string) bool { return strings.Contains(s, "Approve") || strings.Contains(s, "approve") })
	d.Key("y")
	eventually(t, tuiWait, "route approved on the server", func() error {
		n, err := c.GetNode(context.Background(), id)
		if err != nil {
			return err
		}
		if !slices.Contains(n.ApprovedRoutes, pfx) {
			return errf("approved %v", n.ApprovedRoutes)
		}
		return nil
	})
	d.Wait(tuiWait, "served")
}

// V6, V7: a broken rule is refused with line and column and the buffer is
// kept; the fixed rule's diff shows only the new rule, and ctrl+s applies it
// after a confirm.
func TestV06V07PolicyEdit(t *testing.T) {
	c := S.client(t)
	ctx := context.Background()
	if _, err := c.SetPolicy(ctx, basePolicy); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = c.SetPolicy(context.Background(), basePolicy) })
	d := remoteTUI(t, 120, 40, appOpts{})
	d.Key("1", "a")
	d.Wait(tuiWait, "tag:server")
	d.Key("e")
	d.WaitFunc(tuiWait, "the editor", func(s string) bool { return strings.Contains(s, "ctrl+s") })
	// Add a broken rule as a new first line inside the object.
	d.Key("ctrl+home", "end", "enter")
	d.Type(`"ssh": [oops],`)
	d.Key("ctrl+s")
	scr := d.WaitFunc(tuiWait, "a validation error with a position", func(s string) bool {
		return strings.Contains(s, "line") && strings.Contains(s, "column") && strings.Contains(s, "nothing was applied")
	})
	if p, _ := c.GetPolicy(ctx); p.Raw != basePolicy {
		t.Fatalf("nothing may be applied; policy is now:\n%s", p.Raw)
	}
	_ = scr
	// V7: reopen (the buffer is kept, cursor on the error line), fix it.
	d.Key("e")
	d.Wait(tuiWait, "oops")
	d.Key("home", "ctrl+k")
	rule := `"ssh": [{"action": "accept", "src": ["autogroup:member"], "dst": ["autogroup:self"], "users": ["autogroup:nonroot"]}],`
	d.Type(rule)
	d.Key("ctrl+s")
	scr = d.Wait(tuiWait, "Validated by the server")
	plus := 0
	for _, l := range strings.Split(scr, "\n") {
		if strings.Contains(l, "+") && strings.Contains(l, `"ssh"`) {
			plus++
		}
	}
	if plus != 1 || strings.Contains(scr, "-  \"acls\"") {
		t.Errorf("the diff should show only the new rule:\n%s", scr)
	}
	d.Key("ctrl+s")
	d.WaitFunc(tuiWait, "the apply confirm", func(s string) bool { return strings.Contains(s, "Apply") })
	d.Key("y")
	eventually(t, tuiWait, "policy applied", func() error {
		p, err := c.GetPolicy(ctx)
		if err != nil {
			return err
		}
		if !strings.Contains(p.Raw, "autogroup:nonroot") {
			return errf("policy not updated")
		}
		return nil
	})
}

// V8: T then scroll through themes repaints live; the choice persists.
func TestV08Themes(t *testing.T) {
	d := remoteTUI(t, 120, 40, appOpts{})
	before := d.View()
	d.Key("T")
	d.WaitFunc(tuiWait, "the theme picker", func(s string) bool { return strings.Contains(strings.ToLower(s), "theme") })
	open := d.View()
	d.Key("down")
	d.WaitFunc(tuiWait, "a live repaint", func(string) bool { return d.View() != open })
	d.Key("down")
	d.Key("enter")
	time.Sleep(200 * time.Millisecond)
	if d.View() == before {
		t.Error("picking a theme should repaint")
	}
	cfg, err := config.Load(S.cfgFile)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UI.Theme == "" {
		t.Fatalf("the theme was not saved to %s", S.cfgFile)
	}
	// A restart starts from the saved config and paints the same colours.
	picked := headerColours(d.View())
	d2 := remoteTUI(t, 120, 40, appOpts{cfg: &cfg})
	if got := headerColours(d2.View()); got != picked {
		t.Errorf("after a restart the header should keep theme %s:\n%q\n%q", cfg.UI.Theme, got, picked)
	}
	if headerColours(before) == picked {
		t.Error("the picked theme should paint differently from the default")
	}
	t.Logf("theme saved: %s", cfg.UI.Theme)
	// Put the default back for the other scenarios.
	cfg.UI.Theme = ""
	if err := config.Save(S.cfgFile, cfg); err != nil {
		t.Fatal(err)
	}
}

// V9: : then "expire" lists every expire action.
func TestV09PaletteExpire(t *testing.T) {
	joinedNode(t)
	d := remoteTUI(t, 120, 40, appOpts{})
	d.Key(":")
	d.Type("expire")
	scr := d.Wait(tuiWait, "Expire machine", "Expire key")
	t.Logf("palette:\n%s", paletteLines(scr))
}

var onlineRE = regexp.MustCompile(`(\d+) / (\d+) online`)

// V10: headsup nodes ls -o json | jq length matches the TUI's count.
func TestV10CLICountMatchesTUI(t *testing.T) {
	joinedNode(t)
	r := ok(t, S.remoteCLI(t, "nodes", "ls", "-o", "json"))
	nodes := decode[[]map[string]any](t, r)
	d := remoteTUI(t, 120, 40, appOpts{})
	d.WaitFunc(tuiWait, "the dashboard to count "+strconv.Itoa(len(nodes))+" machines", func(s string) bool {
		m := onlineRE.FindStringSubmatch(s)
		return m != nil && m[2] == strconv.Itoa(len(nodes))
	})
}

// paletteLines is the palette's entries, for the log.
func paletteLines(screen string) string {
	var out []string
	for _, l := range strings.Split(screen, "\n") {
		if i := strings.Index(l, "Expire"); i >= 0 || strings.Contains(l, "expiry") {
			out = append(out, strings.TrimSpace(strings.Trim(strings.TrimSpace(l), "│")))
		}
	}
	return strings.Join(out, "\n")
}

// V11: --read-only, then D on a machine is blocked with a read-only toast.
func TestV11ReadOnly(t *testing.T) {
	_, id := joinedNode(t)
	d := remoteTUI(t, 120, 40, appOpts{readOnly: true})
	d.Key("1", "m")
	d.Wait(tuiWait, "client-1")
	d.Key("D")
	d.WaitFunc(tuiWait, "a read-only toast", func(s string) bool { return strings.Contains(strings.ToLower(s), "read-only") })
	if _, err := S.client(t).GetNode(context.Background(), id); err != nil {
		t.Fatalf("the machine must still exist: %v", err)
	}
	r := exitCode(t, S.remoteCLI(t, "--read-only", "nodes", "delete", strconv.FormatUint(id, 10), "--yes"), 3)
	if !strings.Contains(strings.ToLower(r.stderr), "read-only") {
		t.Errorf("CLI should say why:\n%s", r)
	}
}

// V12: at 80x24 the sidebar becomes tabs, low-priority columns drop, and
// nothing wraps.
func TestV12Small(t *testing.T) {
	joinedNode(t)
	d := remoteTUI(t, 80, 24, appOpts{})
	d.Key("1", "m")
	scr := d.Wait(tuiWait, "client-1")
	lines := uitest.Lines(strings.TrimRight(scr, "\n"))
	if len(lines) > 24 {
		t.Errorf("%d lines at 80x24", len(lines))
	}
	if w := uitest.MaxWidth(scr); w > 80 {
		t.Errorf("a line is %d cells wide at 80x24:\n%s", w, scr)
	}
	if strings.Contains(scr, "1 TAILNET") {
		t.Errorf("the sidebar should become tabs:\n%s", scr)
	}
	wide := remoteTUI(t, 160, 40, appOpts{})
	wide.Key("1", "m")
	ws := wide.Wait(tuiWait, "client-1")
	if header(ws) == header(scr) {
		t.Errorf("columns should drop at 80 wide:\n%s\n---\n%s", ws, scr)
	}
	if os.Getenv("HEADSUP_SHOW") != "" {
		t.Logf("80x24:\n%s", scr)
	}
}

var sgrRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// headerColours is the set of colours the header line uses: the theme, not the
// text (which changes as data loads).
func headerColours(s string) string {
	l, _, _ := strings.Cut(s, "\n")
	seen := map[string]bool{}
	for _, c := range sgrRE.FindAllString(l, -1) {
		seen[c] = true
	}
	out := make([]string, 0, len(seen))
	for c := range seen {
		out = append(out, strconv.Quote(c))
	}
	slices.Sort(out)
	return strings.Join(out, " ")
}

func header(screen string) string {
	for _, l := range strings.Split(screen, "\n") {
		if strings.Contains(l, "NAME") {
			return strings.Join(strings.Fields(l), " ")
		}
	}
	return ""
}
