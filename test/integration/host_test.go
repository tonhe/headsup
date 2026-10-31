//go:build integration

package integration

// Quickstart section 3: H1-H11 in host mode against the containerised
// headscale ([host].container), plus H2 inside the container as a user
// without access to the socket.

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tonhe/headsup/internal/brand"
	"github.com/tonhe/headsup/internal/host"
)

// H1: launched with no flags the header shows host:<hostname> and the
// dashboard has live metric sparklines.
func TestH01HostHeaderAndSparklines(t *testing.T) {
	joinedNode(t)
	d, env := hostTUI(t, 140, 45)
	d.Wait(tuiWait, "host:"+env.Hostname, "(host)")
	d.WaitFunc(30*time.Second, "metric sparklines", func(s string) bool {
		return strings.ContainsAny(s, "▁▂▃▄▅▆▇") && !strings.Contains(s, "collecting…")
	})
	r := ok(t, S.cli(t, "host", "info", "-o", "json"))
	info := decode[map[string]any](t, r)
	if info["transport"] != host.TransportContainerExec.String() || !strings.Contains(info["container"].(string), S.name) {
		t.Errorf("host info: %v", info)
	}
}

// H2: a user without access to headscale's socket gets a permission
// explainer with the fix and the remote-mode option. Run inside the
// container, as nobody.
func TestH02PermissionExplainer(t *testing.T) {
	if out, err := run("docker", "cp", S.binLinux, S.name+":/ko-app/"+brand.Name); err != nil {
		t.Fatalf("copying %s into the container: %v\n%s", brand.Name, err, out)
	}
	out, err := run("docker", "exec", "-u", "65534:65534", "-e", "HOME=/tmp", S.name, brand.Name, "nodes", "ls")
	if err == nil {
		t.Fatalf("nobody should not reach the socket:\n%s", out)
	}
	for _, want := range []string{"host mode is not available", "headscale.sock", "Fix: sudo", "--url"} {
		if !strings.Contains(out, want) {
			t.Errorf("explainer should mention %q:\n%s", want, out)
		}
	}
	// As root inside the container (a host install run as root) it works.
	if out, err := run("docker", "exec", "-e", "HOME=/tmp", S.name, brand.Name, "nodes", "ls"); err != nil {
		t.Errorf("as root inside the container: %v\n%s", err, out)
	}
}

// H3: a machine's detail has OS, client version, DERP region and latency and
// endpoints; p pings it.
func TestH03MachineDetailAndPing(t *testing.T) {
	_, id := joinedNode(t)
	_, h, _ := hostConn(t)
	var ex host.NodeExtras
	eventually(t, 30*time.Second, "database enrichment", func() error {
		m, err := h.NodeExtras(context.Background(), []uint64{id})
		if err != nil {
			return err
		}
		ex = m[id]
		if ex.OS == "" || ex.ClientVersion == "" || len(ex.Endpoints) == 0 {
			return errf("extras %+v", ex)
		}
		return nil
	})
	if ex.PreferredDERP == 0 {
		t.Logf("no preferred DERP region (offline runner?): %+v", ex)
	}
	d, _ := hostTUI(t, 140, 60)
	d.Key("1", "m")
	// The detail pane shows OS and client; DERP and endpoints are in it too
	// but below the fold, so check them as table columns at full width.
	d.Wait(tuiWait, "client-1", "OS", ex.OS, "Client", ex.ClientVersion)
	d.Key("p")
	d.WaitFunc(45*time.Second, "a ping answer", func(s string) bool { return strings.Contains(s, "answered in") })
	wide, _ := hostTUI(t, 260, 40)
	wide.Key("1", "m")
	wide.Wait(tuiWait, "client-1", "DERP")
}

// H4: a web-login device shows as waiting in Add device, and pasting its URL
// with p registers it.
func TestH04WebLoginRegister(t *testing.T) {
	c := S.startClient(t, "web-1", "")
	url := c.loginURL(t)
	d, _ := hostTUI(t, 120, 40)
	d.Key("1", "p")
	d.Wait(tuiWait, "headscale reports 1 device(s) waiting")
	d.Key("p")
	d.WaitFunc(tuiWait, "the paste form", func(s string) bool { return strings.Contains(s, "URL") || strings.Contains(s, "key") })
	d.Key(url)
	d.Key("ctrl+s")
	waitNode(t, "web-1", 60*time.Second)
	c.stop(t)
}

// H5: g d, add nas.home.arpa A 100.64.0.3: a diff and confirm, and it
// resolves from a client with no restart.
func TestH05DNSRecord(t *testing.T) {
	c, _ := joinedNode(t)
	before := startedAt(t)
	d, _ := hostTUI(t, 120, 40)
	d.Key("2", "d")
	d.Wait(tuiWait, "Extra records")
	d.Key("tab", "n")
	d.Wait(tuiWait, "Add record")
	d.Key("ctrl+u")
	d.Type("nas.home.arpa")
	d.Key("tab", "tab")
	d.Type("100.64.0.3")
	d.Key("ctrl+s")
	d.Wait(tuiWait, "Pending changes", `"nas.home.arpa"`)
	d.Key("ctrl+s")
	d.Wait(tuiWait, "Apply DNS changes")
	d.Key("y")
	eventually(t, 60*time.Second, "the record resolving on the client", func() error {
		out, err := c.exec("tailscale", "dns", "query", "nas.home.arpa")
		if err != nil || !strings.Contains(out, "100.64.0.3") {
			return errf("%v: %s", err, out)
		}
		return nil
	})
	if startedAt(t) != before {
		t.Error("adding a record must not restart headscale")
	}
	t.Cleanup(func() { _ = os.WriteFile(filepath.Join(S.cfgDir, "extra-records.json"), []byte("[]\n"), 0o644) })
}

// H6, H7: an invalid node.expiry is refused before writing (the file is
// byte-identical); a valid one gives a diff, a backup, a write and a
// restart offer, health turns green, and u rolls back.
func TestH06H07ServerSettings(t *testing.T) {
	orig := readConfig(t)
	t.Cleanup(func() {
		if !bytes.Equal(readConfig(t), orig) {
			_ = os.WriteFile(S.configPath(), orig, 0o644)
			_, _ = run("docker", "restart", S.name)
			_ = S.waitHealthy(60 * time.Second)
		}
	})
	d, _ := hostTUI(t, 120, 40)
	d.Key("2", "s")
	d.Wait(tuiWait, "Server Settings")
	d.Key("tab")
	d.Wait(tuiWait, "node.expiry")
	d.Key("enter", "ctrl+u")
	d.Type("banana")
	d.Key("enter")
	d.Wait(tuiWait, `invalid duration "banana"`)
	if !bytes.Equal(readConfig(t), orig) {
		t.Fatal("H6: an invalid value must not touch the file")
	}
	// CLI path to the same refusal.
	exitCode(t, S.cli(t, "conf", "set", "node.expiry", "banana", "--yes"), 6)
	if !bytes.Equal(readConfig(t), orig) {
		t.Fatal("H6: conf set must not touch the file on an invalid value")
	}

	// H7.
	d.Key("ctrl+u")
	d.Type("2160h")
	d.Key("enter")
	d.Wait(tuiWait, "Changed, not saved")
	d.Key("ctrl+s")
	d.Wait(tuiWait, "Valid", "expiry: 2160h")
	d.Key("ctrl+s")
	d.Wait(tuiWait, "Write config.yaml", "backup")
	d.Key("y")
	d.Wait(tuiWait, "Wrote", "Backup", "X restart now")
	if !strings.Contains(string(readConfig(t)), "expiry: 2160h") {
		t.Fatal("H7: the value was not written")
	}
	before := startedAt(t)
	d.Key("X")
	d.Wait(tuiWait, "Restart headscale")
	d.Key("y")
	eventually(t, 90*time.Second, "headscale restarted and healthy", func() error {
		if startedAt(t) == before {
			return errf("not restarted yet")
		}
		return S.waitHealthy(time.Second)
	})
	d.WaitFunc(60*time.Second, "health to turn green", func(s string) bool { return strings.Contains(s, "healthy") })
	d.Key("u")
	d.WaitFunc(tuiWait, "the rollback confirm", func(s string) bool { return strings.Contains(s, "Restore") || strings.Contains(s, "restore") })
	d.Key("y")
	eventually(t, 90*time.Second, "the rollback", func() error {
		if !bytes.Equal(readConfig(t), orig) {
			return errf("config not restored")
		}
		return S.waitHealthy(time.Second)
	})
}

// H8: with policy.mode file, editing the policy runs the check, shows a
// diff, writes the file, reloads headscale and the new policy is active.
func TestH08FilePolicy(t *testing.T) {
	orig := readConfig(t)
	t.Cleanup(func() {
		_ = os.WriteFile(S.configPath(), orig, 0o644)
		_ = os.WriteFile(filepath.Join(S.cfgDir, "policy.hujson"), []byte(basePolicy), 0o644)
		_, _ = run("docker", "restart", S.name)
		_ = S.waitHealthy(60 * time.Second)
		_ = S.seedPolicy()
	})
	ok(t, S.cli(t, "conf", "set", "policy.mode", "file", "--yes"))
	ok(t, S.cli(t, "svc", "restart", "--yes"))
	if err := S.waitHealthy(60 * time.Second); err != nil {
		t.Fatal(err)
	}
	next := strings.Replace(basePolicy, `"acls": [`, `"acls": [
    {"action": "accept", "src": ["alice@"], "dst": ["tag:server:22"]},`, 1)
	f := filepath.Join(t.TempDir(), "policy.hujson")
	if err := os.WriteFile(f, []byte(next), 0o644); err != nil {
		t.Fatal(err)
	}
	ok(t, S.cli(t, "policy", "check", f))
	diff := ok(t, S.cli(t, "policy", "diff", f))
	if !strings.Contains(diff.stdout, "tag:server:22") {
		t.Errorf("diff should show the new rule:\n%s", diff)
	}
	ok(t, S.cli(t, "policy", "set", f, "--yes"))
	if b, _ := os.ReadFile(filepath.Join(S.cfgDir, "policy.hujson")); string(b) != next {
		t.Fatalf("policy file not written:\n%s", b)
	}
	eventually(t, 30*time.Second, "the new policy active", func() error {
		resp, err := http.Get(S.debugURL("/debug/policy"))
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		var b bytes.Buffer
		_, _ = b.ReadFrom(resp.Body)
		if !strings.Contains(b.String(), "tag:server:22") {
			return errf("not reloaded yet")
		}
		return nil
	})
}

// H9: the logs follow live lines, the level filter hides lower levels, and a
// line that mentions a machine jumps to it.
func TestH09Logs(t *testing.T) {
	joinedNode(t)
	d, _ := hostTUI(t, 140, 45)
	d.Key("2", "l")
	d.Wait(tuiWait, "Logs", "following")
	for i := 0; i < len("tdiwe") && !strings.Contains(d.Screen(), ">= WRN") && !strings.Contains(strings.ToLower(d.Screen()), ">= warn"); i++ {
		d.Key("l")
	}
	if s := strings.ToLower(d.Screen()); !strings.Contains(s, ">= wrn") && !strings.Contains(s, ">= warn") {
		t.Fatalf("no warn filter:\n%s", d.Screen())
	}
	// A bad registration URL logs an error, which passes a warn filter.
	resp, err := http.Get(S.url + "/register/hskey-authreq-tooshort")
	if err == nil {
		resp.Body.Close()
	}
	scr := d.Wait(30*time.Second, "invalid auth id")
	if strings.Contains(scr, " INF ") {
		t.Errorf("info lines should be hidden by the warn filter:\n%s", scr)
	}
	// Back to everything, find the line where the machine connected.
	for i := 0; i < 5 && strings.Contains(d.Screen(), ">= "); i++ {
		d.Key("l")
	}
	d.Key("/")
	d.Type("node.id=")
	d.Key("enter")
	d.Wait(tuiWait, "node.id=")
	d.Key("m")
	d.Wait(tuiWait, "Machines", "client-1")
}

// H10: the doctor lists findings with fixes, and a backup has the config,
// policy, records and an online database copy.
func TestH10DoctorAndBackup(t *testing.T) {
	r := S.cli(t, "host", "doctor", "-o", "json")
	if r.code != 0 {
		t.Errorf("the doctor should find no errors on a fresh container install:\n%s", r)
	}
	// headscale's state (the noise key) is root's in the container, so a
	// non-root user is refused with the fix, as on a host install.
	r = exitCode(t, S.cli(t, "backup", "create"), 9)
	if !strings.Contains(r.stderr, "sudo "+brand.Name) {
		t.Errorf("the refusal should give the fix:\n%s", r)
	}
	// Run as root (inside the container), it works.
	if out, err := run("docker", "cp", S.binLinux, S.name+":/ko-app/"+brand.Name); err != nil {
		t.Fatalf("copying %s into the container: %v\n%s", brand.Name, err, out)
	}
	root := func(args ...string) string {
		out, err := run("docker", append([]string{"exec", "-e", "HOME=/var/lib/headscale/.it-home", S.name, brand.Name}, args...)...)
		if err != nil {
			t.Fatalf("%s %v as root: %v\n%s", brand.Name, args, err, out)
		}
		return out
	}
	t.Log(root("backup", "create"))
	ls := result{stdout: root("backup", "ls", "-o", "json")}
	bs := decode[[]map[string]any](t, ls)
	if len(bs) == 0 {
		t.Fatalf("no backups listed:\n%s", ls)
	}
	last := bs[len(bs)-1]
	if f, _ := last["files"].(float64); f < 3 {
		t.Errorf("backup should hold config, policy and records: %v\n%s", last, r)
	}
	if db, _ := last["database"].(string); db == "" {
		t.Errorf("backup should hold a database copy: %v", last)
	}
	d, _ := hostTUI(t, 120, 40)
	d.Key("2", "S")
	d.Wait(tuiWait, "Doctor")
}

// H11: conf set with no TTY exits 0 and skips the restart with a notice;
// --no-restart is honoured.
func TestH11ConfSetNoTTY(t *testing.T) {
	orig := readConfig(t)
	t.Cleanup(func() { _ = os.WriteFile(S.configPath(), orig, 0o644) })
	before := startedAt(t)
	r := ok(t, S.cli(t, "conf", "set", "log.level", "debug", "--yes"))
	if !strings.Contains(r.stderr, "not restarted") {
		t.Errorf("a notice should say headscale was not restarted:\n%s", r)
	}
	r = ok(t, S.cli(t, "conf", "set", "log.level", "warn", "--yes", "--no-restart"))
	if !strings.Contains(r.stderr, "--no-restart") {
		t.Errorf("--no-restart should be acknowledged:\n%s", r)
	}
	if !strings.Contains(string(readConfig(t)), "level: warn") {
		t.Error("the value was not written")
	}
	if startedAt(t) != before {
		t.Error("headscale must not be restarted")
	}
}

func readConfig(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(S.configPath())
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// startedAt is when the headscale container last started.
func startedAt(t *testing.T) string {
	t.Helper()
	out, err := run("docker", "inspect", "-f", "{{.State.StartedAt}}", S.name)
	if err != nil {
		t.Fatalf("docker inspect: %v\n%s", err, out)
	}
	return strings.TrimSpace(out)
}

func (s *suite) debugURL(path string) string {
	return "http://127.0.0.1:" + itoa(s.metricsPort) + path
}
