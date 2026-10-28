// Package files is the managed-file pipeline: open with a hash, plan
// (validate on a temporary copy and diff), apply (re-hash, private backup,
// atomic write preserving ownership and mode) and rollback.
package files

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tonhe/fathom/internal/brand"
	"github.com/tonhe/fathom/internal/host"
)

// SchemaFunc validates content in memory and returns issues (Source "schema").
type SchemaFunc func(data []byte) []host.Issue

// ExternalFunc validates a temporary copy of the proposed content, e.g. with
// `headscale configtest -c <tmpPath>`. A returned error means the validator
// could not run; it is reported as a warning, not a failure.
type ExternalFunc func(ctx context.Context, tmpPath string) ([]host.Issue, error)

// DangerFunc rates a proposed change.
type DangerFunc func(kind host.FileKind, old, next []byte) host.Danger

// Options configures a Manager. Zero values pick sensible defaults.
type Options struct {
	Root      string // prefix joined to every target path (tests); "" for the real filesystem
	BackupDir string // private backup root; default $XDG_STATE_HOME/<brand>/backups
	Keep      int    // backups kept per file; default 50
	Now       func() time.Time
	Schema    map[host.FileKind]SchemaFunc
	External  map[host.FileKind]ExternalFunc
	Danger    DangerFunc
	UID       *int // identity that must own the backup root; nil means os.Getuid(); -1 skips the check
	Chown     func(path string, uid, gid int) error
}

// Manager implements the Open/Plan/Apply/Rollback part of host.Host.
type Manager struct {
	o Options
}

// BackupDir is the backup root in use.
func (m *Manager) BackupDir() string { return m.o.BackupDir }

// DefaultEffect is when a write to each kind takes effect (research.md R17).
func DefaultEffect(k host.FileKind) host.Effect {
	switch k {
	case host.FilePolicy:
		return host.EffectReload
	case host.FileExtraRecords:
		return host.EffectLive
	default:
		return host.EffectRestart
	}
}

// DefaultBackupDir is $XDG_STATE_HOME/<brand>/backups (falling back to ~/.local/state).
func DefaultBackupDir() string {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, brand.Name, "backups")
}

// New returns a Manager.
func New(o Options) *Manager {
	if o.Keep <= 0 {
		o.Keep = 50
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.BackupDir == "" {
		o.BackupDir = DefaultBackupDir()
	}
	if o.UID == nil {
		u := currentUID()
		o.UID = &u
	}
	if o.Chown == nil {
		o.Chown = os.Lchown
	}
	return &Manager{o: o}
}

func (m *Manager) real(path string) string {
	if m.o.Root == "" {
		return path
	}
	return filepath.Join(m.o.Root, path)
}

func needFor(k host.FileKind) host.Need {
	switch k {
	case host.FilePolicy:
		return host.NeedPolicyW
	case host.FileExtraRecords:
		return host.NeedRecordsW
	default:
		return host.NeedConfigW
	}
}

func permErr(k host.FileKind, op string) error {
	return &host.ErrPermission{Need: needFor(k), Op: op, FixCommand: "sudo " + brand.Name}
}

// Sum returns the hex sha256 of b.
func Sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// Open reads a managed file and records its hash, owner and mode.
func (m *Manager) Open(kind host.FileKind, path string) (*host.File, error) {
	if path == "" {
		return nil, fmt.Errorf("%s: no path configured", kind)
	}
	rp := m.real(path)
	fi, err := os.Stat(rp)
	if err != nil {
		if os.IsPermission(err) {
			return nil, &host.ErrPermission{Need: host.NeedConfigR, Op: "read " + path, FixCommand: "sudo " + brand.Name}
		}
		return nil, err
	}
	raw, err := os.ReadFile(rp)
	if err != nil {
		if os.IsPermission(err) {
			return nil, &host.ErrPermission{Need: host.NeedConfigR, Op: "read " + path, FixCommand: "sudo " + brand.Name}
		}
		return nil, err
	}
	uid, gid, _ := Owner(fi)
	return &host.File{
		Kind: kind, Path: path, UID: uid, GID: gid, Mode: fi.Mode().Perm(),
		SHA256: Sum(raw), Raw: raw, Effect: DefaultEffect(kind),
	}, nil
}

// Plan validates next (schema, then the external validator on a temporary
// copy next to the target) and diffs it against the opened content. The live
// file is never touched.
func (m *Manager) Plan(ctx context.Context, f *host.File, next []byte) (*host.Change, error) {
	if f == nil {
		return nil, errors.New("plan: nil file")
	}
	c := &host.Change{File: f, Next: next, Effect: f.Effect, Danger: m.danger(f, next)}
	c.Diff = Diff(f.Raw, next)
	if fn := m.o.Schema[f.Kind]; fn != nil {
		c.Issues = append(c.Issues, fn(next)...)
	}
	if ext := m.o.External[f.Kind]; ext != nil && c.Valid() {
		issues, err := m.runExternal(ctx, f, next, ext)
		if err != nil {
			c.Issues = append(c.Issues, host.Issue{Severity: host.SevWarn, Message: "external validator did not run: " + err.Error(), Source: "validator"})
		}
		c.Issues = append(c.Issues, issues...)
	}
	return c, nil
}

func (m *Manager) danger(f *host.File, next []byte) host.Danger {
	if m.o.Danger != nil {
		return m.o.Danger(f.Kind, f.Raw, next)
	}
	switch f.Effect {
	case host.EffectLive:
		return host.DangerNone
	default:
		return host.DangerConfirm
	}
}

func (m *Manager) runExternal(ctx context.Context, f *host.File, next []byte, ext ExternalFunc) ([]host.Issue, error) {
	rp := m.real(f.Path)
	dir := filepath.Dir(rp)
	base := filepath.Base(rp)
	ext2 := filepath.Ext(base)
	tmp, err := os.CreateTemp(dir, "."+strings.TrimSuffix(base, ext2)+"."+brand.Name+"-check-*"+ext2)
	if err != nil {
		// The directory is not writable: validate a private copy instead.
		tmp, err = os.CreateTemp("", brand.Name+"-check-*"+ext2)
		if err != nil {
			return nil, err
		}
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return nil, err
	}
	if _, err := tmp.Write(next); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	return ext(ctx, name)
}

// Apply writes a planned change: it refuses invalid changes, re-checks the
// hash, backs the file up privately and replaces it atomically.
func (m *Manager) Apply(ctx context.Context, c *host.Change) (host.Applied, error) {
	if c == nil || c.File == nil {
		return host.Applied{}, errors.New("apply: nil change")
	}
	if !c.Valid() {
		return host.Applied{}, &host.ErrInvalid{Issues: c.Issues}
	}
	if err := ctx.Err(); err != nil {
		return host.Applied{}, err
	}
	f := c.File
	cur, err := os.ReadFile(m.real(f.Path))
	if err != nil {
		if os.IsPermission(err) {
			return host.Applied{}, permErr(f.Kind, "read "+f.Path)
		}
		return host.Applied{}, err
	}
	if Sum(cur) != f.SHA256 {
		return host.Applied{}, &host.ErrChangedOnDisk{Path: f.Path, Current: cur}
	}
	now := m.o.Now()
	bp, err := m.backup(f, cur, now)
	if err != nil {
		return host.Applied{}, err
	}
	if err := m.writeAtomic(f, c.Next); err != nil {
		return host.Applied{}, err
	}
	return host.Applied{File: f, BackupPath: bp, NewSHA256: Sum(c.Next), At: now}, nil
}

// Rollback restores the pre-write content recorded in a, refusing if the
// file changed again since the write.
func (m *Manager) Rollback(ctx context.Context, a host.Applied) error {
	if a.File == nil {
		return errors.New("rollback: nothing recorded")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	old, err := os.ReadFile(a.BackupPath)
	if err != nil {
		return fmt.Errorf("rollback: read backup: %w", err)
	}
	if Sum(old) != a.File.SHA256 {
		return fmt.Errorf("rollback: backup %s does not match the recorded hash", a.BackupPath)
	}
	cur, err := os.ReadFile(m.real(a.File.Path))
	if err != nil {
		return err
	}
	if a.NewSHA256 != "" && Sum(cur) != a.NewSHA256 {
		return &host.ErrChangedOnDisk{Path: a.File.Path, Current: cur}
	}
	if _, err := m.backup(a.File, cur, m.o.Now()); err != nil {
		return err
	}
	return m.writeAtomic(a.File, old)
}

// CheckBackupRoot refuses a backup root that is a symlink, grants any group
// or other permission, or belongs to another user (FR-064). A missing root is
// created with mode 0700.
func (m *Manager) CheckBackupRoot() error {
	return checkPrivateDir(m.o.BackupDir, *m.o.UID, true)
}

func checkPrivateDir(dir string, uid int, create bool) error {
	fi, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) && create {
		if err := mkdirPrivate(dir); err != nil {
			return err
		}
		fi, err = os.Lstat(dir)
	}
	if err != nil {
		return err
	}
	owner, _, ok := Owner(fi)
	switch {
	case fi.Mode()&fs.ModeSymlink != 0:
		return &host.ErrInsecureBackupDir{Path: dir, Mode: fi.Mode(), Owner: owner,
			FixCommand: fmt.Sprintf("rm %s && mkdir -m 700 %s", dir, dir)}
	case !fi.IsDir():
		return &host.ErrInsecureBackupDir{Path: dir, Mode: fi.Mode(), Owner: owner,
			FixCommand: fmt.Sprintf("mv %s %s.old && mkdir -m 700 %s", dir, dir, dir)}
	case ok && uid >= 0 && owner != uid:
		return &host.ErrInsecureBackupDir{Path: dir, Mode: fi.Mode(), Owner: owner,
			FixCommand: fmt.Sprintf("sudo chown -R $(id -u):$(id -g) %s && chmod 700 %s", dir, dir)}
	case fi.Mode().Perm()&0o077 != 0:
		return &host.ErrInsecureBackupDir{Path: dir, Mode: fi.Mode(), Owner: owner,
			FixCommand: "chmod 700 " + dir}
	}
	return nil
}

// mkdirPrivate creates dir and missing parents with mode 0700, chmod-ing each
// new directory so the umask cannot widen it.
func mkdirPrivate(dir string) error {
	if fi, err := os.Stat(dir); err == nil {
		if !fi.IsDir() {
			return fmt.Errorf("%s is not a directory", dir)
		}
		return nil
	}
	if parent := filepath.Dir(dir); parent != dir {
		if err := mkdirPrivate(parent); err != nil {
			return err
		}
	}
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	return os.Chmod(dir, 0o700)
}

// writePrivate writes a 0600 file, chmod-ing after open to defeat the umask.
func writePrivate(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// ManifestFile is one entry of a backup manifest: never contents or secrets.
type ManifestFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	UID    int    `json:"uid"`
	GID    int    `json:"gid"`
	Mode   string `json:"mode"`
}

// Manifest is the manifest.json in each backup directory.
type Manifest struct {
	ID    string         `json:"id"`
	Files []ManifestFile `json:"files"`
}

const tsLayout = "20060102T150405.000000000Z"

func (m *Manager) backup(f *host.File, cur []byte, now time.Time) (string, error) {
	if err := m.CheckBackupRoot(); err != nil {
		return "", err
	}
	id := now.UTC().Format(tsLayout)
	dir := filepath.Join(m.o.BackupDir, id)
	for i := 1; ; i++ {
		if _, err := os.Lstat(dir); errors.Is(err, fs.ErrNotExist) {
			break
		}
		id = fmt.Sprintf("%s-%d", now.UTC().Format(tsLayout), i)
		dir = filepath.Join(m.o.BackupDir, id)
	}
	rel := strings.TrimPrefix(filepath.Clean("/"+f.Path), "/")
	target := filepath.Join(dir, rel)
	if err := mkdirPrivate(filepath.Dir(target)); err != nil {
		return "", err
	}
	if err := writePrivate(target, cur); err != nil {
		return "", err
	}
	man := Manifest{ID: id, Files: []ManifestFile{{
		Path: f.Path, SHA256: Sum(cur), UID: f.UID, GID: f.GID, Mode: fmt.Sprintf("%#o", uint32(f.Mode.Perm())),
	}}}
	mb, _ := json.MarshalIndent(man, "", "  ")
	if err := writePrivate(filepath.Join(dir, "manifest.json"), mb); err != nil {
		return "", err
	}
	m.prune(rel)
	return target, nil
}

// Backups lists backup copies of path, oldest first.
func (m *Manager) Backups(path string) ([]string, error) {
	rel := strings.TrimPrefix(filepath.Clean("/"+path), "/")
	ents, err := os.ReadDir(m.o.BackupDir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(m.o.BackupDir, e.Name(), rel)
		if _, err := os.Lstat(p); err == nil {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (m *Manager) prune(rel string) {
	list, err := m.Backups(rel)
	if err != nil || len(list) <= m.o.Keep {
		return
	}
	for _, p := range list[:len(list)-m.o.Keep] {
		_ = os.Remove(p)
		// Drop empty parents and a manifest-only backup directory.
		root := filepath.Join(m.o.BackupDir, strings.SplitN(strings.TrimPrefix(p, m.o.BackupDir+string(filepath.Separator)), string(filepath.Separator), 2)[0])
		for d := filepath.Dir(p); d != root && strings.HasPrefix(d, root); d = filepath.Dir(d) {
			if os.Remove(d) != nil {
				break
			}
		}
		if ents, err := os.ReadDir(root); err == nil && len(ents) == 1 && ents[0].Name() == "manifest.json" {
			_ = os.Remove(filepath.Join(root, "manifest.json"))
			_ = os.Remove(root)
		}
	}
}

// writeAtomic replaces f.Path with data: temp file next to the target,
// fsync, chown/chmod to the original, rename, fsync the directory. When the
// directory is not writable or the owner cannot be preserved, it falls back
// to an in-place truncate-and-write (the backup already exists).
func (m *Manager) writeAtomic(f *host.File, data []byte) error {
	rp := m.real(f.Path)
	dir := filepath.Dir(rp)
	tmp := filepath.Join(dir, "."+filepath.Base(rp)+"."+brand.Name+"-tmp")
	_ = os.Remove(tmp)
	t, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsPermission(err) {
			return m.writeInPlace(f, data)
		}
		return err
	}
	cleanup := func(e error) error { t.Close(); os.Remove(tmp); return e }
	if _, err := t.Write(data); err != nil {
		return cleanup(err)
	}
	if err := t.Sync(); err != nil {
		return cleanup(err)
	}
	if err := t.Chmod(f.Mode.Perm()); err != nil {
		return cleanup(err)
	}
	if fi, err := t.Stat(); err == nil {
		if uid, gid, ok := Owner(fi); ok && (uid != f.UID || gid != f.GID) {
			if err := m.o.Chown(tmp, f.UID, f.GID); err != nil {
				cleanup(nil)
				return m.writeInPlace(f, data)
			}
		}
	}
	if err := t.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, rp); err != nil {
		os.Remove(tmp)
		if os.IsPermission(err) {
			return permErr(f.Kind, "write "+f.Path)
		}
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}

func (m *Manager) writeInPlace(f *host.File, data []byte) error {
	rp := m.real(f.Path)
	w, err := os.OpenFile(rp, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		if os.IsPermission(err) {
			return permErr(f.Kind, "write "+f.Path)
		}
		return err
	}
	if _, err := w.Write(data); err != nil {
		w.Close()
		return err
	}
	if err := w.Sync(); err != nil {
		w.Close()
		return err
	}
	return w.Close()
}
