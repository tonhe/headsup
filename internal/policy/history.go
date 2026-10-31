package policy

import (
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
	"sync"
	"time"

	"github.com/tonhe/headsup/internal/config"
	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/host/files"
)

// Source says why a revision was recorded.
type Source string

const (
	SourceServer   Source = "server"    // seen when fetched
	SourcePreApply Source = "pre-apply" // the live policy just before an apply
	SourceApplied  Source = "applied"   // what an apply wrote
)

// DefaultHistoryMax is how many revisions are kept per profile.
const DefaultHistoryMax = 50

// Revision is one locally kept policy version (data-model PolicyRevision).
type Revision struct {
	Profile         string    `json:"profile"`
	FetchedAt       time.Time `json:"fetched_at"`
	ServerUpdatedAt time.Time `json:"server_updated_at,omitzero"`
	SHA256          string    `json:"sha256"`
	Path            string    `json:"path"`
	Source          Source    `json:"source"`
}

// History stores policy revisions under Root/<profile>/, content-addressed by
// sha256 so identical policies are stored once. Directories are 0700, files
// 0600, and a root that is a symlink, shared, or owned by someone else is
// refused (FR-064).
type History struct {
	Root string
	Max  int
	// UID is the expected owner of the directories; -1 skips the check.
	UID int

	mu sync.Mutex
}

// DefaultHistoryDir is $XDG_STATE_HOME/<brand>/policy.
func DefaultHistoryDir() string { return filepath.Join(config.StateDir(), "policy") }

// NewHistory returns a store rooted at root, keeping DefaultHistoryMax revisions.
func NewHistory(root string) *History {
	return &History{Root: root, Max: DefaultHistoryMax, UID: os.Getuid()}
}

// Sum is the hex sha256 of raw.
func Sum(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

// ProfileKey turns a profile label or server URL into a safe directory name.
func ProfileKey(s string) string {
	s = strings.TrimPrefix(strings.TrimPrefix(s, "https://"), "http://")
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	k := strings.Trim(b.String(), "._")
	if k == "" {
		return "default"
	}
	return k
}

func (h *History) dir(profile string) string { return filepath.Join(h.Root, ProfileKey(profile)) }

const indexName = "index.json"

// Record stores raw as the newest revision for profile. If the same content
// is already kept, its entry moves to the top instead of being duplicated.
// It reports whether the content was new.
func (h *History) Record(profile, raw string, src Source, serverUpdated, at time.Time) (Revision, bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	dir := h.dir(profile)
	if err := h.ensure(dir); err != nil {
		return Revision{}, false, err
	}
	revs, err := readIndex(dir)
	if err != nil {
		return Revision{}, false, err
	}
	sum := Sum(raw)
	fresh := true
	kept := revs[:0]
	for _, r := range revs {
		if r.SHA256 == sum {
			fresh = false
			continue
		}
		kept = append(kept, r)
	}
	name := sum + ".hujson"
	path := filepath.Join(dir, name)
	if fresh {
		if err := writePrivate(path, []byte(raw)); err != nil {
			return Revision{}, false, err
		}
	}
	rev := Revision{Profile: profile, FetchedAt: at.UTC(), ServerUpdatedAt: serverUpdated.UTC(), SHA256: sum, Path: name, Source: src}
	if serverUpdated.IsZero() {
		rev.ServerUpdatedAt = time.Time{}
	}
	revs = append([]Revision{rev}, kept...)
	max := h.Max
	if max <= 0 {
		max = DefaultHistoryMax
	}
	var drop []Revision
	if len(revs) > max {
		drop = revs[max:]
		revs = revs[:max]
	}
	if err := writeIndex(dir, revs); err != nil {
		return Revision{}, false, err
	}
	for _, r := range drop {
		_ = os.Remove(filepath.Join(dir, filepath.Base(r.Path)))
	}
	return rev, fresh, nil
}

// List returns the kept revisions for profile, newest first.
func (h *History) List(profile string) ([]Revision, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	dir := h.dir(profile)
	if _, err := os.Lstat(dir); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err := h.checkPrivate(dir); err != nil {
		return nil, err
	}
	revs, err := readIndex(dir)
	sort.SliceStable(revs, func(i, j int) bool { return revs[i].FetchedAt.After(revs[j].FetchedAt) })
	return revs, err
}

// Load returns a revision's content, verifying its hash.
func (h *History) Load(r Revision) (string, error) {
	b, err := os.ReadFile(filepath.Join(h.dir(r.Profile), filepath.Base(r.Path)))
	if err != nil {
		return "", err
	}
	if Sum(string(b)) != r.SHA256 {
		return "", fmt.Errorf("policy history: %s is corrupted (hash mismatch)", r.Path)
	}
	return string(b), nil
}

func (h *History) ensure(dir string) error {
	if err := h.ensureOne(h.Root); err != nil {
		return err
	}
	return h.ensureOne(dir)
}

func (h *History) ensureOne(dir string) error {
	if _, err := os.Lstat(dir); errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
			return err
		}
		if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return err
		}
	}
	return h.checkPrivate(dir)
}

func (h *History) checkPrivate(dir string) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	owner, _, ok := files.Owner(fi)
	insecure := func(fix string) error {
		return &host.ErrInsecureBackupDir{Path: dir, Mode: fi.Mode(), Owner: owner, FixCommand: fix}
	}
	switch {
	case fi.Mode()&fs.ModeSymlink != 0:
		return insecure(fmt.Sprintf("rm %s && mkdir -m 700 %s", dir, dir))
	case !fi.IsDir():
		return insecure(fmt.Sprintf("mv %s %s.old && mkdir -m 700 %s", dir, dir, dir))
	case ok && h.UID >= 0 && owner != h.UID:
		return insecure(fmt.Sprintf("sudo chown -R $(id -u):$(id -g) %s && chmod 700 %s", dir, dir))
	case ok && fi.Mode().Perm()&0o077 != 0:
		// Mode bits mean something only where the platform has Unix
		// ownership (ok); Windows reports every directory as 0777.
		return insecure("chmod 700 " + dir)
	}
	return nil
}

func readIndex(dir string) ([]Revision, error) {
	b, err := os.ReadFile(filepath.Join(dir, indexName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var revs []Revision
	if err := json.Unmarshal(b, &revs); err != nil {
		return nil, fmt.Errorf("policy history index %s: %w", dir, err)
	}
	return revs, nil
}

func writeIndex(dir string, revs []Revision) error {
	b, err := json.MarshalIndent(revs, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, "."+indexName+".tmp")
	_ = os.Remove(tmp)
	if err := writePrivate(tmp, b); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, indexName))
}

// writePrivate writes a 0600 file, chmod-ing after open to defeat the umask.
func writePrivate(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
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
