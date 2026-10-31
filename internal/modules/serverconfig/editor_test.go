package serverconfig

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestEditorArgv(t *testing.T) {
	if got := editorArgv(env(nil)); len(got) != 1 || got[0] != "vi" {
		t.Errorf("default %v", got)
	}
	if got := editorArgv(env(map[string]string{"EDITOR": "nano"})); got[0] != "nano" {
		t.Errorf("EDITOR %v", got)
	}
	if got := editorArgv(env(map[string]string{"EDITOR": "nano", "VISUAL": "code -w"})); len(got) != 2 || got[0] != "code" || got[1] != "-w" {
		t.Errorf("VISUAL %v", got)
	}
}

func TestEditorSessionRoundTrip(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a /bin/sh editor script and Unix mode bits")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "ed.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf 'log:\\n  level: debug\\n' > \"$1\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	cmd, done, err := editorSession([]byte("log:\n  level: info\n"), env(map[string]string{"EDITOR": script}))
	if err != nil {
		t.Fatal(err)
	}
	tmp := cmd.Args[len(cmd.Args)-1]
	fi, err := os.Stat(tmp)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("temp copy must be private: %v %v", fi.Mode(), err)
	}
	if b, _ := os.ReadFile(tmp); string(b) != "log:\n  level: info\n" {
		t.Errorf("temp holds %q", b)
	}
	msg := done(cmd.Run()).(editedMsg)
	if msg.err != nil || string(msg.data) != "log:\n  level: debug\n" {
		t.Errorf("edited %q %v", msg.data, msg.err)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Error("temp copy must be removed")
	}

	_, done2, _ := editorSession([]byte("x: 1\n"), env(nil))
	if msg := done2(os.ErrPermission).(editedMsg); msg.err == nil || !strings.Contains(msg.err.Error(), "editor vi") {
		t.Errorf("failed editor: %v", msg.err)
	}
	if editorCmd([]byte("x: 1\n")) == nil {
		t.Error("editorCmd")
	}
}

func TestRebase(t *testing.T) {
	base := []byte("# c\nnode:\n  expiry: 0\nlog:\n  level: info\n  format: text\ntrusted_proxies: []\nold: 1\n")
	mine := []byte("# c\nnode:\n  expiry: 90d\nlog:\n  level: info\n  format: text\ntrusted_proxies:\n  - 10.0.0.0/8\n")
	theirs := []byte("# c\n# theirs\nnode:\n  expiry: 0\nlog:\n  level: warn\n  format: text\ntrusted_proxies: []\nold: 1\n")
	out, keys, err := Rebase(base, mine, theirs)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	for _, want := range []string{"# theirs", "expiry: 90d", "level: warn", "trusted_proxies: [10.0.0.0/8]"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "old:") || len(keys) != 3 {
		t.Errorf("keys %v\n%s", keys, got)
	}

	// The same key changed on both sides is refused.
	clash := []byte("node:\n  expiry: 7d\n")
	if _, _, err := Rebase([]byte("node:\n  expiry: 0\n"), []byte("node:\n  expiry: 90d\n"), clash); err == nil || !strings.Contains(err.Error(), "node.expiry") {
		t.Errorf("clash: %v", err)
	}
	// Both sides making the same change is fine.
	if _, _, err := Rebase([]byte("a: 1\n"), []byte("a: 2\n"), []byte("a: 2\n")); err != nil {
		t.Errorf("same change: %v", err)
	}
	// Lists of mappings cannot be carried key by key.
	if _, _, err := Rebase([]byte("r: []\n"), []byte("r:\n  - {name: a}\n"), []byte("r: []\nz: 1\n")); err == nil {
		t.Error("list of mappings should be refused")
	}
	for i, bad := range [][3]string{{"a: [", "a: 1", "a: 1"}, {"a: 1", "a: [", "a: 1"}, {"a: 1", "a: 1", "a: ["}} {
		if _, _, err := Rebase([]byte(bad[0]), []byte(bad[1]), []byte(bad[2])); err == nil {
			t.Errorf("bad YAML %d accepted", i)
		}
	}
}
