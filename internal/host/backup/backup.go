// Package backup takes one-key snapshot bundles of a headscale host: the
// config, policy file, extra records, DERP map files, the noise and DERP
// private keys, and a consistent copy of the SQLite database made with
// SQLite's online backup API (Postgres gets pg_dump instructions instead).
//
// Bundles hold secrets, so (FR-064) every directory is created 0700 and
// every file 0600 under a 0077 umask, an insecure root is refused with
// host.ErrInsecureBackupDir, and the manifest records only paths, hashes,
// ownership and mode. Restore puts files back through the host's managed
// file pipeline; the database is never written, and its restore is given
// as steps for the admin to run.
package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/tonhe/headsup/internal/brand"
	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/host/files"
	"github.com/tonhe/headsup/internal/host/hsconfig"
)

// Kinds of file in a bundle.
const (
	KindConfig       = "config"
	KindPolicy       = "policy"
	KindExtraRecords = "extra-records"
	KindDERPMap      = "derp-map"
	KindNoiseKey     = "noise-key"
	KindDERPKey      = "derp-key"
)

// DB copy methods recorded in the manifest.
const (
	MethodSQLiteBackupAPI = "sqlite-backup-api"
	MethodPgDump          = "pg_dump-instructions"
	MethodSimulated       = "simulated"
)

// ManifestFile is one backed-up file: never its contents.
type ManifestFile struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	SHA256 string `json:"sha256"`
	UID    int    `json:"uid"`
	GID    int    `json:"gid"`
	Mode   string `json:"mode"`
}

// ManifestDB describes the database copy.
type ManifestDB struct {
	Kind   string `json:"kind"`             // sqlite | postgres
	Path   string `json:"path,omitempty"`   // the live SQLite path; empty for Postgres (a DSN may hold a password)
	SHA256 string `json:"sha256,omitempty"` // of the copy in the bundle
	Method string `json:"method"`
}

// Manifest is manifest.json in a bundle (FR-064: paths, hashes, ownership
// and mode only).
type Manifest struct {
	ID               string         `json:"id"`
	Created          time.Time      `json:"created"`
	HeadscaleVersion string         `json:"headscale_version,omitempty"`
	HeadsupVersion   string         `json:"headsup_version,omitempty"`
	Files            []ManifestFile `json:"files"`
	Missing          []string       `json:"missing,omitempty"` // configured paths that did not exist
	DB               *ManifestDB    `json:"db,omitempty"`
}

// DBCopier writes a consistent copy of the live database to dst (a new,
// empty 0600 file) and returns the method used. It must never write src.
type DBCopier func(ctx context.Context, env host.Env, dst string) (method string, err error)

// Options configures Backup and Restore.
type Options struct {
	// Root is the bundle directory; default DefaultRoot().
	Root string
	// Keep is how many bundles retention keeps; default 10.
	Keep int
	// UID must own Root; nil means this process, -1 skips the check.
	UID *int
	Now func() time.Time
	// HeadsupVersion is recorded in the manifest.
	HeadsupVersion string
	// DB copies the database; default SQLiteOnline.
	DB DBCopier
	// Simulated marks the in-memory demo host: restore never writes the
	// real filesystem for files outside the host's managed pipeline.
	Simulated bool
}

// DefaultKeep is the default bundle retention.
const DefaultKeep = 10

// DefaultRoot is <backup dir>/bundles, beside the per-write backups.
func DefaultRoot(backupDir string) string {
	if backupDir == "" {
		backupDir = files.DefaultBackupDir()
	}
	return filepath.Join(backupDir, "bundles")
}

// DemoRoot is a private, per-user bundle root under the system temp
// directory for the simulated demo host, so demos never write the real
// backup directory.
func DemoRoot() string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("%s-demo-%d", brand.Name, os.Getuid()), "bundles")
}

func (o Options) withDefaults() Options {
	if o.Root == "" {
		o.Root = DefaultRoot("")
	}
	if o.Keep <= 0 {
		o.Keep = DefaultKeep
	}
	if o.UID == nil {
		u := files.CurrentUID()
		o.UID = &u
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.DB == nil {
		o.DB = SQLiteOnline
	}
	return o
}

// Result is what Backup did.
type Result struct {
	Manifest Manifest
	Dir      string
	// DBSteps are instructions for the database when it could not be copied
	// directly (Postgres), or how to restore the copy.
	DBSteps []string
	Notes   []string
}

// Summary is a one-line, secret-free description for toasts and logs.
func (r Result) Summary() string {
	s := fmt.Sprintf("Backup %s: %d files", r.Manifest.ID, len(r.Manifest.Files))
	if db := r.Manifest.DB; db != nil {
		switch db.Method {
		case MethodPgDump:
			s += ", Postgres dump steps"
		default:
			s += " + database"
		}
	}
	return s + "."
}

// Target is one file a bundle holds.
type Target struct {
	Kind string
	Path string
}

// Targets lists the files a bundle of h would contain, from the detected
// environment and the config (DERP map files and private keys).
func Targets(h host.Host) []Target {
	env := h.Env()
	var out []Target
	seen := map[string]bool{}
	add := func(kind, p string) {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, Target{Kind: kind, Path: p})
	}
	add(KindConfig, env.ConfigPath)
	var doc *hsconfig.Doc
	if f, err := h.Open(host.FileConfig, ""); err == nil {
		doc, _ = hsconfig.Parse(f.Raw)
	}
	get := func(k string) string {
		if doc == nil {
			return ""
		}
		v, _ := doc.Get(k)
		return resolve(env.ConfigPath, v)
	}
	policy := env.PolicyPath
	if policy == "" {
		policy = get("policy.path")
	}
	if env.PolicyMode != "database" {
		add(KindPolicy, resolve(env.ConfigPath, policy))
	}
	records := env.RecordsPath
	if records == "" {
		records = get("dns.extra_records_path")
	}
	add(KindExtraRecords, resolve(env.ConfigPath, records))
	if doc != nil {
		if ps, ok := doc.List("derp.paths"); ok {
			for _, p := range ps {
				add(KindDERPMap, resolve(env.ConfigPath, p))
			}
		}
	}
	add(KindNoiseKey, get("noise.private_key_path"))
	add(KindDERPKey, get("derp.server.private_key_path"))
	return out
}

func resolve(configPath, p string) string {
	p = strings.TrimSpace(p)
	if p == "" || filepath.IsAbs(p) || strings.HasPrefix(p, "/") || configPath == "" {
		return p
	}
	if strings.HasPrefix(configPath, "/") {
		return path.Join(path.Dir(configPath), p)
	}
	return filepath.Join(filepath.Dir(configPath), p)
}

// rel turns an absolute path into its place under files/ in a bundle.
func rel(p string) string {
	p = strings.TrimPrefix(p, filepath.VolumeName(p))
	p = filepath.ToSlash(p)
	return strings.TrimLeft(path.Clean("/"+p), "/")
}

func fileKind(kind string) host.FileKind {
	switch kind {
	case KindPolicy:
		return host.FilePolicy
	case KindExtraRecords:
		return host.FileExtraRecords
	case KindDERPMap:
		return host.FileDERPMap
	default:
		return host.FileConfig
	}
}

// managed reports whether restore goes through the host's file pipeline.
func managed(kind string) bool {
	switch kind {
	case KindConfig, KindPolicy, KindExtraRecords, KindDERPMap:
		return true
	}
	return false
}

// CheckRoot refuses a bundle root (and the backup directory holding it)
// that is a symlink, group- or world-accessible, or owned by another user.
// Missing directories are created 0700.
func CheckRoot(o Options) error {
	o = o.withDefaults()
	parent := filepath.Dir(o.Root)
	if err := files.CheckPrivateDir(parent, *o.UID, true); err != nil {
		return err
	}
	return files.CheckPrivateDir(o.Root, *o.UID, true)
}

var idPattern = regexp.MustCompile(`^\d{8}T\d{6}Z(-\d+)?$`)

// Backup writes a new bundle and applies retention.
func Backup(ctx context.Context, h host.Host, o Options) (Result, error) {
	o = o.withDefaults()
	restore := privateUmask()
	defer restore()
	if err := CheckRoot(o); err != nil {
		return Result{}, err
	}
	env := h.Env()
	now := o.Now().UTC()
	id := now.Format("20060102T150405Z")
	dir := filepath.Join(o.Root, id)
	for i := 2; ; i++ {
		err := os.Mkdir(dir, 0o700)
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrExist) {
			return Result{}, err
		}
		id = fmt.Sprintf("%s-%d", now.Format("20060102T150405Z"), i)
		dir = filepath.Join(o.Root, id)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return Result{}, err
	}
	res, err := write(ctx, h, env, o, id, dir, now)
	if err != nil {
		_ = os.RemoveAll(dir)
		return Result{}, err
	}
	prune(o.Root, o.Keep)
	return res, nil
}

func write(ctx context.Context, h host.Host, env host.Env, o Options, id, dir string, now time.Time) (Result, error) {
	m := Manifest{ID: id, Created: now, HeadscaleVersion: env.Version, HeadsupVersion: o.HeadsupVersion, Files: []ManifestFile{}}
	res := Result{Dir: dir}
	for _, t := range Targets(h) {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		f, err := h.Open(fileKind(t.Kind), t.Path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				m.Missing = append(m.Missing, filepath.ToSlash(t.Path))
				continue
			}
			return res, fmt.Errorf("back up %s: %w", t.Path, err)
		}
		dst := filepath.Join(dir, "files", filepath.FromSlash(rel(t.Path)))
		if err := files.MkdirPrivate(filepath.Dir(dst)); err != nil {
			return res, err
		}
		if err := files.WritePrivate(dst, f.Raw); err != nil {
			return res, err
		}
		// Manifests hold slash-form paths on every OS, so a bundle reads the
		// same wherever it was made.
		m.Files = append(m.Files, ManifestFile{Path: filepath.ToSlash(t.Path), Kind: t.Kind, SHA256: files.Sum(f.Raw),
			UID: f.UID, GID: f.GID, Mode: fmt.Sprintf("%#o", uint32(f.Mode.Perm()))})
	}

	switch env.DB {
	case host.DBSQLite:
		if env.DBPath == "" {
			res.Notes = append(res.Notes, "no SQLite database path detected; database not copied")
			break
		}
		dbDir := filepath.Join(dir, "db")
		if err := files.MkdirPrivate(dbDir); err != nil {
			return res, err
		}
		dst := filepath.Join(dbDir, filepath.Base(env.DBPath))
		if err := files.WritePrivate(dst, nil); err != nil {
			return res, err
		}
		method, err := o.DB(ctx, env, dst)
		if err != nil {
			return res, fmt.Errorf("copy database: %w", err)
		}
		_ = os.Chmod(dst, 0o600)
		sum, err := hashFile(dst)
		if err != nil {
			return res, err
		}
		m.DB = &ManifestDB{Kind: "sqlite", Path: env.DBPath, SHA256: sum, Method: method}
		if method != MethodSimulated {
			res.DBSteps = sqliteRestoreSteps(env, dst)
		}
	case host.DBPostgres:
		m.DB = &ManifestDB{Kind: "postgres", Method: MethodPgDump}
		res.DBSteps = pgDumpSteps(h, dir)
	}

	mb, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return res, err
	}
	if err := files.WritePrivate(filepath.Join(dir, "manifest.json"), mb); err != nil {
		return res, err
	}
	if len(res.DBSteps) > 0 {
		txt := "Database steps for bundle " + id + " (never run automatically):\n\n" + strings.Join(res.DBSteps, "\n") + "\n"
		if err := files.WritePrivate(filepath.Join(dir, "DATABASE.txt"), []byte(txt)); err != nil {
			return res, err
		}
	}
	res.Manifest = m
	return res, nil
}

func hashFile(p string) (string, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	return files.Sum(b), nil
}

func stopCmd(env host.Env, verb string) string {
	switch env.Service {
	case host.ServiceOpenRC:
		return "sudo rc-service headscale " + verb
	case host.ServiceContainer:
		rt := env.ContainerRuntime
		if rt == "" {
			rt = "docker"
		}
		return rt + " " + verb + " " + env.Container
	default:
		return "sudo systemctl " + verb + " headscale"
	}
}

func sqliteRestoreSteps(env host.Env, copyPath string) []string {
	db := env.DBPath
	return []string{
		"1. Stop headscale: " + stopCmd(env, "stop"),
		"2. Move the live database aside: sudo mv " + db + " " + db + ".before-restore (and any " + db + "-wal / -shm files)",
		"3. Copy the backup in: sudo install -m 0640 -o headscale -g headscale " + copyPath + " " + db,
		"4. Start headscale: " + stopCmd(env, "start"),
	}
}

func pgDumpSteps(h host.Host, dir string) []string {
	dbHost, port, name, user := "localhost", "5432", "headscale", "headscale"
	if f, err := h.Open(host.FileConfig, ""); err == nil {
		if d, err := hsconfig.Parse(f.Raw); err == nil {
			for k, p := range map[string]*string{"database.postgres.host": &dbHost, "database.postgres.port": &port,
				"database.postgres.name": &name, "database.postgres.user": &user} {
				if v, ok := d.Get(k); ok && strings.TrimSpace(v) != "" {
					*p = strings.TrimSpace(v)
				}
			}
		}
	}
	out := filepath.Join(dir, "db", name+".pgdump")
	return []string{
		"Postgres cannot be copied with the SQLite backup API. Dump it yourself (you will be asked for the password):",
		fmt.Sprintf("  (umask 077; pg_dump -h %s -p %s -U %s -Fc -f %s %s)", dbHost, port, user, out, name),
		"To restore: stop headscale, then",
		fmt.Sprintf("  pg_restore -h %s -p %s -U %s -d %s --clean %s", dbHost, port, user, name, out),
	}
}

// --- listing and retention --------------------------------------------------

// Summary of a bundle on disk.
type Bundle struct {
	ID      string
	Dir     string
	Created time.Time
	Files   int
	Missing int
	DB      string // method, "" for none
}

// List returns the bundles under root, newest first. A missing root is empty.
func List(root string) ([]Bundle, error) {
	es, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Bundle
	for _, e := range es {
		if !e.IsDir() || !idPattern.MatchString(e.Name()) {
			continue
		}
		dir := filepath.Join(root, e.Name())
		m, err := ReadManifest(dir)
		if err != nil {
			continue
		}
		b := Bundle{ID: m.ID, Dir: dir, Created: m.Created, Files: len(m.Files), Missing: len(m.Missing)}
		if m.DB != nil {
			b.DB = m.DB.Method
		}
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

// ReadManifest reads dir/manifest.json.
func ReadManifest(dir string) (Manifest, error) {
	var m Manifest
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, fmt.Errorf("%s: %w", dir, err)
	}
	return m, nil
}

func prune(root string, keep int) {
	bs, err := List(root)
	if err != nil || len(bs) <= keep {
		return
	}
	for _, b := range bs[keep:] {
		_ = os.RemoveAll(b.Dir)
	}
}

// Verify checks every file in the bundle against its manifest hash.
func Verify(dir string) (Manifest, error) {
	m, err := ReadManifest(dir)
	if err != nil {
		return m, err
	}
	for _, f := range m.Files {
		b, err := os.ReadFile(filepath.Join(dir, "files", filepath.FromSlash(rel(f.Path))))
		if err != nil {
			return m, err
		}
		if files.Sum(b) != f.SHA256 {
			return m, fmt.Errorf("bundle %s: %s does not match its manifest hash", m.ID, f.Path)
		}
	}
	if m.DB != nil && m.DB.SHA256 != "" && m.DB.Path != "" {
		sum, err := hashFile(filepath.Join(dir, "db", filepath.Base(m.DB.Path)))
		if err != nil {
			return m, err
		}
		if sum != m.DB.SHA256 {
			return m, fmt.Errorf("bundle %s: database copy does not match its manifest hash", m.ID)
		}
	}
	return m, nil
}
