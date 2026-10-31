package backup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"

	"github.com/tonhe/headsup/internal/brand"
	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/host/files"
)

// RestoreResult reports what Restore did, per path.
type RestoreResult struct {
	Restored  []string
	Unchanged []string
	Skipped   []string // not restored, with the reason in Failed or Notes
	Failed    map[string]error
	Notes     []string
	// DBSteps restore the database by hand; it is never written here.
	DBSteps []string
}

// OK reports whether every file was restored or already matched.
func (r RestoreResult) OK() bool { return len(r.Failed) == 0 }

// Restore puts the files of the bundle in dir back. Config, policy, records
// and DERP map files go through the host's managed pipeline (validate,
// back up the current file, atomic write keeping owner and mode); private
// keys are written atomically with the recorded mode and owner. The bundle
// is verified against its manifest first and refused if it was altered.
// The database is never touched: DBSteps say how to restore it.
func Restore(ctx context.Context, h host.Host, dir string, o Options) (RestoreResult, error) {
	o = o.withDefaults()
	res := RestoreResult{Failed: map[string]error{}}
	m, err := Verify(dir)
	if err != nil {
		return res, err
	}
	env := h.Env()
	for _, f := range m.Files {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		data, err := os.ReadFile(filepath.Join(dir, "files", filepath.FromSlash(rel(f.Path))))
		if err != nil {
			res.Failed[f.Path] = err
			continue
		}
		if managed(f.Kind) {
			restoreManaged(ctx, h, f, data, &res)
			continue
		}
		if o.Simulated {
			res.Skipped = append(res.Skipped, f.Path)
			res.Notes = append(res.Notes, f.Path+": not written on a simulated host")
			continue
		}
		if cur, err := os.ReadFile(f.Path); err == nil && bytes.Equal(cur, data) {
			res.Unchanged = append(res.Unchanged, f.Path)
			continue
		}
		if err := writeKey(f, data); err != nil {
			res.Failed[f.Path] = err
			continue
		}
		res.Restored = append(res.Restored, f.Path)
	}
	if m.DB != nil {
		switch m.DB.Method {
		case MethodPgDump:
			res.DBSteps = pgDumpSteps(h, dir)
		case MethodSimulated:
		default:
			e := env
			if e.DBPath == "" {
				e.DBPath = m.DB.Path
			}
			res.DBSteps = sqliteRestoreSteps(e, filepath.Join(dir, "db", filepath.Base(m.DB.Path)))
		}
	}
	return res, nil
}

func restoreManaged(ctx context.Context, h host.Host, f ManifestFile, data []byte, res *RestoreResult) {
	cur, err := h.Open(fileKind(f.Kind), f.Path)
	if errors.Is(err, fs.ErrNotExist) {
		res.Skipped = append(res.Skipped, f.Path)
		res.Failed[f.Path] = fmt.Errorf("%s no longer exists; recreate it, then restore again", f.Path)
		return
	}
	if err != nil {
		res.Failed[f.Path] = err
		return
	}
	if bytes.Equal(cur.Raw, data) {
		res.Unchanged = append(res.Unchanged, f.Path)
		return
	}
	c, err := h.Plan(ctx, cur, bytes.Clone(data))
	if err != nil {
		res.Failed[f.Path] = err
		return
	}
	if _, err := h.Apply(ctx, c); err != nil {
		res.Failed[f.Path] = err
		return
	}
	res.Restored = append(res.Restored, f.Path)
}

// writeKey replaces a private key file atomically with the recorded mode,
// and the recorded owner where this process may set it.
func writeKey(f ManifestFile, data []byte) error {
	mode := fs.FileMode(0o600)
	if v, err := strconv.ParseUint(f.Mode, 0, 32); err == nil {
		mode = fs.FileMode(v).Perm()
	}
	dir := filepath.Dir(f.Path)
	tmp := filepath.Join(dir, "."+filepath.Base(f.Path)+"."+brand.Name+"-restore")
	_ = os.Remove(tmp)
	t, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsPermission(err) {
			return &host.ErrPermission{Need: host.NeedConfigW, Op: "write " + f.Path, FixCommand: "sudo " + brand.Name}
		}
		return err
	}
	fail := func(e error) error { t.Close(); os.Remove(tmp); return e }
	if _, err := t.Write(data); err != nil {
		return fail(err)
	}
	if err := t.Sync(); err != nil {
		return fail(err)
	}
	if err := t.Chmod(mode); err != nil {
		return fail(err)
	}
	if fi, err := t.Stat(); err == nil {
		if uid, gid, ok := files.Owner(fi); ok && (uid != f.UID || gid != f.GID) {
			if err := os.Lchown(tmp, f.UID, f.GID); err != nil {
				return fail(&host.ErrPermission{Need: host.NeedConfigW, Op: "restore the owner of " + f.Path, FixCommand: "sudo " + brand.Name})
			}
		}
	}
	if err := t.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, f.Path); err != nil {
		os.Remove(tmp)
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}
