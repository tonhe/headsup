package theme

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// TestNoHardcodedColours enforces that UI code reads colours from theme
// roles: outside internal/theme no non-test .go file may call lipgloss.Color(
// or contain a quoted "#rrggbb" literal.
func TestNoHardcodedColours(t *testing.T) {
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test file")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(self), "..", ".."))
	themeDir := filepath.Join(root, "internal", "theme")
	hexLit := regexp.MustCompile("[\"`]#[0-9a-fA-F]{6}[\"`]")

	var bad []string
	for _, top := range []string{"internal", "cmd"} {
		start := filepath.Join(root, top)
		if _, err := os.Stat(start); err != nil {
			continue
		}
		err := filepath.WalkDir(start, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if path == themeDir || d.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			for i, line := range strings.Split(string(b), "\n") {
				if strings.Contains(line, "lipgloss.Color(") || hexLit.MatchString(line) {
					bad = append(bad, rel+":"+strconv.Itoa(i+1)+": "+strings.TrimSpace(line))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(bad) > 0 {
		t.Fatalf("hard-coded colours outside internal/theme (use theme roles):\n%s", strings.Join(bad, "\n"))
	}
}
