package files

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tonhe/headsup/internal/host"
)

const original = "# comment\nlog:\n  level: info\nsecret: hunter2-sensitive\n"

type env struct {
	root, backups string
	m             *Manager
	clock         time.Time
}

func newEnv(t *testing.T, o Options) *env {
	t.Helper()
	tmp := t.TempDir()
	e := &env{root: filepath.Join(tmp, "root"), backups: filepath.Join(tmp, "state", "backups"), clock: time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)}
	if err := os.MkdirAll(filepath.Join(e.root, "etc/headscale"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.root, "etc/headscale/config.yaml"), []byte(original), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(e.root, "etc/headscale/config.yaml"), 0o640); err != nil {
		t.Fatal(err)
	}
	o.Root = e.root
	if o.BackupDir == "" {
		o.BackupDir = e.backups
	}
	o.Now = func() time.Time { e.clock = e.clock.Add(time.Second); return e.clock }
	e.m = New(o)
	return e
}

// unixModes reports whether the platform exposes Unix mode bits and
// ownership. Windows reports 0666/0777 and no owner, so assertions on modes
// and chown semantics only hold elsewhere (host mode targets Linux).
const unixModes = runtime.GOOS != "windows"

func skipWithoutUnixModes(t *testing.T, why string) {
	t.Helper()
	if !unixModes {
		t.Skip("Unix permission semantics unavailable on " + runtime.GOOS + ": " + why)
	}
}

func (e *env) target() string { return filepath.Join(e.root, "etc/headscale/config.yaml") }

func rejectBad(data []byte) []host.Issue {
	if bytes.Contains(data, []byte("BAD")) {
		return []host.Issue{{Severity: host.SevError, Message: "bad value", Line: 3, Source: "schema"}}
	}
	return nil
}

func TestPlanApply(t *testing.T) {
	tests := []struct {
		name    string
		next    string
		mutate  func(t *testing.T, e *env)
		wantErr func(error) bool
	}{
		{name: "valid", next: strings.Replace(original, "info", "debug", 1)},
		{name: "invalid leaves target identical", next: strings.Replace(original, "info", "BAD", 1),
			wantErr: func(err error) bool { var ei *host.ErrInvalid; return errors.As(err, &ei) }},
		{name: "changed on disk", next: strings.Replace(original, "info", "warn", 1),
			mutate: func(t *testing.T, e *env) {
				if err := os.WriteFile(e.target(), []byte(original+"# edited elsewhere\n"), 0o640); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: func(err error) bool {
				var ec *host.ErrChangedOnDisk
				return errors.As(err, &ec) && strings.Contains(string(ec.Current), "edited elsewhere")
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t, Options{Schema: map[host.FileKind]SchemaFunc{host.FileConfig: rejectBad}})
			f, err := e.m.Open(host.FileConfig, "/etc/headscale/config.yaml")
			if err != nil {
				t.Fatal(err)
			}
			c, err := e.m.Plan(context.Background(), f, []byte(tt.next))
			if err != nil {
				t.Fatal(err)
			}
			if c.Effect != host.EffectRestart {
				t.Errorf("effect = %v", c.Effect)
			}
			if tt.mutate != nil {
				tt.mutate(t, e)
			}
			before, _ := os.ReadFile(e.target())
			a, err := e.m.Apply(context.Background(), c)
			after, _ := os.ReadFile(e.target())
			if tt.wantErr != nil {
				if err == nil || !tt.wantErr(err) {
					t.Fatalf("err = %v", err)
				}
				if !bytes.Equal(before, after) {
					t.Fatal("target changed on a refused apply")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != tt.next {
				t.Fatalf("content = %q", after)
			}
			fi, _ := os.Stat(e.target())
			if unixModes && fi.Mode().Perm() != 0o640 {
				t.Errorf("mode = %v, want 0640", fi.Mode().Perm())
			}
			uid, gid, _ := Owner(fi)
			if uid != f.UID || gid != f.GID {
				t.Errorf("owner %d:%d, want %d:%d", uid, gid, f.UID, f.GID)
			}
			if a.NewSHA256 != Sum([]byte(tt.next)) {
				t.Error("NewSHA256 mismatch")
			}
			if err := e.m.Rollback(context.Background(), a); err != nil {
				t.Fatal(err)
			}
			rolled, _ := os.ReadFile(e.target())
			if !bytes.Equal(rolled, []byte(original)) {
				t.Fatalf("rollback not byte-exact: %q", rolled)
			}
		})
	}
}

func TestDiffLineNumbers(t *testing.T) {
	d := Diff([]byte("a\nb\nc\n"), []byte("a\nB\nc\nd\n"))
	want := []host.DiffLine{
		{Op: ' ', Text: "a", Old: 1, New: 1},
		{Op: '-', Text: "b", Old: 2},
		{Op: '+', Text: "B", New: 2},
		{Op: ' ', Text: "c", Old: 3, New: 3},
		{Op: '+', Text: "d", New: 4},
	}
	if len(d) != len(want) {
		t.Fatalf("got %+v", d)
	}
	for i := range want {
		if d[i] != want[i] {
			t.Errorf("line %d = %+v, want %+v", i, d[i], want[i])
		}
	}
}

func TestBackupHygiene(t *testing.T) {
	e := newEnv(t, Options{})
	f, _ := e.m.Open(host.FileConfig, "/etc/headscale/config.yaml")
	c, _ := e.m.Plan(context.Background(), f, []byte("log:\n  level: warn\n"))
	a, err := e.m.Apply(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	err = filepath.WalkDir(e.backups, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, _ := d.Info()
		want := fs.FileMode(0o600)
		if d.IsDir() {
			want = 0o700
		}
		if unixModes && fi.Mode().Perm() != want {
			t.Errorf("%s mode %v, want %v", p, fi.Mode().Perm(), want)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	bk, _ := os.ReadFile(a.BackupPath)
	if string(bk) != original {
		t.Error("backup content differs from the original")
	}
	man, err := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(a.BackupPath))), "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(man, []byte("hunter2")) || bytes.Contains(man, []byte("level")) {
		t.Fatal("manifest contains file contents")
	}
	var mf Manifest
	if err := json.Unmarshal(man, &mf); err != nil || len(mf.Files) != 1 || mf.Files[0].SHA256 != f.SHA256 || (unixModes && mf.Files[0].Mode != "0640") {
		t.Fatalf("manifest = %s (%v)", man, err)
	}
	var raw map[string]any
	_ = json.Unmarshal(man, &raw)
	for _, fe := range raw["files"].([]any) {
		for k := range fe.(map[string]any) {
			switch k {
			case "path", "sha256", "uid", "gid", "mode":
			default:
				t.Errorf("manifest has unexpected key %q", k)
			}
		}
	}
}

func TestInsecureBackupRoots(t *testing.T) {
	foreign := 424242
	tests := []struct {
		name  string
		setup func(t *testing.T, dir string) Options
		fix   string
	}{
		{name: "group readable", setup: func(t *testing.T, dir string) Options {
			mustMkdir(t, dir, 0o750)
			return Options{BackupDir: dir}
		}, fix: "chmod 700"},
		{name: "world readable", setup: func(t *testing.T, dir string) Options {
			mustMkdir(t, dir, 0o705)
			return Options{BackupDir: dir}
		}, fix: "chmod 700"},
		{name: "symlink", setup: func(t *testing.T, dir string) Options {
			real := dir + "-real"
			mustMkdir(t, real, 0o700)
			if err := os.Symlink(real, dir); err != nil {
				t.Fatal(err)
			}
			return Options{BackupDir: dir}
		}, fix: "mkdir -m 700"},
		{name: "foreign owner", setup: func(t *testing.T, dir string) Options {
			mustMkdir(t, dir, 0o700)
			return Options{BackupDir: dir, UID: &foreign}
		}, fix: "chown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.name != "symlink" {
				skipWithoutUnixModes(t, "mode bits and ownership of the backup root")
			}
			dir := filepath.Join(t.TempDir(), "backups")
			e := newEnv(t, tt.setup(t, dir))
			f, _ := e.m.Open(host.FileConfig, "/etc/headscale/config.yaml")
			c, _ := e.m.Plan(context.Background(), f, []byte("x: 1\n"))
			_, err := e.m.Apply(context.Background(), c)
			var ib *host.ErrInsecureBackupDir
			if !errors.As(err, &ib) {
				t.Fatalf("err = %v, want ErrInsecureBackupDir", err)
			}
			if !strings.Contains(ib.FixCommand, tt.fix) {
				t.Errorf("fix = %q", ib.FixCommand)
			}
			got, _ := os.ReadFile(e.target())
			if string(got) != original {
				t.Error("target written despite insecure backup root")
			}
		})
	}
}

func mustMkdir(t *testing.T, dir string, mode fs.FileMode) {
	t.Helper()
	if err := os.MkdirAll(dir, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, mode); err != nil {
		t.Fatal(err)
	}
}

func TestRetention(t *testing.T) {
	e := newEnv(t, Options{Keep: 3})
	for i := 0; i < 6; i++ {
		f, err := e.m.Open(host.FileConfig, "/etc/headscale/config.yaml")
		if err != nil {
			t.Fatal(err)
		}
		c, _ := e.m.Plan(context.Background(), f, []byte(strings.Repeat("x", i+1)+"\n"))
		if _, err := e.m.Apply(context.Background(), c); err != nil {
			t.Fatal(err)
		}
	}
	list, err := e.m.Backups("/etc/headscale/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("kept %d backups, want 3", len(list))
	}
	ents, _ := os.ReadDir(e.backups)
	if len(ents) != 3 {
		t.Errorf("%d backup dirs remain, want 3", len(ents))
	}
}

func TestExternalValidatorRunsOnTempCopy(t *testing.T) {
	var seen string
	var seenData []byte
	e := newEnv(t, Options{External: map[host.FileKind]ExternalFunc{
		host.FileConfig: func(ctx context.Context, tmp string) ([]host.Issue, error) {
			seen = tmp
			seenData, _ = os.ReadFile(tmp)
			return []host.Issue{{Severity: host.SevError, Message: "configtest failed", Source: "configtest"}}, nil
		},
	}})
	f, _ := e.m.Open(host.FileConfig, "/etc/headscale/config.yaml")
	c, _ := e.m.Plan(context.Background(), f, []byte("next: 1\n"))
	if seen == "" || seen == e.target() || string(seenData) != "next: 1\n" {
		t.Fatalf("validator saw %q (%q)", seen, seenData)
	}
	if filepath.Dir(seen) != filepath.Dir(e.target()) {
		t.Errorf("temp copy not next to target: %s", seen)
	}
	if _, err := os.Stat(seen); !errors.Is(err, fs.ErrNotExist) {
		t.Error("temp copy not removed")
	}
	if c.Valid() {
		t.Fatal("change should be invalid")
	}
	if _, err := e.m.Apply(context.Background(), c); err == nil {
		t.Fatal("apply accepted an invalid change")
	}
	got, _ := os.ReadFile(e.target())
	if string(got) != original {
		t.Fatal("target modified")
	}
}

func TestGroupPreserved(t *testing.T) {
	groups, _ := os.Getgroups()
	e := newEnv(t, Options{})
	fi, _ := os.Stat(e.target())
	_, gid, ok := Owner(fi)
	if !ok {
		t.Skip("no ownership on this platform")
	}
	other := -1
	for _, g := range groups {
		if g != gid {
			other = g
			break
		}
	}
	if other < 0 || os.Chown(e.target(), -1, other) != nil {
		t.Skip("no secondary group to test with")
	}
	f, _ := e.m.Open(host.FileConfig, "/etc/headscale/config.yaml")
	c, _ := e.m.Plan(context.Background(), f, []byte("a: 2\n"))
	if _, err := e.m.Apply(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	fi, _ = os.Stat(e.target())
	if _, g, _ := Owner(fi); g != other {
		t.Errorf("gid = %d, want %d", g, other)
	}
}

func TestAllowed(t *testing.T) {
	tests := []struct {
		id   Ident
		mode fs.FileMode
		uid  int
		gid  int
		want AccessMode
		ok   bool
	}{
		{Ident{UID: 0}, 0o600, 1, 1, AccessRead | AccessWrite, true},
		{Ident{UID: 1000, GIDs: []int{1000}}, 0o644, 0, 0, AccessRead, true},
		{Ident{UID: 1000, GIDs: []int{1000}}, 0o644, 0, 0, AccessWrite, false},
		{Ident{UID: 1000, GIDs: []int{999}}, 0o660, 0, 999, AccessWrite, true},
		{Ident{UID: 1000, GIDs: []int{1000}}, 0o750, 0, 999, AccessRead, false},
		{Ident{UID: 999}, 0o600, 999, 999, AccessWrite, true},
	}
	for i, tt := range tests {
		if got := Allowed(tt.id, tt.mode, tt.uid, tt.gid, tt.want); got != tt.ok {
			t.Errorf("%d: got %v want %v", i, got, tt.ok)
		}
	}
}

func TestBackupRel(t *testing.T) {
	tests := map[string]string{
		"/etc/headscale/config.yaml": filepath.FromSlash("etc/headscale/config.yaml"),
		"etc/../etc/x":               "etc" + string(filepath.Separator) + "x",
	}
	if runtime.GOOS == "windows" {
		tests[`C:\Users\x\etc\config.yaml`] = `Users\x\etc\config.yaml`
	}
	for in, want := range tests {
		if got := backupRel(in); got != want {
			t.Errorf("backupRel(%q) = %q, want %q", in, got, want)
		}
	}
}
