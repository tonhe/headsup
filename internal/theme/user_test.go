package theme

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validTOML = `
name = "Test Ocean"
base00 = "#0b1020"
base01 = "#131a2e"
base02 = "#1e2742"
base03 = "#4a5578"
base04 = "#8792b3"
base05 = "#c8d0e8"
base06 = "#e1e6f5"
base07 = "#ffffff"
base08 = "#ff6b81"
base09 = "#ffa65c"
base0A = "#ffd866"
base0B = "#7bd88f"
base0C = "#5ad4e6"
base0D = "#7aa2f7"
base0E = "#c792ea"
base0F = "#B07A5B"
`

const validYAML = `scheme: "Tomorrow Night"
author: "Chris Kempson"
base00: "1d1f21"
base01: "282a2e"
base02: "373b41"
base03: "969896"
base04: "b4b7b4"
base05: "c5c8c6"
base06: "e0e0e0"
base07: "ffffff"
base08: "cc6666"
base09: "de935f"
base0A: "f0c674"
base0B: "b5bd68"
base0C: "8abeb7"
base0D: "81a2be"
base0E: "b294bb"
base0F: 000000
`

func writeTheme(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadUserThemes(t *testing.T) {
	tests := []struct {
		name      string
		file      string
		body      string
		wantLoad  bool
		wantWarn  string // substring; "" means no warnings
		check     func(t *testing.T, th Theme)
		wantCount int
	}{
		{name: "valid toml", file: "ocean.toml", body: validTOML, wantLoad: true,
			check: func(t *testing.T, th Theme) {
				if th.Name != "Test Ocean" || th.Slug != "test-ocean" || th.Light {
					t.Fatalf("meta %q %q %v", th.Name, th.Slug, th.Light)
				}
				if th.Base0F != "#b07a5b" || th.Base00 != "#0b1020" {
					t.Fatalf("colours %q %q", th.Base0F, th.Base00)
				}
				if !strings.HasSuffix(th.Source, "ocean.toml") {
					t.Fatalf("source %q", th.Source)
				}
			}},
		{name: "missing key", file: "m.toml", body: strings.Replace(validTOML, "base0A = \"#ffd866\"\n", "", 1),
			wantWarn: "missing base0A"},
		{name: "bad hex", file: "b.toml", body: strings.Replace(validTOML, "#ff6b81", "#ff6b8z", 1),
			wantWarn: "base08"},
		{name: "short hex", file: "s.toml", body: strings.Replace(validTOML, "#ff6b81", "#fff", 1),
			wantWarn: "not a #rrggbb"},
		{name: "yaml scheme", file: "tomorrow-night.yaml", body: validYAML, wantLoad: true,
			check: func(t *testing.T, th Theme) {
				if th.Name != "Tomorrow Night" || th.Slug != "tomorrow-night" {
					t.Fatalf("meta %q %q", th.Name, th.Slug)
				}
				if th.Base00 != "#1d1f21" || th.Base0F != "#000000" {
					t.Fatalf("colours %q %q", th.Base00, th.Base0F)
				}
			}},
		{name: "role override", file: "r.toml",
			body: validTOML + "\n[roles]\naccent = \"base0E\"\ntagBg = \"#2A1F3D\"\n", wantLoad: true,
			check: func(t *testing.T, th Theme) {
				if th.Overrides["accent"] != "base0E" || th.Overrides["tag_bg"] != "#2a1f3d" {
					t.Fatalf("overrides %v", th.Overrides)
				}
				r := th.Roles()
				if r.Accent != th.Base0E || r.TagBg != "#2a1f3d" {
					t.Fatalf("roles accent=%q tagbg=%q", r.Accent, r.TagBg)
				}
			}},
		{name: "unknown role warns", file: "u.toml",
			body: validTOML + "\n[roles]\nsparkle = \"base0E\"\n", wantLoad: true, wantWarn: "unknown role"},
		{name: "low contrast", file: "low.toml",
			body: strings.Replace(validTOML, "#c8d0e8", "#1a2030", 1), wantLoad: true, wantWarn: "low contrast"},
		{name: "light derived", file: "light.toml",
			body: strings.NewReplacer("#0b1020", "#fafafa", "#c8d0e8", "#202020").Replace(validTOML), wantLoad: true,
			check: func(t *testing.T, th Theme) {
				if !th.Light {
					t.Fatal("expected light")
				}
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeTheme(t, dir, tt.file, tt.body)
			writeTheme(t, dir, "ignored.txt", "not a theme")
			loaded, warns := LoadUserThemes(dir)
			if tt.wantLoad != (len(loaded) == 1) {
				t.Fatalf("loaded=%d warns=%v", len(loaded), warns)
			}
			if tt.wantWarn == "" && len(warns) != 0 {
				t.Fatalf("unexpected warnings %v", warns)
			}
			if tt.wantWarn != "" {
				if len(warns) == 0 || !strings.Contains(strings.Join(warns, "\n"), tt.wantWarn) ||
					!strings.Contains(warns[0], tt.file) {
					t.Fatalf("warnings %v, want %q naming %s", warns, tt.wantWarn, tt.file)
				}
			}
			if tt.check != nil && len(loaded) == 1 {
				tt.check(t, loaded[0])
			}
		})
	}
}

func TestLoadUserThemesMissingDir(t *testing.T) {
	l, w := LoadUserThemes(filepath.Join(t.TempDir(), "none"))
	if l != nil || w != nil {
		t.Fatal("missing dir should be silent")
	}
}

func TestRegisterUserThemes(t *testing.T) {
	dir := t.TempDir()
	writeTheme(t, dir, "x.toml", strings.Replace(validTOML, "Test Ocean", "Registered Unit Test", 1))
	n, w := RegisterUserThemes(dir)
	if n != 1 || len(w) != 0 {
		t.Fatalf("n=%d w=%v", n, w)
	}
	if ByName("registered-unit-test") == nil {
		t.Fatal("not registered")
	}
}

func TestExampleThemes(t *testing.T) {
	loaded, warns := LoadUserThemes(filepath.Join("..", "..", "themes"))
	if len(warns) != 0 {
		t.Fatalf("warnings: %v", warns)
	}
	got := map[string]bool{}
	for _, th := range loaded {
		got[th.Slug] = true
	}
	for _, s := range []string{"midnight-harbor", "high-contrast"} {
		if !got[s] {
			t.Errorf("example theme %s not loaded (got %v)", s, got)
		}
	}
}

func TestSlugifyAndSnake(t *testing.T) {
	for in, want := range map[string]string{"Midnight Harbor": "midnight-harbor", "  A--B_c!": "a-b-c", "Ünï 2": "n-2"} {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"tagBg": "tag_bg", "tag-bg": "tag_bg", "border_focus": "border_focus", "Accent": "accent"} {
		if got := snake(in); got != want {
			t.Errorf("snake(%q) = %q want %q", in, got, want)
		}
	}
}
