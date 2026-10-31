// Package activity is the append-only, local audit log of actions the user
// took (JSONL at StateDir/activity.jsonl). Parameters are redacted before
// they are written; the file and its directory are private (FR-064 style).
package activity

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/tonhe/headsup/internal/config"
)

// FileName is the log's name inside the state directory.
const FileName = "activity.jsonl"

// Redacted replaces secret values.
const Redacted = "[redacted]"

// Target is an object an action touched.
type Target struct {
	Kind string `json:"kind"`
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

// Result is the outcome of an action.
type Result struct {
	Status  string `json:"status"`            // "ok" | "error"
	Kind    string `json:"kind,omitempty"`    // error kind, e.g. "permission"
	Message string `json:"message,omitempty"` // error message (redacted)
}

// OK is a successful result.
func OK() Result { return Result{Status: "ok"} }

// Err is a failed result.
func Err(kind, msg string) Result { return Result{Status: "error", Kind: kind, Message: msg} }

// DiffStats summarises a file change.
type DiffStats struct {
	Added   int `json:"added"`
	Removed int `json:"removed"`
	Changed int `json:"changed,omitempty"`
}

// Entry is one log line.
type Entry struct {
	Time       time.Time      `json:"time"`
	Profile    string         `json:"profile,omitempty"`
	ActionID   string         `json:"action_id"`
	Targets    []Target       `json:"targets,omitempty"`
	Params     map[string]any `json:"params,omitempty"`
	Result     Result         `json:"result"`
	DurationMS int64          `json:"duration_ms"`
	BackupID   string         `json:"backup_id,omitempty"`
	DiffStats  *DiffStats     `json:"diff_stats,omitempty"`
	// Files lists each host file the action wrote. BackupID and DiffStats
	// above summarise them (the single backup ID, the summed stats).
	Files []FileChange `json:"files,omitempty"`
}

// FileChange is one host file write: its path, the backup taken before it,
// and line counts. File content is never logged.
type FileChange struct {
	Path      string    `json:"path"`
	BackupID  string    `json:"backup_id,omitempty"`
	DiffStats DiffStats `json:"diff_stats"`
}

// AddFiles records host file writes on e: each one in Files, a "file"
// target per path not already targeted, the summed diff stats, and the
// backup ID when the writes share one (or there is only one).
func (e *Entry) AddFiles(fs []FileChange) {
	if len(fs) == 0 {
		return
	}
	targeted := map[string]bool{}
	for _, t := range e.Targets {
		if t.Kind == "file" {
			targeted[t.ID] = true
			targeted[t.Name] = true
		}
	}
	sum := DiffStats{}
	if e.DiffStats != nil {
		sum = *e.DiffStats
	}
	ids := map[string]bool{}
	for _, f := range fs {
		e.Files = append(e.Files, f)
		sum.Added += f.DiffStats.Added
		sum.Removed += f.DiffStats.Removed
		sum.Changed += f.DiffStats.Changed
		if f.BackupID != "" {
			ids[f.BackupID] = true
		}
		if f.Path != "" && !targeted[f.Path] {
			targeted[f.Path] = true
			e.Targets = append(e.Targets, Target{Kind: "file", ID: f.Path, Name: f.Path})
		}
	}
	e.DiffStats = &sum
	if e.BackupID == "" && len(ids) == 1 {
		for id := range ids {
			e.BackupID = id
		}
	}
}

// Matches reports whether e mentions q (case-insensitive) in its action,
// profile, targets, result, or backup ID. An empty q matches everything.
func (e Entry) Matches(q string) bool {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return true
	}
	parts := []string{e.ActionID, e.Profile, e.Result.Status, e.Result.Kind, e.Result.Message, e.BackupID}
	for _, t := range e.Targets {
		parts = append(parts, t.Kind, t.ID, t.Name)
	}
	for _, f := range e.Files {
		parts = append(parts, f.Path)
	}
	return strings.Contains(strings.ToLower(strings.Join(parts, "\x00")), q)
}

// Logger records and reads activity.
type Logger interface {
	Log(Entry) error
	Recent(n int) ([]Entry, error)
}

// Nop discards everything.
type Nop struct{}

func (Nop) Log(Entry) error             { return nil }
func (Nop) Recent(int) ([]Entry, error) { return nil, nil }

// Memory keeps entries for this session only (the demo, or when the log
// file cannot be opened). Entries are redacted like the file's.
type Memory struct {
	mu  sync.Mutex
	es  []Entry
	Max int // entries kept; <= 0 means 1000
}

// Log implements Logger.
func (m *Memory) Log(e Entry) error {
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.es = append(m.es, Redact(e))
	limit := m.Max
	if limit <= 0 {
		limit = 1000
	}
	if len(m.es) > limit {
		m.es = append([]Entry(nil), m.es[len(m.es)-limit:]...)
	}
	return nil
}

// Recent implements Logger: up to n entries, newest first; n <= 0 is all.
func (m *Memory) Recent(n int) ([]Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Entry, 0, len(m.es))
	for i := len(m.es) - 1; i >= 0; i-- {
		out = append(out, m.es[i])
		if n > 0 && len(out) == n {
			break
		}
	}
	return out, nil
}

// File is the JSONL implementation.
type File struct {
	mu   sync.Mutex
	path string
}

// Open returns a File logger in dir (created 0700 if missing). It refuses
// a dir that is a symlink, group/other accessible, or owned by another user.
func Open(dir string) (*File, error) {
	if err := ensurePrivateDir(dir); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, FileName)
	if fi, err := os.Lstat(path); err == nil {
		if !fi.Mode().IsRegular() {
			return nil, fmt.Errorf("activity log %s is not a regular file", path)
		}
		if insecurePerm(fi.Mode()) {
			return nil, fmt.Errorf("activity log %s is accessible by group or others (mode %04o); fix with: chmod 600 %s",
				path, fi.Mode().Perm(), path)
		}
	}
	return &File{path: path}, nil
}

// OpenDefault opens the logger in config.StateDir().
func OpenDefault() (*File, error) { return Open(config.StateDir()) }

// Path is the log file path.
func (f *File) Path() string { return f.path }

// Log appends e after redacting it. A zero Time is set to now.
func (f *File) Log(e Entry) error {
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	e = Redact(e)
	b, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("activity: encode: %w", err)
	}
	b = append(b, '\n')
	f.mu.Lock()
	defer f.mu.Unlock()
	fh, err := os.OpenFile(f.path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("activity: %w", err)
	}
	defer fh.Close()
	if runtime.GOOS != "windows" {
		if err := fh.Chmod(0o600); err != nil {
			return fmt.Errorf("activity: %w", err)
		}
	}
	if _, err := fh.Write(b); err != nil {
		return fmt.Errorf("activity: %w", err)
	}
	return nil
}

// Recent returns up to n most recent entries, newest first. n <= 0 means all.
// Malformed lines are skipped.
func (f *File) Recent(n int) ([]Entry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, err := os.ReadFile(f.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("activity: %w", err)
	}
	var all []Entry
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var e Entry
		if json.Unmarshal(line, &e) == nil {
			all = append(all, e)
		}
	}
	out := make([]Entry, 0, len(all))
	for i := len(all) - 1; i >= 0; i-- {
		out = append(out, all[i])
		if n > 0 && len(out) == n {
			break
		}
	}
	return out, nil
}

var (
	secretKeyWords = []string{"key", "secret", "token", "password"}
	secretValue    = regexp.MustCompile(`(?i)\b(hs|ts)key-[A-Za-z0-9_\-]+`)
)

// Redact returns a copy of e with secret params and secret-looking values replaced.
func Redact(e Entry) Entry {
	if e.Params != nil {
		e.Params = redactMap(e.Params)
	}
	e.Result.Message = RedactString(e.Result.Message)
	if len(e.Targets) > 0 {
		ts := make([]Target, len(e.Targets))
		for i, t := range e.Targets {
			t.Name = RedactString(t.Name)
			t.ID = RedactString(t.ID)
			ts[i] = t
		}
		e.Targets = ts
	}
	e.BackupID = RedactString(e.BackupID)
	if len(e.Files) > 0 {
		fs := make([]FileChange, len(e.Files))
		for i, f := range e.Files {
			f.Path, f.BackupID = RedactString(f.Path), RedactString(f.BackupID)
			fs[i] = f
		}
		e.Files = fs
	}
	return e
}

// RedactString replaces hskey-/tskey- tokens inside s.
func RedactString(s string) string {
	if s == "" {
		return s
	}
	return secretValue.ReplaceAllString(s, Redacted)
}

func isSecretKey(k string) bool {
	k = strings.ToLower(k)
	for _, w := range secretKeyWords {
		if strings.Contains(k, w) {
			return true
		}
	}
	return false
}

func redactMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if isSecretKey(k) {
			out[k] = Redacted
			continue
		}
		out[k] = redactValue(v)
	}
	return out
}

func redactValue(v any) any {
	switch x := v.(type) {
	case string:
		return RedactString(x)
	case map[string]any:
		return redactMap(x)
	case map[string]string:
		m := make(map[string]any, len(x))
		for k, s := range x {
			m[k] = s
		}
		return redactMap(m)
	case []string:
		out := make([]string, len(x))
		for i, s := range x {
			out[i] = RedactString(s)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, s := range x {
			out[i] = redactValue(s)
		}
		return out
	case fmt.Stringer:
		return RedactString(x.String())
	}
	return v
}

func insecurePerm(m fs.FileMode) bool {
	return runtime.GOOS != "windows" && m.Perm()&0o077 != 0
}

func ensurePrivateDir(dir string) error {
	fi, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("activity: create %s: %w", dir, err)
		}
		if runtime.GOOS != "windows" {
			if err := os.Chmod(dir, 0o700); err != nil {
				return fmt.Errorf("activity: %w", err)
			}
		}
		fi, err = os.Lstat(dir)
	}
	if err != nil {
		return fmt.Errorf("activity: %w", err)
	}
	if fi.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("activity dir %s is a symlink; refusing to write secrets through it", dir)
	}
	if !fi.IsDir() {
		return fmt.Errorf("activity dir %s is not a directory", dir)
	}
	if insecurePerm(fi.Mode()) {
		return fmt.Errorf("activity dir %s is accessible by group or others (mode %04o); fix with: chmod 700 %s",
			dir, fi.Mode().Perm(), dir)
	}
	if !ownedByMe(fi) {
		return fmt.Errorf("activity dir %s is owned by another user; fix with: chown $(id -u) %s", dir, dir)
	}
	return nil
}
