package files

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tonhe/fathom/internal/host"
)

// apply plans and applies next against the env's config file.
func (e *env) apply(t *testing.T, next string) (host.Applied, error) {
	t.Helper()
	f, err := e.m.Open(host.FileConfig, "/etc/headscale/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	c, err := e.m.Plan(context.Background(), f, []byte(next))
	if err != nil {
		t.Fatal(err)
	}
	return e.m.Apply(context.Background(), c)
}

// A config directory the user cannot write (typical /etc/headscale) but a
// writable file falls back to an in-place write after the backup.
func TestInPlaceFallbackWhenDirNotWritable(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	e := newEnv(t, Options{})
	dir := filepath.Dir(e.target())
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })

	next := "# comment\nlog:\n  level: debug\n"
	a, err := e.apply(t, next)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	got, _ := os.ReadFile(e.target())
	if string(got) != next {
		t.Fatalf("file = %q, want %q", got, next)
	}
	if fi, _ := os.Stat(e.target()); fi.Mode().Perm() != 0o640 {
		t.Errorf("in-place write must keep mode, got %v", fi.Mode().Perm())
	}
	if err := e.m.Rollback(context.Background(), a); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if got, _ := os.ReadFile(e.target()); string(got) != original {
		t.Fatalf("rollback not byte-exact: %q", got)
	}
}

// A file the user cannot write at all yields ErrPermission with a fix
// command, and the file is untouched.
func TestPermissionErrorCarriesFix(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores file permissions")
	}
	e := newEnv(t, Options{})
	dir := filepath.Dir(e.target())
	os.Chmod(e.target(), 0o440)
	os.Chmod(dir, 0o555)
	t.Cleanup(func() { os.Chmod(dir, 0o755); os.Chmod(e.target(), 0o640) })

	_, err := e.apply(t, "log:\n  level: debug\n")
	var pe *host.ErrPermission
	if !errors.As(err, &pe) {
		t.Fatalf("err = %v, want *host.ErrPermission", err)
	}
	if pe.FixCommand == "" || pe.Need != host.NeedConfigW {
		t.Errorf("permission error = %+v", pe)
	}
	if got, _ := os.ReadFile(e.target()); string(got) != original {
		t.Fatal("file changed despite the permission error")
	}
}

func TestNeedForKinds(t *testing.T) {
	tests := map[host.FileKind]host.Need{
		host.FileConfig: host.NeedConfigW, host.FilePolicy: host.NeedPolicyW,
		host.FileExtraRecords: host.NeedRecordsW, host.FileDERPMap: host.NeedConfigW,
	}
	for k, want := range tests {
		if got := needFor(k); got != want {
			t.Errorf("needFor(%v) = %v, want %v", k, got, want)
		}
	}
	if DefaultEffect(host.FilePolicy) != host.EffectReload || DefaultEffect(host.FileExtraRecords) != host.EffectLive ||
		DefaultEffect(host.FileConfig) != host.EffectRestart {
		t.Error("DefaultEffect does not match research R17")
	}
}

func TestDefaultBackupDirHonoursXDG(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/x/state")
	if got := DefaultBackupDir(); filepath.Dir(filepath.Dir(got)) != "/x/state" {
		t.Errorf("DefaultBackupDir = %q, want under /x/state", got)
	}
}

func TestChangedAndOpenErrors(t *testing.T) {
	if a, r := Changed(Diff([]byte("x\ny\n"), []byte("x\nz\nw\n"))); a != 2 || r != 1 {
		t.Errorf("Changed = +%d -%d, want +2 -1", a, r)
	}
	e := newEnv(t, Options{})
	if _, err := e.m.Open(host.FileConfig, ""); err == nil {
		t.Error("Open with no path must fail")
	}
	if _, err := e.m.Open(host.FileConfig, "/etc/headscale/missing.yaml"); err == nil {
		t.Error("Open of a missing file must fail")
	}
}
