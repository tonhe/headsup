package backup_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tonhe/headsup/internal/activity"
	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/host/backup"
	"github.com/tonhe/headsup/internal/host/fake"
	"github.com/tonhe/headsup/internal/host/local"
)

const (
	noiseKey   = "privkey:6e6f6973652d7365637265742d6b65792d62797465732d303132333435363738"
	derpKey    = "privkey:646572702d7365637265742d6b65792d62797465732d6162636465663031"
	oidcSecret = "oidc-client-SECRET-7f3a9c"
	pgPass     = "pg-SECRET-pass-91"
)

type rig struct {
	t      *testing.T
	dir    string
	env    host.Env
	h      *local.Host
	root   string
	paths  map[string]string // kind -> path
	orig   map[string][]byte // path -> bytes
	now    time.Time
	dbPath string
}

func skipWindows(t *testing.T, why string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Unix file modes and ownership: " + why)
	}
}

func write(t *testing.T, p string, b []byte, mode fs.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, mode); err != nil {
		t.Fatal(err)
	}
}

func newRig(t *testing.T, wal bool) *rig {
	t.Helper()
	dir := t.TempDir()
	etc := filepath.Join(dir, "etc", "headscale")
	lib := filepath.Join(dir, "var", "lib", "headscale")
	r := &rig{t: t, dir: dir, now: time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC),
		paths: map[string]string{
			backup.KindConfig:       filepath.Join(etc, "config.yaml"),
			backup.KindPolicy:       filepath.Join(etc, "policy.hujson"),
			backup.KindExtraRecords: filepath.Join(lib, "extra-records.json"),
			backup.KindDERPMap:      filepath.Join(etc, "derp.yaml"),
			backup.KindNoiseKey:     filepath.Join(lib, "noise_private.key"),
			backup.KindDERPKey:      filepath.Join(lib, "derp_server_private.key"),
		},
		orig: map[string][]byte{},
	}
	r.dbPath = filepath.Join(lib, "db.sqlite")
	yq := func(s string) string { return `"` + filepath.ToSlash(s) + `"` }
	cfg := strings.Join([]string{
		"server_url: https://hs.example.com",
		"listen_addr: 127.0.0.1:8080",
		"noise:",
		"  private_key_path: " + yq(r.paths[backup.KindNoiseKey]),
		"derp:",
		"  server:",
		"    enabled: false",
		"    private_key_path: " + yq(r.paths[backup.KindDERPKey]),
		"  paths:",
		"    - " + yq(r.paths[backup.KindDERPMap]),
		"database:",
		"  type: sqlite",
		"  sqlite:",
		"    path: " + yq(r.dbPath),
		"policy:",
		"  mode: file",
		"  path: " + yq(r.paths[backup.KindPolicy]),
		"dns:",
		"  base_domain: tail.example.com",
		"  extra_records_path: " + yq(r.paths[backup.KindExtraRecords]),
		"oidc:",
		"  issuer: https://id.example.com",
		"  client_id: headscale",
		"  client_secret: " + oidcSecret,
		"",
	}, "\n")
	contents := map[string]string{
		backup.KindConfig:       cfg,
		backup.KindPolicy:       "{\n  // allow all\n  \"acls\": [{\"action\": \"accept\", \"src\": [\"*\"], \"dst\": [\"*:*\"]}]\n}\n",
		backup.KindExtraRecords: `[{"name": "grafana.tail.example.com", "type": "A", "value": "100.64.0.3"}]` + "\n",
		backup.KindDERPMap:      "regions:\n  900:\n    regionid: 900\n    regioncode: home\n",
		backup.KindNoiseKey:     noiseKey + "\n",
		backup.KindDERPKey:      derpKey + "\n",
	}
	for k, p := range r.paths {
		mode := fs.FileMode(0o640)
		if strings.HasSuffix(k, "-key") {
			mode = 0o600
		}
		write(t, p, []byte(contents[k]), mode)
		r.orig[p] = []byte(contents[k])
	}
	makeDB(t, r.dbPath, wal)
	r.env = host.Env{
		Hostname: "hs", ConfigPath: r.paths[backup.KindConfig], Version: "",
		DB: host.DBSQLite, DBPath: r.dbPath, PolicyMode: "file",
		PolicyPath: r.paths[backup.KindPolicy], RecordsPath: r.paths[backup.KindExtraRecords],
		ServerURL: "https://hs.example.com",
	}
	state := filepath.Join(dir, "state")
	r.h = local.New(r.env, local.Options{BackupDir: filepath.Join(state, "backups"), Now: r.clock})
	r.root = backup.DefaultRoot(filepath.Join(state, "backups"))
	return r
}

func (r *rig) clock() time.Time { return r.now }

func (r *rig) opts() backup.Options {
	return backup.Options{Root: r.root, Now: r.clock, HeadsupVersion: "test"}
}

func makeDB(t *testing.T, p string, wal bool) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	w, err := sql.Open("sqlite", "file:"+filepath.ToSlash(p))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	stmts := []string{
		"CREATE TABLE migrations (id VARCHAR(255) PRIMARY KEY)",
		"INSERT INTO migrations VALUES ('202312101416')",
		"CREATE TABLE nodes (id integer PRIMARY KEY, hostname text, host_info text, endpoints text)",
		"INSERT INTO nodes (id, hostname) VALUES (1, 'laptop'), (2, 'phone'), (3, 'router')",
	}
	if wal {
		stmts = append([]string{"PRAGMA journal_mode=WAL"}, stmts...)
	}
	for _, s := range stmts {
		if _, err := w.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
}

func sum(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func fileSum(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return sum(b)
}

func TestBundleContentsAndHashes(t *testing.T) {
	r := newRig(t, false)
	res, err := backup.Backup(context.Background(), r.h, r.opts())
	if err != nil {
		t.Fatal(err)
	}
	m := res.Manifest
	if m.ID != "20261028T120000Z" || res.Dir != filepath.Join(r.root, m.ID) {
		t.Errorf("id %q dir %q", m.ID, res.Dir)
	}
	kinds := map[string]backup.ManifestFile{}
	for _, f := range m.Files {
		kinds[f.Kind] = f
	}
	for k, p := range r.paths {
		f, ok := kinds[k]
		if !ok {
			t.Errorf("bundle lacks %s (%s)", k, p)
			continue
		}
		// Compare separator-insensitively: on Windows the key and DERP paths
		// are recorded in slash form, the rest natively.
		if filepath.ToSlash(f.Path) != filepath.ToSlash(p) || f.SHA256 != sum(r.orig[p]) {
			t.Errorf("%s: manifest %+v, want path %s hash %s", k, f, p, sum(r.orig[p]))
		}
		if runtime.GOOS != "windows" {
			want := "0640"
			if strings.HasSuffix(k, "-key") {
				want = "0600"
			}
			if f.Mode != want || f.UID != os.Getuid() {
				t.Errorf("%s: mode %s uid %d", k, f.Mode, f.UID)
			}
		}
	}
	if len(m.Missing) != 0 {
		t.Errorf("missing: %v", m.Missing)
	}
	if m.DB == nil || m.DB.Method != backup.MethodSQLiteBackupAPI || m.DB.Path != r.dbPath {
		t.Fatalf("db entry: %+v", m.DB)
	}
	// The copy is a working database with the same rows.
	cp := filepath.Join(res.Dir, "db", "db.sqlite")
	if fileSum(t, cp) != m.DB.SHA256 {
		t.Error("db hash mismatch")
	}
	c, err := sql.Open("sqlite", "file:"+filepath.ToSlash(cp)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var n int
	if err := c.QueryRow("SELECT count(*) FROM nodes").Scan(&n); err != nil || n != 3 {
		t.Errorf("copy rows %d, %v", n, err)
	}
	if len(res.DBSteps) == 0 || !strings.Contains(strings.Join(res.DBSteps, "\n"), "systemctl stop headscale") {
		t.Errorf("db steps: %v", res.DBSteps)
	}
	if _, err := os.Stat(filepath.Join(res.Dir, "DATABASE.txt")); err != nil {
		t.Error("bundle should carry the database steps")
	}
	if _, err := backup.Verify(res.Dir); err != nil {
		t.Errorf("verify: %v", err)
	}
	if !strings.Contains(res.Summary(), "6 files + database") {
		t.Errorf("summary %q", res.Summary())
	}
	bs, err := backup.List(r.root)
	if err != nil || len(bs) != 1 || bs[0].Files != 6 || bs[0].DB != backup.MethodSQLiteBackupAPI {
		t.Errorf("list: %+v %v", bs, err)
	}
}

func dbState(t *testing.T, p string) (string, time.Time) {
	t.Helper()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fileSum(t, p), fi.ModTime()
}

func TestDatabaseNeverWritten(t *testing.T) {
	for _, wal := range []bool{false, true} {
		r := newRig(t, wal)
		h0, m0 := dbState(t, r.dbPath)
		res, err := backup.Backup(context.Background(), r.h, r.opts())
		if err != nil {
			t.Fatalf("wal=%v: %v", wal, err)
		}
		h1, m1 := dbState(t, r.dbPath)
		if h0 != h1 || !m0.Equal(m1) {
			t.Errorf("wal=%v: the live database changed during backup", wal)
		}
		if !wal {
			if _, err := os.Stat(r.dbPath + "-journal"); err == nil {
				t.Error("a rollback journal was created next to the live database")
			}
		}
		// Restore never touches the database either.
		if _, err := backup.Restore(context.Background(), r.h, res.Dir, r.opts()); err != nil {
			t.Fatal(err)
		}
		if h2, m2 := dbState(t, r.dbPath); h2 != h0 || !m2.Equal(m0) {
			t.Errorf("wal=%v: restore wrote the database", wal)
		}
	}
}

func TestModes(t *testing.T) {
	skipWindows(t, "0700/0600 are POSIX permissions")
	r := newRig(t, false)
	res, err := backup.Backup(context.Background(), r.h, r.opts())
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{filepath.Dir(r.root), r.root} {
		fi, err := os.Stat(d)
		if err != nil || fi.Mode().Perm() != 0o700 {
			t.Errorf("%s: %v %v", d, fi.Mode(), err)
		}
	}
	n := 0
	err = filepath.WalkDir(res.Dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, err := os.Lstat(p)
		if err != nil {
			return err
		}
		want := fs.FileMode(0o600)
		if d.IsDir() {
			want = 0o700
		}
		if fi.Mode().Perm() != want {
			t.Errorf("%s: mode %v, want %v", p, fi.Mode().Perm(), want)
		}
		n++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n < 10 {
		t.Errorf("walked only %d entries", n)
	}
}

func TestRestoreByteExact(t *testing.T) {
	r := newRig(t, false)
	res, err := backup.Backup(context.Background(), r.h, r.opts())
	if err != nil {
		t.Fatal(err)
	}
	// Change some files after the backup (still valid, so the pipeline accepts the restore).
	cfg := r.paths[backup.KindConfig]
	write(t, cfg, bytes.Replace(r.orig[cfg], []byte("listen_addr: 127.0.0.1:8080"), []byte("listen_addr: 127.0.0.1:9999"), 1), 0o640)
	pol := r.paths[backup.KindPolicy]
	write(t, pol, []byte(`{"acls": []}`), 0o640)
	nk := r.paths[backup.KindNoiseKey]
	write(t, nk, []byte("privkey:00\n"), 0o600)

	r.now = r.now.Add(time.Minute)
	out, err := backup.Restore(context.Background(), r.h, res.Dir, r.opts())
	if err != nil {
		t.Fatal(err)
	}
	if !out.OK() {
		t.Fatalf("restore failures: %v", out.Failed)
	}
	for p, want := range r.orig {
		got, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s not restored byte-exact:\n%s\nwant\n%s", p, got, want)
		}
	}
	if len(out.Restored) != 3 || len(out.Unchanged) != 3 {
		t.Errorf("restored %v unchanged %v", out.Restored, out.Unchanged)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(nk); fi.Mode().Perm() != 0o600 {
			t.Errorf("key mode %v", fi.Mode().Perm())
		}
		if fi, _ := os.Stat(cfg); fi.Mode().Perm() != 0o640 {
			t.Errorf("config mode %v", fi.Mode().Perm())
		}
	}
	if len(out.DBSteps) == 0 || !strings.Contains(strings.Join(out.DBSteps, "\n"), r.dbPath) {
		t.Errorf("database restore must be given as steps: %v", out.DBSteps)
	}
}

func TestRestoreRefusesTamperedBundle(t *testing.T) {
	r := newRig(t, false)
	res, err := backup.Backup(context.Background(), r.h, r.opts())
	if err != nil {
		t.Fatal(err)
	}
	cfgCopy := filepath.Join(res.Dir, "files", filepath.FromSlash(strings.TrimLeft(filepath.ToSlash(strings.TrimPrefix(r.paths[backup.KindConfig], filepath.VolumeName(r.paths[backup.KindConfig]))), "/")))
	if err := os.WriteFile(cfgCopy, []byte("server_url: https://evil.example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := backup.Restore(context.Background(), r.h, res.Dir, r.opts()); err == nil || !strings.Contains(err.Error(), "manifest hash") {
		t.Fatalf("tampered bundle restored: %v", err)
	}
	if got, _ := os.ReadFile(r.paths[backup.KindConfig]); !bytes.Equal(got, r.orig[r.paths[backup.KindConfig]]) {
		t.Error("live config touched")
	}
}

func TestRestoreReportsInvalidAndMissing(t *testing.T) {
	r := newRig(t, false)
	res, err := backup.Backup(context.Background(), r.h, r.opts())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(r.paths[backup.KindDERPMap]); err != nil {
		t.Fatal(err)
	}
	write(t, r.paths[backup.KindExtraRecords], []byte("[]\n"), 0o640)
	out, err := backup.Restore(context.Background(), r.h, res.Dir, r.opts())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out.Failed[r.paths[backup.KindDERPMap]]; !ok || out.OK() {
		t.Errorf("a vanished managed file should be reported: %+v", out)
	}
	if got, _ := os.ReadFile(r.paths[backup.KindExtraRecords]); !bytes.Equal(got, r.orig[r.paths[backup.KindExtraRecords]]) {
		t.Error("records not restored")
	}
}

func TestInsecureRootsRefused(t *testing.T) {
	skipWindows(t, "group/other bits, symlinks and owners")
	ctx := context.Background()

	t.Run("0755", func(t *testing.T) {
		r := newRig(t, false)
		if err := os.MkdirAll(r.root, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(r.root, 0o755); err != nil {
			t.Fatal(err)
		}
		_, err := backup.Backup(ctx, r.h, r.opts())
		var ie *host.ErrInsecureBackupDir
		if !errors.As(err, &ie) || ie.FixCommand != "chmod 700 "+r.root {
			t.Fatalf("0755 root: %v", err)
		}
		if es, _ := os.ReadDir(r.root); len(es) != 0 {
			t.Errorf("wrote into an insecure root: %v", es)
		}
	})
	t.Run("parent 0755", func(t *testing.T) {
		r := newRig(t, false)
		if err := os.MkdirAll(r.root, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(filepath.Dir(r.root), 0o755); err != nil {
			t.Fatal(err)
		}
		var ie *host.ErrInsecureBackupDir
		if _, err := backup.Backup(ctx, r.h, r.opts()); !errors.As(err, &ie) {
			t.Fatalf("insecure parent: %v", err)
		}
	})
	t.Run("symlink", func(t *testing.T) {
		r := newRig(t, false)
		real := filepath.Join(r.dir, "elsewhere")
		if err := os.MkdirAll(real, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(r.root), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(real, r.root); err != nil {
			t.Fatal(err)
		}
		_, err := backup.Backup(ctx, r.h, r.opts())
		var ie *host.ErrInsecureBackupDir
		if !errors.As(err, &ie) || !strings.Contains(ie.FixCommand, "mkdir -m 700") {
			t.Fatalf("symlinked root: %v", err)
		}
		if es, _ := os.ReadDir(real); len(es) != 0 {
			t.Errorf("followed the symlink: %v", es)
		}
	})
	t.Run("foreign owner", func(t *testing.T) {
		r := newRig(t, false)
		o := r.opts()
		other := os.Getuid() + 1 // the root is ours, so for this check it belongs to someone else
		o.UID = &other
		_, err := backup.Backup(ctx, r.h, o)
		var ie *host.ErrInsecureBackupDir
		if !errors.As(err, &ie) || !strings.Contains(ie.FixCommand, "chown") {
			t.Fatalf("foreign owner: %v", err)
		}
		if err := backup.CheckRoot(r.opts()); err != nil {
			t.Errorf("own root refused: %v", err)
		}
	})
}

func TestNoSecretsInManifestOrActivityLog(t *testing.T) {
	r := newRig(t, false)
	res, err := backup.Backup(context.Background(), r.h, r.opts())
	if err != nil {
		t.Fatal(err)
	}
	man, err := os.ReadFile(filepath.Join(res.Dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	dbtxt, _ := os.ReadFile(filepath.Join(res.Dir, "DATABASE.txt"))

	// Log the backup the way the app does (action, targets, result), plus a
	// failed backup whose error the log records.
	logDir := filepath.Join(r.dir, "activity")
	al, err := activity.Open(logDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := al.Log(activity.Entry{Time: r.now, ActionID: "server.backup", BackupID: res.Manifest.ID,
		Targets: []activity.Target{{Kind: "backup", ID: res.Manifest.ID, Name: res.Dir}},
		Params:  map[string]any{"summary": res.Summary()}, Result: activity.OK()}); err != nil {
		t.Fatal(err)
	}
	o := r.opts()
	bad := os.Getuid() + 1
	o.UID = &bad
	_, berr := backup.Backup(context.Background(), r.h, o)
	if berr == nil && runtime.GOOS != "windows" {
		t.Fatal("expected a failure")
	}
	if berr != nil {
		_ = al.Log(activity.Entry{Time: r.now, ActionID: "server.backup", Result: activity.Err("permission", berr.Error())})
	}
	logged, err := os.ReadFile(filepath.Join(logDir, activity.FileName))
	if err != nil {
		t.Fatal(err)
	}
	secrets := []string{noiseKey, strings.TrimPrefix(noiseKey, "privkey:"), derpKey, oidcSecret}
	for name, b := range map[string][]byte{"manifest": man, "activity log": logged, "summary": []byte(res.Summary()), "database steps": dbtxt} {
		for _, s := range secrets {
			if bytes.Contains(b, []byte(s)) {
				t.Errorf("%s contains a secret (%s…)", name, s[:12])
			}
		}
	}
	// The bundle itself does hold the key (it is a backup), privately.
	if b, _ := os.ReadFile(filepath.Join(res.Dir, "files", strings.TrimLeft(filepath.ToSlash(strings.TrimPrefix(r.paths[backup.KindNoiseKey], filepath.VolumeName(r.paths[backup.KindNoiseKey]))), "/"))); !bytes.Contains(b, []byte(noiseKey)) {
		t.Error("bundle should contain the noise key copy")
	}
}

func TestRetention(t *testing.T) {
	r := newRig(t, false)
	o := r.opts()
	o.Keep = 2
	var ids []string
	for range 4 {
		res, err := backup.Backup(context.Background(), r.h, o)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, res.Manifest.ID)
		r.now = r.now.Add(time.Hour)
	}
	bs, err := backup.List(r.root)
	if err != nil || len(bs) != 2 || bs[0].ID != ids[3] || bs[1].ID != ids[2] {
		t.Fatalf("retention kept %+v (%v), want %v", bs, err, ids[2:])
	}
	// Same-second bundles get a suffix rather than colliding.
	r.now = r.now.Add(-time.Hour)
	res, err := backup.Backup(context.Background(), r.h, o)
	if err != nil || !strings.HasSuffix(res.Manifest.ID, "-2") {
		t.Errorf("collision id %q %v", res.Manifest.ID, err)
	}
	// Unrelated entries in the root are left alone.
	if err := os.WriteFile(filepath.Join(r.root, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if bs, _ := backup.List(r.root); len(bs) != 2 {
		t.Errorf("list %+v", bs)
	}
	if bs, err := backup.List(filepath.Join(r.dir, "nope")); err != nil || bs != nil {
		t.Errorf("missing root: %v %v", bs, err)
	}
}

func TestMissingFilesRecorded(t *testing.T) {
	r := newRig(t, false)
	if err := os.Remove(r.paths[backup.KindDERPKey]); err != nil {
		t.Fatal(err)
	}
	res, err := backup.Backup(context.Background(), r.h, r.opts())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Manifest.Missing) != 1 || res.Manifest.Missing[0] != r.paths[backup.KindDERPKey] || len(res.Manifest.Files) != 5 {
		t.Errorf("missing %v files %d", res.Manifest.Missing, len(res.Manifest.Files))
	}
}

func TestPostgresGivesDumpSteps(t *testing.T) {
	r := newRig(t, false)
	cfg := r.paths[backup.KindConfig]
	pg := strings.Replace(string(r.orig[cfg]), "  type: sqlite", "  type: postgres\n  postgres:\n    host: db.internal\n    port: 5433\n    name: hs\n    user: hsadmin\n    pass: "+pgPass, 1)
	write(t, cfg, []byte(pg), 0o640)
	env := r.env
	env.DB, env.DBPath = host.DBPostgres, "postgres://hsadmin:"+pgPass+"@db.internal:5433/hs"
	h := local.New(env, local.Options{BackupDir: filepath.Join(r.dir, "state", "backups")})
	res, err := backup.Backup(context.Background(), h, r.opts())
	if err != nil {
		t.Fatal(err)
	}
	if res.Manifest.DB == nil || res.Manifest.DB.Method != backup.MethodPgDump || res.Manifest.DB.Path != "" {
		t.Fatalf("db %+v", res.Manifest.DB)
	}
	steps := strings.Join(res.DBSteps, "\n")
	if !strings.Contains(steps, "pg_dump -h db.internal -p 5433 -U hsadmin") || strings.Contains(steps, pgPass) {
		t.Errorf("steps %s", steps)
	}
	man, _ := os.ReadFile(filepath.Join(res.Dir, "manifest.json"))
	if bytes.Contains(man, []byte(pgPass)) {
		t.Error("manifest leaks the Postgres password")
	}
	if !strings.Contains(res.Summary(), "Postgres") {
		t.Errorf("summary %q", res.Summary())
	}
	out, err := backup.Restore(context.Background(), h, res.Dir, r.opts())
	if err != nil || !strings.Contains(strings.Join(out.DBSteps, "\n"), "pg_restore") {
		t.Errorf("restore steps %v %v", out.DBSteps, err)
	}
}

func TestSimulatedHost(t *testing.T) {
	h := fake.New()
	root := filepath.Join(t.TempDir(), "b", "bundles")
	o := backup.Options{Root: root, DB: backup.SimulatedDB, Simulated: true}
	res, err := backup.Backup(context.Background(), h, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Manifest.Files) != 3 || res.Manifest.DB.Method != backup.MethodSimulated {
		t.Errorf("simulated bundle: %+v", res.Manifest)
	}
	// The fake's keys are not present, so they are recorded as missing.
	if len(res.Manifest.Missing) != 2 {
		t.Errorf("missing %v", res.Manifest.Missing)
	}
	h.ChangeOnDisk(fake.PolicyPath, []byte(`{"acls": []}`))
	out, err := backup.Restore(context.Background(), h, res.Dir, o)
	if err != nil || !out.OK() || len(out.Restored) != 1 {
		t.Fatalf("restore %+v %v", out, err)
	}
	if got, _ := h.File(fake.PolicyPath); !bytes.Equal(got, fake.SeedPolicy) {
		t.Error("policy not restored")
	}
	if len(out.DBSteps) != 0 {
		t.Errorf("no db steps for a simulated db: %v", out.DBSteps)
	}
}

func TestPermissionFailureLeavesNoPartialBundle(t *testing.T) {
	h := fake.New()
	h.FailPermission(host.NeedConfigR, "")
	root := filepath.Join(t.TempDir(), "b", "bundles")
	_, err := backup.Backup(context.Background(), h, backup.Options{Root: root, DB: backup.SimulatedDB})
	var pe *host.ErrPermission
	if !errors.As(err, &pe) {
		t.Fatalf("want a permission error, got %v", err)
	}
	if es, _ := os.ReadDir(root); len(es) != 0 {
		t.Errorf("partial bundle left behind: %v", es)
	}
}

func TestSQLiteOnlineErrors(t *testing.T) {
	dir := t.TempDir()
	if _, err := backup.SQLiteOnline(context.Background(), host.Env{DBPath: filepath.Join(dir, "none.sqlite")}, filepath.Join(dir, "out")); err == nil {
		t.Error("missing database should fail")
	}
	if _, err := backup.SQLiteOnline(context.Background(), host.Env{DBPath: dir}, filepath.Join(dir, "out")); err == nil {
		t.Error("a directory is not a database")
	}
	p := filepath.Join(dir, "db.sqlite")
	makeDB(t, p, false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := backup.SQLiteOnline(ctx, host.Env{DBPath: p}, filepath.Join(dir, "out")); err == nil {
		t.Error("cancelled copy should fail")
	}
}

func TestDefaultRoot(t *testing.T) {
	if got := backup.DefaultRoot("/srv/b"); got != filepath.Join("/srv/b", "bundles") {
		t.Errorf("DefaultRoot = %q", got)
	}
	if got := backup.DefaultRoot(""); !strings.HasSuffix(got, filepath.Join("backups", "bundles")) {
		t.Errorf("DefaultRoot(\"\") = %q", got)
	}
}
