package prefs_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/tonhe/headsup/internal/app"
	"github.com/tonhe/headsup/internal/config"
	"github.com/tonhe/headsup/internal/theme"
	"github.com/tonhe/headsup/internal/ui/uitest"
)

// sgr returns the truecolor foreground and background SGR fragments for c,
// exactly as lipgloss emits them.
func sgr(t *testing.T, c lipgloss.Color) (fg, bg string) {
	t.Helper()
	tc := termenv.TrueColor.Color(string(c))
	if tc == nil {
		t.Fatalf("bad colour %q", c)
	}
	return tc.Sequence(false), tc.Sequence(true)
}

func usesColour(out string, fg, bg string) bool {
	return strings.Contains(out, fg) || strings.Contains(out, bg)
}

const userTheme = `name  = "Test Harbor"
slug  = "test-harbor"
light = false
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
base0D = "#11aa33"
base0E = "#c792ea"
base0F = "#b07a5b"
`

func setup(t *testing.T, opt app.Options) (*driver, string) {
	t.Helper()
	uitest.ForceColor()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	cfg := config.Default()
	cfg.UI.Theme = "tokyo-night"
	if opt.Config != nil {
		cfg = *opt.Config
	}
	opt.Config, opt.ConfigPath = &cfg, path
	opt.ThemesDir = filepath.Join(dir, "themes")
	if opt.Theme == "" {
		opt.Theme = cfg.UI.Theme
	}
	t.Cleanup(func() { theme.Set(*theme.ByName(theme.Default)) })
	return newDriver(t, 120, 40, opt), path
}

func loadSaved(t *testing.T, path string) config.Config {
	t.Helper()
	c, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestThemeFlow (T121, US8 independent test): switch theme in the picker,
// every view re-renders in the new palette with no restart, the choice is
// saved to config, and a user theme file shows up in the picker.
func TestThemeFlow(t *testing.T) {
	d, path := setup(t, app.Options{})
	oldFg, oldBg := sgr(t, theme.ByName("tokyo-night").Roles().Accent)
	newFg, newBg := sgr(t, theme.ByName("dracula").Roles().Accent)
	if !usesColour(d.view(), oldFg, oldBg) {
		t.Fatal("precondition: the start screen should use the tokyo-night accent")
	}

	// Pick Dracula in the picker.
	d.key("T")
	if !strings.Contains(ansi.Strip(d.view()), "Theme") {
		t.Fatalf("T should open the theme picker:\n%s", ansi.Strip(d.view()))
	}
	d.key("dracula")
	// Live preview: the screen behind the picker is already repainted.
	if !usesColour(d.view(), newFg, newBg) {
		t.Error("moving onto a theme should preview it live")
	}
	d.key("enter")
	if strings.Contains(ansi.Strip(d.view()), "search themes") {
		t.Fatal("enter should close the picker")
	}

	// Every view re-renders in the new palette.
	for _, k := range []string{"1", "2", "3", ","} {
		d.key(k)
		out := d.view()
		if !usesColour(out, newFg, newBg) {
			t.Errorf("view %s does not use the new accent", k)
		}
		if usesColour(out, oldFg, oldBg) {
			t.Errorf("view %s still uses the old accent", k)
		}
	}
	if !strings.Contains(ansi.Strip(d.view()), "Dracula") {
		t.Errorf("Preferences should show the new theme:\n%s", ansi.Strip(d.view()))
	}

	// Persisted.
	if got := loadSaved(t, path).UI.Theme; got != "dracula" {
		t.Fatalf("saved ui.theme = %q, want dracula", got)
	}

	// A user theme dropped into the themes dir appears after a reload.
	if err := os.MkdirAll(filepath.Join(filepath.Dir(path), "themes"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "themes", "harbor.toml"), []byte(userTheme), 0o600); err != nil {
		t.Fatal(err)
	}
	d.key("T", "ctrl+r", "harbor")
	if !strings.Contains(ansi.Strip(d.view()), "Test Harbor") {
		t.Fatalf("user theme missing from the picker:\n%s", ansi.Strip(d.view()))
	}
	d.key("enter")
	userFg, userBg := sgr(t, theme.ByName("test-harbor").Roles().Accent)
	if !usesColour(d.view(), userFg, userBg) {
		t.Error("the user theme should be applied")
	}
	if got := loadSaved(t, path).UI.Theme; got != "test-harbor" {
		t.Fatalf("saved ui.theme = %q, want test-harbor", got)
	}

	// esc restores the theme that was active before the picker opened.
	d.key("T", "nord")
	d.key("esc", "esc")
	if !usesColour(d.view(), userFg, userBg) {
		t.Error("esc should restore the previous theme")
	}
	if got := loadSaved(t, path).UI.Theme; got != "test-harbor" {
		t.Errorf("cancel must not save; ui.theme = %q", got)
	}
}

// TestThemeFilter checks the light/dark filter.
func TestThemeFilter(t *testing.T) {
	d, _ := setup(t, app.Options{})
	d.key("T", "tab") // dark
	if out := ansi.Strip(d.view()); strings.Contains(out, "Solarized Light") || !strings.Contains(out, "dark") {
		t.Errorf("dark filter should hide light themes:\n%s", out)
	}
	d.key("tab") // light
	if out := ansi.Strip(d.view()); !strings.Contains(out, "light") || strings.Contains(out, "Solarized Dark ") {
		t.Errorf("light filter should hide dark themes:\n%s", out)
	}
}

// TestProfileThemeOverride (T124): ctrl+t saves the theme for the connected
// profile only, and Preferences can clear it again.
func TestProfileThemeOverride(t *testing.T) {
	cfg := config.Default()
	cfg.UI.Theme = "tokyo-night"
	cfg.Profiles = []config.Profile{{Name: "work", URL: "https://hs.example.com"}}
	d, path := setup(t, app.Options{Config: &cfg, Profile: "work"})
	d.key("T", "nord", "ctrl+t")
	saved := loadSaved(t, path)
	if p, _ := saved.FindProfile("work"); p.Theme != "nord" {
		t.Fatalf("profile theme = %q, want nord", p.Theme)
	}
	if saved.UI.Theme != "tokyo-night" {
		t.Errorf("ctrl+t must not change ui.theme; got %q", saved.UI.Theme)
	}
	// Preferences shows the override; x on that row clears it.
	d.key(",")
	out := ansi.Strip(d.view())
	if !strings.Contains(out, "Profile theme") || !strings.Contains(out, "Nord") {
		t.Fatalf("Preferences should show the profile theme:\n%s", out)
	}
	d.key("j", "x")
	if p, _ := loadSaved(t, path).FindProfile("work"); p.Theme != "" {
		t.Errorf("reset should clear the override; got %q", p.Theme)
	}
	fg, bg := sgr(t, theme.ByName("tokyo-night").Roles().Accent)
	if !usesColour(d.view(), fg, bg) {
		t.Error("clearing the override should fall back to ui.theme")
	}
}

// TestPreferencesRows edits icons, density, refresh and columns.
func TestPreferencesRows(t *testing.T) {
	d, path := setup(t, app.Options{})
	d.key(",")
	out := ansi.Strip(d.view())
	for _, want := range []string{"Preferences", "Theme", "Icons", "Density", "Sidebar", "Mouse", "Time format", "Refresh", "Columns: machines"} {
		if !strings.Contains(out, want) {
			t.Errorf("Preferences missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Profile theme") {
		t.Error("no config profile: the profile theme row should be hidden")
	}

	d.key("j", "l") // icons: unicode -> nerd
	if got := loadSaved(t, path).UI.Icons; got != "nerd" {
		t.Errorf("icons = %q, want nerd", got)
	}
	d.key("h", "h") // -> unicode -> ascii
	if got := loadSaved(t, path).UI.Icons; got != "ascii" {
		t.Errorf("icons = %q, want ascii", got)
	}
	if !strings.Contains(ansi.Strip(d.view()), "> Icons") {
		t.Errorf("ASCII icons should apply live:\n%s", ansi.Strip(d.view()))
	}
	d.key("x")
	if got := loadSaved(t, path).UI.Icons; got != "unicode" {
		t.Errorf("reset icons = %q, want unicode", got)
	}

	d.key("j", "enter") // density form
	d.key("right", "enter")
	if got := loadSaved(t, path).UI.Density; got != "compact" {
		t.Errorf("density = %q, want compact", got)
	}

	d.key("j", "j", "j", "j", "enter", "ctrl+u", "10s", "enter") // refresh
	if got := loadSaved(t, path).UI.Refresh; got != "10s" {
		t.Errorf("refresh = %q, want 10s", got)
	}
	d.key("enter", "ctrl+u", "soon", "enter") // invalid: form stays open
	if got := loadSaved(t, path).UI.Refresh; got != "10s" {
		t.Errorf("invalid duration saved: %q", got)
	}
	d.key("esc")

	d.key("G", "enter", "ctrl+u", "name, status", "enter")
	if got := loadSaved(t, path).UI.Columns["machines"]; strings.Join(got, ",") != "name,status" {
		t.Errorf("machines columns = %v", got)
	}

	d.key("k", "k", "k", "k", "k", "space") // up to Mouse and toggle it off
	saved := loadSaved(t, path)
	if saved.UI.Mouse {
		t.Error("space on Mouse should turn it off")
	}
	if d.m.MouseEnabled() {
		t.Error("turning mouse off should apply live")
	}
	d.key("k", "l") // Sidebar: auto -> always
	if got := loadSaved(t, path).UI.Sidebar; got != "always" {
		t.Errorf("sidebar = %q, want always", got)
	}
}

// TestThemeRowCycles cycles the theme row with l and opens the picker with enter.
func TestThemeRowCycles(t *testing.T) {
	d, path := setup(t, app.Options{})
	d.key(",", "l")
	got := loadSaved(t, path).UI.Theme
	if got == "tokyo-night" || theme.ByName(got) == nil {
		t.Fatalf("l should move to the next theme; got %q", got)
	}
	d.key("enter")
	if !strings.Contains(ansi.Strip(d.view()), "search themes") {
		t.Error("enter on the theme row should open the picker")
	}
}
