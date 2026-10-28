package uitest

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// updateGoldens rewrites golden files instead of comparing. It is not named
// -update to avoid colliding with teatest's flag.
var updateGoldens = flag.Bool("update-goldens", false, "rewrite golden files")

// Golden compares a rendered frame (ANSI stripped, so goldens capture layout,
// not one theme's colours) with testdata/<TestName>.golden.
func Golden(t *testing.T, got string) {
	t.Helper()
	got = strings.TrimRight(ansi.Strip(got), "\n") + "\n"
	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	path := filepath.Join("testdata", name+".golden")
	if *updateGoldens {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden %s (run make golden-update): %v", path, err)
	}
	if string(want) != got {
		t.Errorf("frame differs from %s (run make golden-update if intended)\n--- want\n%s\n--- got\n%s", path, want, got)
	}
}
