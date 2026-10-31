package uitest

import (
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/tonhe/headsup/internal/icons"
)

// updateGoldens rewrites golden files instead of comparing. It is not named
// -update to avoid colliding with teatest's flag.
var updateGoldens = flag.Bool("update-goldens", false, "rewrite golden files")

// Golden compares a rendered frame (ANSI stripped, so goldens capture layout,
// not one theme's colours) with testdata/<TestName>.golden.
//
// Prefer Screen, which also renders the frame in the ASCII icon set, in 16
// colours and under NO_COLOR.
func Golden(t *testing.T, got string) {
	t.Helper()
	compareGolden(t, goldenPath(t), got)
}

func goldenPath(t *testing.T) string {
	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	return filepath.Join("testdata", name+".golden")
}

func compareGolden(t *testing.T, path, got string) {
	t.Helper()
	got = strings.TrimRight(ansi.Strip(got), "\n") + "\n"
	if *updateGoldens {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
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

// Screen snapshots a screen in every accessibility variant (constitution V,
// T171). render builds and renders the screen from scratch with the given
// icon set; it is called once per variant, after the colour profile is set.
//
//   - Unicode icons, true colour: compared with testdata/<TestName>.golden.
//   - ascii: ASCII icons, compared with testdata/<TestName>_ascii.golden.
//   - ansi16: 16 colours. The layout must match the first golden exactly, and
//     no 256-colour or true-colour sequence may appear.
//   - nocolor: NO_COLOR (lipgloss's Ascii profile, which drops every SGR
//     attribute). The layout must match the first golden and there must be
//     no SGR sequence at all, so the golden itself shows that selection,
//     focus and state never depend on colour alone.
func Screen(t *testing.T, render func(t *testing.T, ic icons.Set) string) {
	t.Helper()
	screen(t, true, render)
}

// ScreenColours is Screen without the ASCII icon variant, for output that
// does not draw icons (or always draws the ASCII set, like the CLI).
func ScreenColours(t *testing.T, render func(t *testing.T) string) {
	t.Helper()
	screen(t, false, func(t *testing.T, _ icons.Set) string { return render(t) })
}

func screen(t *testing.T, ascii bool, render func(t *testing.T, ic icons.Set) string) {
	t.Helper()
	old := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(old) })

	base := goldenPath(t)
	lipgloss.SetColorProfile(termenv.TrueColor)
	compareGolden(t, base, render(t, icons.Unicode))
	if ascii {
		t.Run("ascii", func(t *testing.T) {
			lipgloss.SetColorProfile(termenv.TrueColor)
			compareGolden(t, goldenPath(t), render(t, icons.ASCII))
		})
	}
	t.Run("ansi16", func(t *testing.T) {
		lipgloss.SetColorProfile(termenv.ANSI)
		out := render(t, icons.Unicode)
		if bad := sgrBeyond(out, termenv.ANSI); len(bad) > 0 {
			t.Errorf("16-colour frame uses wider colour sequences: %q", bad)
		}
		if !*updateGoldens {
			compareGolden(t, base, out)
		}
	})
	t.Run("nocolor", func(t *testing.T) {
		lipgloss.SetColorProfile(termenv.Ascii)
		out := render(t, icons.Unicode)
		if bad := sgrBeyond(out, termenv.Ascii); len(bad) > 0 {
			t.Errorf("NO_COLOR frame has SGR sequences: %q", bad)
		}
		if !*updateGoldens {
			compareGolden(t, base, out)
		}
	})
}

var sgrRE = regexp.MustCompile("\x1b\\[([0-9;:]*)m")

// sgrBeyond returns the SGR sequences in s that profile p cannot show: any
// SGR at all for Ascii, 256-colour or true-colour ones for ANSI. A bare reset
// is always allowed.
func sgrBeyond(s string, p termenv.Profile) []string {
	var bad []string
	for _, m := range sgrRE.FindAllStringSubmatch(s, -1) {
		params := m[1]
		if params == "" || params == "0" {
			continue
		}
		switch p {
		case termenv.Ascii:
			bad = append(bad, m[0])
		case termenv.ANSI:
			ps := strings.FieldsFunc(params, func(r rune) bool { return r == ';' || r == ':' })
			for _, v := range ps {
				n, _ := strconv.Atoi(v)
				if n == 38 || n == 48 || n == 58 {
					bad = append(bad, m[0])
					break
				}
			}
		}
	}
	return bad
}
