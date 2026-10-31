package files

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/tonhe/headsup/internal/host"
)

func TestExportedPrivateHelpers(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "b")
	if err := MkdirPrivate(dir); err != nil {
		t.Fatal(err)
	}
	if err := CheckPrivateDir(dir, CurrentUID(), false); err != nil {
		t.Fatalf("fresh private dir refused: %v", err)
	}
	p := filepath.Join(dir, "f")
	if err := WritePrivate(p, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := WritePrivate(p, []byte("y")); err == nil {
		t.Error("WritePrivate must not overwrite")
	}
	if runtime.GOOS == "windows" {
		t.Skip("Unix modes: 0700/0600 and group bits are POSIX permissions")
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Errorf("file mode %v", fi.Mode().Perm())
	}
	if err := os.Chmod(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	var ie *host.ErrInsecureBackupDir
	if err := CheckPrivateDir(dir, CurrentUID(), false); !errors.As(err, &ie) {
		t.Errorf("group-readable dir accepted: %v", err)
	}
}
