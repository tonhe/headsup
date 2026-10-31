package policy

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tonhe/headsup/internal/host"
)

func TestHistoryRecordListLoad(t *testing.T) {
	root := filepath.Join(t.TempDir(), "policy")
	h := NewHistory(root)
	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

	r1, fresh, err := h.Record("https://hs.example.com", "{}\n", SourceServer, t0.Add(-time.Hour), t0)
	if err != nil || !fresh {
		t.Fatalf("record: %v fresh=%v", err, fresh)
	}
	if _, fresh, _ := h.Record("https://hs.example.com", "{}\n", SourceServer, time.Time{}, t0.Add(time.Minute)); fresh {
		t.Fatal("same content must be deduplicated by sha256")
	}
	if _, _, err := h.Record("https://hs.example.com", `{"acls":[]}`, SourceApplied, time.Time{}, t0.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	revs, err := h.List("https://hs.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(revs) != 2 || revs[0].Source != SourceApplied || revs[1].SHA256 != r1.SHA256 {
		t.Fatalf("revisions: %+v", revs)
	}
	if !revs[1].FetchedAt.Equal(t0.Add(time.Minute)) {
		t.Errorf("dedupe should move the entry to the newest time, got %v", revs[1].FetchedAt)
	}
	got, err := h.Load(revs[1])
	if err != nil || got != "{}\n" {
		t.Fatalf("load: %q %v", got, err)
	}

	dir := filepath.Join(root, "hs.example.com")
	for _, p := range unixOnly([]string{root, dir}) {
		fi, err := os.Stat(p)
		if err != nil || fi.Mode().Perm() != 0o700 {
			t.Errorf("%s mode %v, want 0700 (%v)", p, fi.Mode().Perm(), err)
		}
	}
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		fi, _ := e.Info()
		if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
			t.Errorf("%s mode %v, want 0600", e.Name(), fi.Mode().Perm())
		}
	}

	// Corruption is detected.
	if err := os.WriteFile(filepath.Join(dir, revs[1].Path), []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Load(revs[1]); err == nil || !strings.Contains(err.Error(), "corrupted") {
		t.Errorf("tampered revision should fail: %v", err)
	}
}

func TestHistoryKeepsLastN(t *testing.T) {
	h := NewHistory(filepath.Join(t.TempDir(), "h"))
	h.Max = 5
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range 8 {
		if _, _, err := h.Record("p", fmt.Sprintf(`{"n": %d}`, i), SourceServer, time.Time{}, t0.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	revs, err := h.List("p")
	if err != nil || len(revs) != 5 {
		t.Fatalf("kept %d revisions (%v), want 5", len(revs), err)
	}
	if c, _ := h.Load(revs[0]); c != `{"n": 7}` {
		t.Errorf("newest = %q", c)
	}
	files, _ := filepath.Glob(filepath.Join(h.Root, "p", "*.hujson"))
	if len(files) != 5 {
		t.Errorf("%d content files on disk, want 5 (pruned)", len(files))
	}
	if revs, err := h.List("never-recorded"); err != nil || revs != nil {
		t.Errorf("unknown profile: %v %v", revs, err)
	}
}

// unixOnly returns s, or nothing on Windows, where Go reports every
// directory as 0777 and files carry no Unix owner.
func unixOnly(s []string) []string {
	if runtime.GOOS == "windows" {
		return nil
	}
	return s
}

func TestHistoryRefusesInsecureDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs Unix mode bits, symlinks and ownership")
	}
	root := t.TempDir()
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	h := NewHistory(root)
	_, _, err := h.Record("p", "{}", SourceServer, time.Time{}, time.Now())
	var ie *host.ErrInsecureBackupDir
	if !errors.As(err, &ie) || ie.FixCommand != "chmod 700 "+root {
		t.Fatalf("want ErrInsecureBackupDir with chmod fix, got %v", err)
	}

	// A symlinked profile directory is refused too.
	safe := filepath.Join(t.TempDir(), "root")
	h = NewHistory(safe)
	if _, _, err := h.Record("a", "{}", SourceServer, time.Time{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(safe, "a"), filepath.Join(safe, "b")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.Record("b", "{}", SourceServer, time.Time{}, time.Now()); !errors.As(err, &ie) {
		t.Fatalf("symlink must be refused, got %v", err)
	}
	if _, err := h.List("b"); !errors.As(err, &ie) {
		t.Fatalf("list through a symlink must be refused, got %v", err)
	}
	// Someone else's directory.
	h.UID = os.Getuid() + 1
	if _, _, err := h.Record("a", "{}", SourceServer, time.Time{}, time.Now()); !errors.As(err, &ie) || !strings.Contains(ie.FixCommand, "chown") {
		t.Fatalf("foreign owner must be refused, got %v", err)
	}
}

func TestProfileKey(t *testing.T) {
	for in, want := range map[string]string{
		"https://hs.example.com:8443/": "hs.example.com_8443",
		"homelab":                      "homelab",
		"":                             "default",
		"../../etc":                    "etc",
		"host:hs01":                    "host_hs01",
	} {
		if got := ProfileKey(in); got != want {
			t.Errorf("ProfileKey(%q) = %q, want %q", in, got, want)
		}
	}
	if !strings.HasSuffix(DefaultHistoryDir(), filepath.Join("policy")) {
		t.Error("default dir")
	}
}
