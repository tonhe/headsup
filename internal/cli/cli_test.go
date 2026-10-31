package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/tonhe/headsup/internal/activity"
	"github.com/tonhe/headsup/internal/api"
	"github.com/tonhe/headsup/internal/api/fake"
	"github.com/tonhe/headsup/internal/config"
	"github.com/tonhe/headsup/internal/host"
	hostfake "github.com/tonhe/headsup/internal/host/fake"
	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/ui/uitest"
)

// fixed is the test clock: every relative time in output derives from it.
var fixed = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

func clock() time.Time { return fixed }

type memLog struct {
	mu sync.Mutex
	es []activity.Entry
}

func (l *memLog) Log(e activity.Entry) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.es = append(l.es, e)
	return nil
}

func (l *memLog) Recent(int) ([]activity.Entry, error) { return l.es, nil }

func (l *memLog) ids() []string {
	var out []string
	for _, e := range l.es {
		out = append(out, e.ActionID+":"+e.Result.Status)
	}
	return out
}

// harness runs verbs against the fake backend and, optionally, the fake host.
type harness struct {
	t           *testing.T
	b           *fake.Backend
	h           *hostfake.Host
	noHost      bool
	readOnly    bool
	interactive bool
	stdin       string
	log         *memLog
	cfg         config.Config
	cfgPath     string
	histDir     string
	connects    int
	out, err    bytes.Buffer
}

func newHarness(t *testing.T, opts ...fake.Option) *harness {
	t.Helper()
	t.Setenv("TMPDIR", t.TempDir()) // demo backups land under os.TempDir
	opts = append([]fake.Option{fake.WithClock(clock)}, opts...)
	b := fake.Demo(opts...)
	nodes, _ := b.ListNodes(context.Background(), api.NodeFilter{})
	ids := make([]uint64, len(nodes))
	for i, n := range nodes {
		ids[i] = n.ID
	}
	dir := t.TempDir()
	return &harness{
		t: t, b: b, h: hostfake.New(hostfake.WithClock(clock), hostfake.WithNodes(ids)),
		log: &memLog{}, cfg: config.Default(), cfgPath: filepath.Join(dir, "config.toml"),
		histDir: filepath.Join(dir, "policy"),
	}
}

func (x *harness) remote() *harness { x.noHost = true; return x }

func (x *harness) options() Options {
	return Options{
		Stdin: strings.NewReader(x.stdin), Stdout: &x.out, Stderr: &x.err, Interactive: x.interactive,
		Now: clock, Config: x.cfg, ConfigPath: x.cfgPath, Activity: x.log, PolicyHistoryDir: x.histDir,
		ThemesDir: filepath.Join(filepath.Dir(x.cfgPath), "themes"),
		Connect: func(f config.Flags) (Conn, error) {
			x.connects++
			c := Conn{Backend: x.b, Label: "test", ServerURL: "https://hs.example.com", ReadOnly: x.readOnly}
			if !x.noHost {
				c.Host = x.h
				c.Label = "host:test"
			}
			return c, nil
		},
		DetectEnv: func(config.Flags) host.Env { return x.h.Env() },
	}
}

// run executes a verb with fresh output buffers and returns the exit code.
func (x *harness) run(args ...string) int {
	x.t.Helper()
	x.out.Reset()
	x.err.Reset()
	return Run(args, x.options())
}

func (x *harness) ok(args ...string) string {
	x.t.Helper()
	if code := x.run(args...); code != 0 {
		x.t.Fatalf("%v: exit %d\nstdout: %s\nstderr: %s", args, code, x.out.String(), x.err.String())
	}
	return x.out.String()
}

func (x *harness) code(want int, args ...string) {
	x.t.Helper()
	if got := x.run(args...); got != want {
		x.t.Fatalf("%v: exit %d, want %d\nstdout: %s\nstderr: %s", args, got, want, x.out.String(), x.err.String())
	}
}

// --- T144: golden output -----------------------------------------------------

func TestGoldenOutput(t *testing.T) {
	cases := map[string][]string{
		"nodes_table":    {"nodes", "ls", "--user", "bob"},
		"nodes_wide":     {"nodes", "ls", "--user", "bob", "-o", "wide"},
		"nodes_json":     {"nodes", "ls", "--filter", "tag:db", "-o", "json"},
		"nodes_yaml":     {"nodes", "ls", "--filter", "tag:db", "-o", "yaml"},
		"node_show":      {"nodes", "show", "laptop-alice"},
		"users_table":    {"users", "ls"},
		"users_json":     {"users", "ls", "-o", "json"},
		"preauth_table":  {"keys", "preauth", "ls"},
		"preauth_yaml":   {"keys", "preauth", "ls", "--user", "ops", "-o", "yaml"},
		"apikeys_wide":   {"keys", "api", "ls", "-o", "wide"},
		"routes_table":   {"routes", "ls"},
		"routes_pending": {"routes", "ls", "--pending", "-o", "json"},
		"server_caps":    {"server", "caps"},
		"server_info":    {"server", "info", "-o", "yaml"},
		"host_perms":     {"host", "perms"},
		"svc_status":     {"svc", "status", "-o", "json"},
		"dns_show":       {"dns", "show"},
		"dns_records":    {"dns", "records", "ls", "-o", "json"},
		"derp_show":      {"derp", "show", "-o", "yaml"},
		"conf_get":       {"conf", "get", "dns.base_domain", "-o", "json"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			x := newHarness(t)
			uitest.Golden(t, x.ok(args...))
		})
	}
}

// The spec's independent test: list JSON parses and matches the server's data.
func TestJSONMatchesBackend(t *testing.T) {
	x := newHarness(t)
	var got []map[string]any
	if err := json.Unmarshal([]byte(x.ok("nodes", "ls", "-o", "json")), &got); err != nil {
		t.Fatal(err)
	}
	want, _ := x.b.ListNodes(context.Background(), api.NodeFilter{})
	if len(got) != len(want) {
		t.Fatalf("%d nodes in json, backend has %d", len(got), len(want))
	}
	byID := map[float64]map[string]any{}
	for _, n := range got {
		byID[n["id"].(float64)] = n
	}
	for _, n := range want {
		j, ok := byID[float64(n.ID)]
		if !ok || j["name"] != n.DisplayName() || j["user"].(map[string]any)["name"] != n.User.Name {
			t.Errorf("node %d: %v", n.ID, j)
		}
	}
	// yaml carries the same data.
	var ys []map[string]any
	if err := yaml.Unmarshal([]byte(x.ok("nodes", "ls", "-o", "yaml")), &ys); err != nil || len(ys) != len(want) {
		t.Fatalf("yaml: %v, %d", err, len(ys))
	}
	// Filters are the TUI's syntax.
	if err := json.Unmarshal([]byte(x.ok("nodes", "ls", "--filter", "is:expired", "-o", "json")), &got); err != nil {
		t.Fatal(err)
	}
	for _, n := range got {
		if n["status"] != "expired" {
			t.Errorf("is:expired returned %v", n["name"])
		}
	}
}

// --- T144: exit codes --------------------------------------------------------

func TestExitCodes(t *testing.T) {
	x := newHarness(t)
	x.code(ExitUsage)                                      // no verb
	x.code(ExitUsage, "frobnicate")                        // unknown verb
	x.code(ExitUsage, "nodes")                             // missing sub-verb
	x.code(ExitUsage, "nodes", "bogus")                    // unknown sub-verb
	x.code(ExitUsage, "nodes", "show")                     // missing argument
	x.code(ExitUsage, "nodes", "ls", "--nope")             // unknown flag
	x.code(ExitUsage, "nodes", "ls", "-o", "xml")          // unknown format
	x.code(ExitUsage, "policy", "get", "-o", "wide")       // format not offered
	x.code(ExitUsage, "nodes", "tags", "1")                // neither --set nor --add
	x.code(ExitNotFound, "nodes", "show", "9999")          // no such node
	x.code(ExitNotFound, "users", "rename", "nobody", "x") // no such user
	x.code(ExitNotFound, "keys", "preauth", "expire", "999", "--yes")
	x.code(ExitNotFound, "routes", "approve", "1", "10.9.9.0/24", "--yes")
	x.code(ExitInvalid, "routes", "approve", "1", "not-a-prefix", "--yes")
	x.code(ExitInvalid, "users", "create", "x!")             // fails the form's validator
	x.code(ExitInvalid, "nodes", "rename", "1", "Bad Name!") // likewise
	x.code(ExitInvalid, "users", "delete", "ops", "--yes")   // still owns machines
	x.code(ExitAborted, "nodes", "delete", "1")              // destructive, no TTY, no --yes
	x.code(ExitAborted, "nodes", "expire", "1")
	x.code(ExitUnsupported, "conf", "edit")
	x.code(ExitOK, "help")
	if !strings.Contains(x.out.String(), "Exit codes") {
		t.Errorf("help: %q", x.out.String())
	}

	// An ambiguous name lists the candidates and exits 2.
	x.ok("nodes", "rename", "1", "lap-one")
	x.ok("nodes", "rename", "2", "laptop-alice") // node 1's hostname is still laptop-alice
	x.code(ExitUsage, "nodes", "show", "laptop-alice")
	if !strings.Contains(x.err.String(), "matches 2 machines") {
		t.Errorf("ambiguous: %q", x.err.String())
	}

	// Server errors map by kind.
	x.b.FailNext("ListNodes", api.KindUnauthorized)
	x.code(ExitUnauthorized, "nodes", "ls")
	x.b.FailNext("ListNodes", api.KindServer)
	x.code(ExitError, "nodes", "ls")
}

func TestUnsupportedCapability(t *testing.T) {
	x := newHarness(t, fake.WithCaps(api.NewCapabilities(api.CapPolicyWritable)))
	x.code(ExitUnsupported, "server", "backfill-ips", "--yes")
	x.code(ExitUnsupported, "nodes", "expire", "1", "--disable", "--yes")
	x.code(ExitUnsupported, "auth", "approve", "pXq7Rz2Lm9Kd")
}

func TestReadOnlyRefusesChanges(t *testing.T) {
	x := newHarness(t)
	x.readOnly = true
	x.code(ExitUnauthorized, "nodes", "expire", "1", "--yes")
	x.code(ExitUnauthorized, "users", "create", "dave")
	x.code(ExitUnauthorized, "conf", "set", "dns.magic_dns", "false", "--yes")
	x.code(ExitUnauthorized, "policy", "set", writeTemp(t, "p.hujson", "{}"), "--yes")
	x.ok("nodes", "ls") // reads still work
	x.readOnly = false
	x.code(ExitUnauthorized, "nodes", "delete", "1", "--yes", "--read-only")
	if len(x.log.es) != 0 {
		t.Errorf("refused changes were logged: %v", x.log.ids())
	}
}

func TestHostExitCodes(t *testing.T) {
	x := newHarness(t).remote()
	for _, args := range [][]string{
		{"svc", "status"}, {"svc", "restart", "--yes"}, {"conf", "get", "server_url"}, {"dns", "show"},
		{"backup", "ls"}, {"host", "doctor"}, {"derp", "show"},
	} {
		x.code(ExitNoHost, args...)
		if !strings.Contains(x.err.String(), "Fix:") {
			t.Errorf("%v: no fix on stderr: %q", args, x.err.String())
		}
	}

	y := newHarness(t)
	y.h.FailPermission(host.NeedService, "sudo usermod -aG headscale $USER")
	y.code(ExitPermission, "svc", "restart", "--yes")
	if !strings.Contains(y.err.String(), "Fix: sudo usermod -aG headscale $USER") {
		t.Errorf("stderr: %q", y.err.String())
	}
	y.h.FailPermission(host.NeedLogs, "")
	y.code(ExitPermission, "svc", "logs")
	y.h.FailPermission(host.NeedConfigR, "sudo chmod g+r /etc/headscale/config.yaml")
	y.code(ExitPermission, "conf", "get", "server_url")
}

// --- T147: confirmation ------------------------------------------------------

func TestConfirmation(t *testing.T) {
	x := newHarness(t)
	x.interactive = true

	x.stdin = "n\n"
	x.code(ExitAborted, "nodes", "expire", "1")
	x.stdin = "y\n"
	x.ok("nodes", "expire", "1")
	if !strings.Contains(x.err.String(), "Continue? [y/N]") || !strings.Contains(x.err.String(), "re-authenticate") {
		t.Errorf("prompt: %q", x.err.String())
	}

	// Critical: the name must be typed.
	x.stdin = "yes\n"
	x.code(ExitAborted, "nodes", "delete", "1")
	x.stdin = "laptop-alice\n"
	x.ok("nodes", "delete", "1")
	if _, err := x.b.GetNode(context.Background(), 1); !api.IsKind(err, api.KindNotFound) {
		t.Errorf("node 1 still there: %v", err)
	}

	// --yes skips the prompt even on a terminal.
	x.stdin = ""
	x.ok("nodes", "expire", "2", "--yes")

	// A form-backed change with no danger runs without asking.
	x.interactive = false
	x.ok("users", "create", "dave")
}

func TestConfirmHelpers(t *testing.T) {
	if got := describeTargets(nil); got != "" {
		t.Error(got)
	}
	ts := []module.Target{{Name: "a"}, {Name: "b"}, {ID: "c"}, {Name: "d"}}
	if got := describeTargets(ts); got != "a, b, c and 1 more" {
		t.Error(got)
	}
	if criticalName(nil) != "yes" || criticalName(ts) != "4" || criticalName(ts[2:3]) != "c" {
		t.Error("criticalName")
	}
}

// --- T146: verbs reuse module Actions --------------------------------------

func TestNodeVerbs(t *testing.T) {
	x := newHarness(t)
	ctx := context.Background()
	x.ok("nodes", "rename", "laptop-alice", "lap-a")
	if n, _ := x.b.GetNode(ctx, 1); n.GivenName != "lap-a" {
		t.Errorf("rename: %q", n.GivenName)
	}
	var out nodeOut
	if err := json.Unmarshal([]byte(x.ok("nodes", "tags", "1", "--set", "web,db", "-o", "json")), &out); err != nil {
		t.Fatal(err)
	}
	if strings.Join(out.Tags, ",") != "tag:db,tag:web" {
		t.Errorf("set: %v", out.Tags)
	}
	x.ok("nodes", "tags", "1", "--add", "ci", "--rm", "web")
	if n, _ := x.b.GetNode(ctx, 1); strings.Join(n.Tags, ",") != "tag:ci,tag:db" {
		t.Errorf("add/rm: %v", n.Tags)
	}
	x.ok("nodes", "expire", "1", "--disable", "--yes")
	if n, _ := x.b.GetNode(ctx, 1); !n.ExpiryDisabled() {
		t.Error("expiry not disabled")
	}
	x.ok("nodes", "expire", "1", "--yes")
	if n, _ := x.b.GetNode(ctx, 1); !n.Expired(fixed.Add(time.Second)) {
		t.Error("not expired")
	}
	x.ok("nodes", "register", "--user", "ops", "hskey-abcdef0123456789", "-o", "json")
	if !strings.Contains(x.out.String(), `"name": "ops"`) {
		t.Errorf("register: %s", x.out.String())
	}
	x.ok("nodes", "delete", "1", "--yes")
	if strings.Join(x.log.ids(), " ") != "machines.rename:ok machines.tags:ok machines.tags:ok machines.noexpire:ok machines.expire:ok pending.paste:ok machines.delete:ok" {
		t.Errorf("activity: %v", x.log.ids())
	}
	if e := x.log.es[0]; e.Profile != "host:test" || len(e.Targets) != 1 || e.Targets[0].ID != "1" {
		t.Errorf("entry: %+v", e)
	}
}

func TestRouteVerbs(t *testing.T) {
	x := newHarness(t)
	ctx := context.Background()
	x.ok("routes", "approve", "homelab-alice", "10.20.0.0/16", "--yes")
	if n, _ := x.b.GetNode(ctx, 5); len(n.PendingRoutes()) != 0 {
		t.Errorf("still pending: %v", n.PendingRoutes())
	}
	x.ok("routes", "revoke", "5", "10.20.0.0/16", "--yes", "-o", "json")
	if n, _ := x.b.GetNode(ctx, 5); len(n.PendingRoutes()) != 1 {
		t.Errorf("not revoked: %v", n.ApprovedRoutes)
	}
	x.ok("routes", "revoke", "exit-fra-1", "exit", "--yes")
	if n, _ := x.b.GetNode(ctx, 32); n.ExitApproved() {
		t.Error("exit still approved")
	}
	x.code(ExitAborted, "routes", "approve", "exit-fra-1", "exit")
}

func TestUserVerbs(t *testing.T) {
	x := newHarness(t)
	var u userOut
	if err := json.Unmarshal([]byte(x.ok("users", "create", "dave", "--display-name", "Dave", "--email", "d@example.com", "-o", "json")), &u); err != nil {
		t.Fatal(err)
	}
	if u.Name != "dave" || u.Email != "d@example.com" || u.DisplayName != "Dave" {
		t.Errorf("create: %+v", u)
	}
	x.ok("users", "rename", "dave", "david", "-o", "yaml")
	if !strings.Contains(x.out.String(), "name: david") {
		t.Errorf("rename: %s", x.out.String())
	}
	x.code(ExitAborted, "users", "delete", "david")
	x.ok("users", "delete", "david", "--yes")
	us, _ := x.b.ListUsers(context.Background(), api.UserFilter{Name: "david"})
	if len(us) != 0 {
		t.Errorf("not deleted: %v", us)
	}
}

func TestKeyVerbs(t *testing.T) {
	x := newHarness(t)
	key := strings.TrimSpace(x.ok("keys", "preauth", "create", "--user", "ops", "--reusable", "--tags", "server", "--expiration", "1h"))
	if !strings.HasPrefix(key, "hskey-auth-") || strings.Contains(key, "…") {
		t.Fatalf("created key: %q", key)
	}
	var k preKeyOut
	if err := json.Unmarshal([]byte(x.ok("keys", "preauth", "create", "--user", "alice", "--ephemeral", "-o", "json")), &k); err != nil {
		t.Fatal(err)
	}
	if !k.Ephemeral || k.Reusable || k.User.Name != "alice" || strings.Contains(k.Key, "…") {
		t.Errorf("json key: %+v", k)
	}
	x.ok("keys", "preauth", "create", "--user", "ops", "--qr", "-o", "yaml")
	if !strings.Contains(x.out.String(), "key: hskey-auth-") {
		t.Errorf("yaml: %s", x.out.String())
	}
	x.code(ExitInvalid, "keys", "preauth", "create", "--user", "nobody")
	x.code(ExitInvalid, "keys", "preauth", "create", "--user", "ops", "--expiration", "soon")

	x.ok("keys", "preauth", "expire", "1", "--yes")
	x.ok("keys", "preauth", "delete", "2", "--yes")
	ks, _ := x.b.ListPreAuthKeys(context.Background(), api.KeyFilter{})
	for _, k := range ks {
		if k.ID == 2 {
			t.Error("key 2 not deleted")
		}
		if k.ID == 1 && k.State(fixed.Add(time.Second)) != api.KeyExpired {
			t.Error("key 1 not expired")
		}
	}

	api1 := strings.TrimSpace(x.ok("keys", "api", "create", "--expiration", "30d"))
	if !strings.HasPrefix(api1, "hskey-api-") {
		t.Fatalf("api key: %q", api1)
	}
	x.ok("keys", "api", "create", "-o", "json")
	if !strings.Contains(x.out.String(), `"key": "hskey-api-`) {
		t.Errorf("api json: %s", x.out.String())
	}
	x.ok("keys", "api", "expire", "1", "--yes") // by ID
	aks, _ := x.b.ListAPIKeys(context.Background())
	x.ok("keys", "api", "delete", aks[1].Prefix, "--yes") // by prefix
	x.code(ExitUsage, "keys", "preauth")
	x.code(ExitUsage, "keys", "preauth", "bogus")
}

func TestAuthVerbs(t *testing.T) {
	x := newHarness(t).remote()
	x.ok("auth", "approve", "pXq7Rz2Lm9Kd", "--user", "ops")
	x.code(ExitAborted, "auth", "reject", "Vb3Nw8Ty1Hs6")
	x.ok("auth", "reject", "https://hs.example.com/register/Vb3Nw8Ty1Hs6", "--yes")
	x.code(ExitNotFound, "auth", "approve", "pXq7Rz2Lm9Kd")
	x.code(ExitInvalid, "auth", "approve", "pXq7Rz2Lm9Kd", "--user", "nobody")

	// In host mode the server's queue already lists entries; the CLI seeks to it.
	y := newHarness(t)
	y.ok("auth", "approve", "Vb3Nw8Ty1Hs6", "--user", "bob")
}

func TestPolicyVerbs(t *testing.T) {
	x := newHarness(t).remote()
	live := x.ok("policy", "get")
	if !strings.Contains(live, "groups") {
		t.Fatalf("get: %q", live)
	}
	var p policyOut
	if err := json.Unmarshal([]byte(x.ok("policy", "get", "-o", "json")), &p); err != nil || p.Source != "api" {
		t.Fatalf("json: %v %+v", err, p)
	}
	good := strings.Replace(live, `"groups": {`, `"groups": {"group:cli": ["alice@"],`, 1)
	f := writeTemp(t, "p.hujson", good)
	x.ok("policy", "check", f)
	x.ok("policy", "diff", f)
	if !strings.Contains(x.out.String(), `+`) || !strings.Contains(x.out.String(), "group:cli") {
		t.Errorf("diff: %s", x.out.String())
	}
	bad := writeTemp(t, "bad.hujson", "{ not json")
	x.code(ExitInvalid, "policy", "check", bad)
	x.code(ExitInvalid, "policy", "set", bad, "--yes")
	x.code(ExitAborted, "policy", "set", f)
	x.ok("policy", "set", f, "--yes")
	if cur, _ := x.b.GetPolicy(context.Background()); !strings.Contains(cur.Raw, "group:cli") {
		t.Error("policy not applied")
	}
	x.ok("policy", "set", f, "--yes") // unchanged: nothing to do
	x.ok("policy", "history", "-o", "json")
	var revs []revisionOut
	if err := json.Unmarshal(x.out.Bytes(), &revs); err != nil || len(revs) < 2 {
		t.Fatalf("history: %v %s", err, x.out.String())
	}
	x.ok("policy", "history")
	x.ok("policy", "rollback", "2", "--yes")
	if cur, _ := x.b.GetPolicy(context.Background()); strings.Contains(cur.Raw, "group:cli") {
		t.Error("rollback did not restore the old policy")
	}
	x.code(ExitNotFound, "policy", "rollback", "99", "--yes")
	x.code(ExitUsage, "policy", "rollback", "zero")
	x.code(ExitError, "policy", "check", "/no/such/file")
	got := []string{}
	for _, e := range x.log.es {
		got = append(got, e.ActionID)
	}
	if strings.Join(got, " ") != "policy.apply policy.rollback" {
		t.Errorf("activity: %v", got)
	}

	// Host file mode goes through the managed-file pipeline and reloads.
	y := newHarness(t)
	y.ok("policy", "get", "-o", "yaml")
	if !strings.Contains(y.out.String(), "source: file") {
		t.Errorf("file mode: %s", y.out.String())
	}
	raw, _ := y.h.File(hostfake.PolicyPath)
	next := strings.Replace(string(raw), "{", "{\n  // edited by the cli", 1)
	y.ok("policy", "set", writeTemp(t, "q.hujson", next), "--yes")
	if b, _ := y.h.File(hostfake.PolicyPath); !strings.Contains(string(b), "edited by the cli") {
		t.Error("policy file not written")
	}
	y.code(ExitInvalid, "policy", "check", bad)

	// stdin works as a file.
	x.stdin = good
	x.ok("policy", "check", "-")
}

func TestServerVerbs(t *testing.T) {
	x := newHarness(t)
	x.ok("server", "backfill-ips", "--yes")
	x.ok("server", "backfill-ips", "--yes", "-o", "json")
	if !strings.Contains(x.out.String(), `"changes"`) {
		t.Errorf("backfill json: %s", x.out.String())
	}
	x.code(ExitAborted, "server", "backfill-ips")
	x.ok("upgrade", "check")
	if !strings.Contains(x.err.String(), "v0.30.1") {
		t.Errorf("upgrade: %q", x.err.String())
	}
}

func TestHostVerbs(t *testing.T) {
	x := newHarness(t)
	info := x.ok("host", "info")
	if !strings.Contains(info, "hs-demo") || !strings.Contains(info, "ok local API") {
		t.Errorf("host info: %s", info)
	}
	x.ok("host", "info", "-o", "json")
	var env envOut
	if err := json.Unmarshal(x.out.Bytes(), &env); err != nil || !env.Usable || env.Hostname != "hs-demo" {
		t.Errorf("host info json: %v %+v", err, env)
	}
	x.ok("host", "doctor", "-o", "wide")
	if !strings.Contains(x.out.String(), "Socket") {
		t.Errorf("doctor: %s", x.out.String())
	}

	x.ok("svc", "restart", "--yes")
	x.ok("svc", "reload", "--yes")
	x.code(ExitAborted, "svc", "stop")
	x.ok("svc", "stop", "--yes")
	x.ok("svc", "start", "--yes")
	if st, _ := x.h.Service().Status(context.Background()); !st.Running {
		t.Errorf("service: %+v", st)
	}
	logs := x.ok("svc", "logs", "--tail", "20", "--level", "warn")
	for _, l := range strings.Split(strings.TrimSpace(logs), "\n") {
		if l != "" && !strings.Contains(l, "WRN") && !strings.Contains(l, "ERR") {
			t.Errorf("level filter let through %q", l)
		}
	}
	x.ok("svc", "logs", "--tail", "5", "-o", "json", "--grep", "node")
	x.code(ExitUsage, "svc", "logs", "--level", "loud")
	x.code(ExitUsage, "svc", "logs", "--grep", "(")

	if v := strings.TrimSpace(x.ok("conf", "get", "dns.base_domain")); v != "tail.demo.example.com" {
		t.Errorf("conf get: %q", v)
	}
	x.ok("conf", "get", "dns.nameservers.global")
	x.code(ExitNotFound, "conf", "get", "no.such.key")
	x.code(ExitAborted, "conf", "set", "dns.base_domain", "tail.corp.example")
	x.ok("conf", "set", "dns.base_domain", "tail.corp.example", "--yes", "--no-restart")
	if b, _ := x.h.File(hostfake.ConfigPath); !strings.Contains(string(b), "tail.corp.example") {
		t.Error("config not written")
	}
	x.ok("dns", "set", "magic_dns", "false", "--yes")
	x.ok("conf", "diff", writeTemp(t, "c.yaml", "server_url: https://x\n"))
	x.code(ExitError, "conf", "migrate", "--yes") // nothing deprecated in the seed

	x.ok("dns", "records", "add", "web.tail.corp.example", "a", "100.64.0.9", "--yes")
	x.ok("dns", "records", "ls")
	if !strings.Contains(x.out.String(), "web.tail.corp.example") {
		t.Errorf("records: %s", x.out.String())
	}
	x.code(ExitInvalid, "dns", "records", "add", "web.tail.corp.example", "A", "100.64.0.9", "--yes")
	x.code(ExitInvalid, "dns", "records", "add", "bad name", "A", "1.2.3.4", "--yes")
	x.ok("dns", "records", "rm", "web.tail.corp.example", "--yes")
	x.code(ExitNotFound, "dns", "records", "rm", "web.tail.corp.example", "--yes")
	x.ok("derp", "regions", "ls", "-o", "json")

	x.ok("backup", "create")
	x.ok("backup", "ls", "-o", "json")
	var bs []bundleOut
	if err := json.Unmarshal(x.out.Bytes(), &bs); err != nil || len(bs) != 1 {
		t.Fatalf("backups: %v %s", err, x.out.String())
	}
	x.ok("backup", "ls", "-o", "wide")
	x.code(ExitAborted, "backup", "restore", bs[0].ID)
	x.ok("backup", "restore", bs[0].ID, "--yes")
	x.code(ExitNotFound, "backup", "restore", "19990101T000000Z", "--yes")

	var ids []string
	for _, e := range x.log.es {
		ids = append(ids, e.ActionID)
	}
	if !strings.Contains(strings.Join(ids, " "), "service.restart service.reload service.stop service.start conf.set") {
		t.Errorf("activity: %v", ids)
	}
}

func TestHostInfoUnusable(t *testing.T) {
	x := newHarness(t)
	opts := x.options()
	opts.DetectEnv = func(config.Flags) host.Env {
		return host.Env{ConfigPath: "/etc/headscale/config.yaml", Transport: host.TransportUnixHTTP,
			Perms: host.Perms{host.NeedSocket: {Reason: "permission denied", FixCommand: "sudo usermod -aG headscale $USER", Denied: true}}}
	}
	var out, errb bytes.Buffer
	opts.Stdout, opts.Stderr = &out, &errb
	if code := Run([]string{"host", "info"}, opts); code != ExitPermission {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "fix: sudo usermod") || !strings.Contains(errb.String(), "Fix: sudo usermod") {
		t.Errorf("out %q err %q", out.String(), errb.String())
	}
	opts.DetectEnv = func(config.Flags) host.Env { return host.Env{} }
	if code := Run([]string{"host", "info"}, opts); code != ExitNoHost {
		t.Fatalf("exit %d", code)
	}
}

func TestLocalVerbs(t *testing.T) {
	x := newHarness(t)
	if !strings.HasPrefix(x.ok("version"), "headsup ") {
		t.Errorf("version: %q", x.out.String())
	}
	x.ok("version", "-o", "json")
	if strings.TrimSpace(x.ok("config", "path")) != x.cfgPath {
		t.Errorf("config path: %q", x.out.String())
	}
	if n := strings.Count(x.ok("themes"), "\n"); n < 10 {
		t.Errorf("themes: %d lines", n)
	}
	x.ok("themes", "ls", "-o", "json")
	x.ok("themes", "preview", "tokyo-night")
	x.code(ExitNotFound, "themes", "preview", "nope")
	x.ok("themes", "reload")
	if x.connects != 0 {
		t.Errorf("local verbs connected %d times", x.connects)
	}

	x.ok("config", "theme", "tokyo-night")
	x.code(ExitNotFound, "config", "theme", "nope")
	x.code(ExitUsage, "config", "profile", "add", "prod")
	x.code(ExitUsage, "config", "profile", "add", "prod", "--url", "https://hs.example.com")
	x.ok("config", "profile", "add", "prod", "--url", "https://hs.example.com", "--api-key-env", "HS_KEY", "--read-only")
	cfg, err := config.Load(x.cfgPath)
	if err != nil || len(cfg.Profiles) != 1 || cfg.DefaultProfile != "prod" || !boolOf(cfg.Profiles[0].ReadOnly) {
		t.Fatalf("saved: %v %+v", err, cfg)
	}
	x.cfg = cfg
	x.ok("config", "profile", "add", "lab", "--url", "https://lab.example.com", "--keyring")
	x.cfg, _ = config.Load(x.cfgPath)
	x.ok("config", "profile", "default", "lab")
	x.cfg, _ = config.Load(x.cfgPath)
	x.ok("config", "profile", "ls", "-o", "json")
	var ps []profileOut
	if err := json.Unmarshal(x.out.Bytes(), &ps); err != nil || len(ps) != 2 || !ps[0].Default || ps[0].Name != "lab" {
		t.Fatalf("profiles: %v %s", err, x.out.String())
	}
	x.ok("config", "profile", "ls")
	x.ok("config", "profile", "rm", "lab")
	x.cfg, _ = config.Load(x.cfgPath)
	x.code(ExitNotFound, "config", "profile", "rm", "lab")
	x.code(ExitNotFound, "config", "profile", "default", "lab")
	if x.cfg.DefaultProfile != "" {
		t.Errorf("default not cleared: %q", x.cfg.DefaultProfile)
	}
}

func TestParseInterleaved(t *testing.T) {
	r := newRunner(Options{})
	v := r.newFlags("x", "")
	n := v.fs.Bool("n", false, "")
	pos, err := v.parse([]string{"a", "-n", "b", "--", "-c"})
	if err != nil || !*n || strings.Join(pos, " ") != "a b -c" {
		t.Fatalf("%v %v %v", pos, err, *n)
	}
}

func TestCodeOf(t *testing.T) {
	cases := []struct {
		err  error
		code int
	}{
		{&host.ErrPermission{FixCommand: "x"}, ExitPermission},
		{&host.ErrInvalid{}, ExitInvalid},
		{host.ErrNoHost, ExitNoHost},
		{api.Errf(api.KindNotFound, "x"), ExitNotFound},
		{api.Errf(api.KindUnsupported, "x"), ExitUnsupported},
		{api.Errf(api.KindInvalid, "x"), ExitInvalid},
		{api.Errf(api.KindConflict, "x"), ExitError},
		{os.ErrClosed, ExitError},
	}
	for _, c := range cases {
		if got, _ := codeOf(c.err); got != c.code {
			t.Errorf("%v: %d, want %d", c.err, got, c.code)
		}
	}
}

func writeTemp(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}
