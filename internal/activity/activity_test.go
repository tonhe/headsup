package activity

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tonhe/headsup/internal/api"
)

func TestRedact(t *testing.T) {
	e := Redact(Entry{
		ActionID: "keys.create",
		Params: map[string]any{
			"api_key":    "plain",
			"AuthToken":  "plain",
			"password":   "plain",
			"oidcSecret": "plain",
			"user":       "alice",
			"note":       "made hskey-auth-abc123_DEF and tskey-xyz",
			"nested":     map[string]any{"token": "x", "ok": "fine"},
			"list":       []string{"hskey-a1", "keep"},
			"secretobj":  api.NewSecret("hskey-api-longlonglong"),
			"wrapped":    api.NewSecret("hskey-api-longlonglong"),
			"count":      3,
		},
		Result: Err("api", "rejected key hskey-api-0123456789"),
	})
	p := e.Params
	for _, k := range []string{"api_key", "AuthToken", "password", "oidcSecret", "secretobj"} {
		if p[k] != Redacted {
			t.Errorf("%s = %v", k, p[k])
		}
	}
	if p["user"] != "alice" || p["count"] != 3 {
		t.Errorf("non-secret changed: %v", p)
	}
	if p["note"] != "made [redacted] and [redacted]" {
		t.Errorf("note = %v", p["note"])
	}
	n := p["nested"].(map[string]any)
	if n["token"] != Redacted || n["ok"] != "fine" {
		t.Errorf("nested = %v", n)
	}
	if l := p["list"].([]string); l[0] != Redacted || l[1] != "keep" {
		t.Errorf("list = %v", l)
	}
	if w := p["wrapped"].(string); strings.Contains(w, "longlonglong") {
		t.Errorf("wrapped = %v", w)
	}
	if strings.Contains(e.Result.Message, "0123456789") {
		t.Errorf("message = %q", e.Result.Message)
	}
}

func TestFileLogPermsAndRecent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{"a.one", "a.two", "a.three"} {
		err := l.Log(Entry{
			Time:     time.Unix(int64(1000+i), 0).UTC(),
			ActionID: id,
			Targets:  []Target{{Kind: "node", ID: "1", Name: "n"}},
			Params:   map[string]any{"key": "hskey-secret-1"},
			Result:   OK(),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	raw, _ := os.ReadFile(l.Path())
	if strings.Contains(string(raw), "hskey-secret") {
		t.Fatal("secret written to disk")
	}
	got, err := l.Recent(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ActionID != "a.three" || got[1].ActionID != "a.two" || got[0].Result.Status != "ok" {
		t.Fatalf("recent = %+v", got)
	}
	if all, _ := l.Recent(0); len(all) != 3 {
		t.Fatalf("all = %d", len(all))
	}
	if runtime.GOOS == "windows" {
		return
	}
	di, _ := os.Stat(dir)
	fi, _ := os.Stat(l.Path())
	if di.Mode().Perm() != 0o700 || fi.Mode().Perm() != 0o600 {
		t.Fatalf("dir %v file %v", di.Mode().Perm(), fi.Mode().Perm())
	}
}

func TestRefuseInsecure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	base := t.TempDir()
	loose := filepath.Join(base, "loose")
	os.Mkdir(loose, 0o700)
	os.Chmod(loose, 0o755)
	if _, err := Open(loose); err == nil || !strings.Contains(err.Error(), "chmod 700") {
		t.Fatalf("loose dir: %v", err)
	}

	real := filepath.Join(base, "real")
	os.Mkdir(real, 0o700)
	link := filepath.Join(base, "link")
	os.Symlink(real, link)
	if _, err := Open(link); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink: %v", err)
	}

	os.WriteFile(filepath.Join(real, FileName), nil, 0o600)
	os.Chmod(filepath.Join(real, FileName), 0o644)
	if _, err := Open(real); err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Fatalf("loose file: %v", err)
	}
}

func TestNop(t *testing.T) {
	var l Logger = Nop{}
	if err := l.Log(Entry{}); err != nil {
		t.Fatal(err)
	}
	if e, err := l.Recent(5); err != nil || e != nil {
		t.Fatal("nop recent")
	}
	var _ Logger = (*File)(nil)
}
