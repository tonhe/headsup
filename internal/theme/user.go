package theme

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/charmbracelet/lipgloss"
	toml "github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

// MinContrast is the minimum fg/bg contrast ratio before a warning.
const MinContrast = 4.5

var baseKeys = [16]string{
	"base00", "base01", "base02", "base03", "base04", "base05", "base06", "base07",
	"base08", "base09", "base0a", "base0b", "base0c", "base0d", "base0e", "base0f",
}

// rawTheme is the format-independent intermediate form of a theme file.
type rawTheme struct {
	name, slug string
	light      *bool
	colors     map[string]string // lower-case base key -> raw value
	roles      map[string]string
}

// LoadUserThemes reads *.toml, *.yaml and *.yml in dir. Invalid files are
// skipped with a warning naming the file and the key; valid files with a
// low fg/bg contrast load with a warning. A missing dir is not an error.
func LoadUserThemes(dir string) (loaded []Theme, warnings []string) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, []string{fmt.Sprintf("themes dir %s: %v", dir, err)}
	}
	var files []string
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		switch strings.ToLower(filepath.Ext(e.Name())) {
		case ".toml", ".yaml", ".yml":
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)
	seen := map[string]string{}
	for _, name := range files {
		path := filepath.Join(dir, name)
		t, warns, err := loadThemeFile(path)
		for _, w := range warns {
			warnings = append(warnings, name+": "+w)
		}
		if err != nil {
			warnings = append(warnings, name+": "+err.Error()+" (skipped)")
			continue
		}
		if prev, dup := seen[t.Slug]; dup {
			warnings = append(warnings, fmt.Sprintf("%s: slug %q also defined by %s; this file wins", name, t.Slug, prev))
			for i := range loaded {
				if loaded[i].Slug == t.Slug {
					loaded = append(loaded[:i], loaded[i+1:]...)
					break
				}
			}
		}
		seen[t.Slug] = name
		loaded = append(loaded, t)
	}
	return loaded, warnings
}

// RegisterUserThemes loads dir and registers every valid theme, replacing
// any theme with the same slug. It returns the number registered and the
// load warnings.
func RegisterUserThemes(dir string) (registered int, warnings []string) {
	ts, warnings := LoadUserThemes(dir)
	for _, t := range ts {
		Register(t)
	}
	return len(ts), warnings
}

// LoadThemeFile parses and validates a single theme file.
func LoadThemeFile(path string) (Theme, []string, error) { return loadThemeFile(path) }

func loadThemeFile(path string) (Theme, []string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Theme{}, nil, err
	}
	var raw rawTheme
	switch strings.ToLower(filepath.Ext(path)) {
	case ".toml":
		raw, err = parseTOML(b)
	default:
		raw, err = parseYAML(b)
	}
	if err != nil {
		return Theme{}, nil, err
	}
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	t, warns, err := build(raw, base)
	if err != nil {
		return Theme{}, warns, err
	}
	t.Source = path
	return t, warns, nil
}

func parseTOML(b []byte) (rawTheme, error) {
	var m map[string]any
	if err := toml.NewDecoder(bytes.NewReader(b)).Decode(&m); err != nil {
		return rawTheme{}, fmt.Errorf("parse TOML: %w", err)
	}
	raw := rawTheme{colors: map[string]string{}}
	for k, v := range m {
		lk := strings.ToLower(k)
		switch {
		case lk == "name" || lk == "scheme":
			raw.name = fmt.Sprint(v)
		case lk == "slug":
			raw.slug = fmt.Sprint(v)
		case lk == "light":
			if bv, ok := v.(bool); ok {
				raw.light = &bv
			} else {
				return raw, fmt.Errorf("light must be true or false")
			}
		case lk == "variant":
			setVariant(&raw, fmt.Sprint(v))
		case lk == "roles":
			tbl, ok := v.(map[string]any)
			if !ok {
				return raw, fmt.Errorf("[roles] must be a table")
			}
			raw.roles = map[string]string{}
			for rk, rv := range tbl {
				raw.roles[rk] = fmt.Sprint(rv)
			}
		case lk == "palette":
			if tbl, ok := v.(map[string]any); ok {
				for pk, pv := range tbl {
					raw.colors[strings.ToLower(pk)] = fmt.Sprint(pv)
				}
			}
		case isBaseKey(lk):
			s, ok := v.(string)
			if !ok {
				return raw, fmt.Errorf("%s must be a quoted \"#rrggbb\" string", k)
			}
			raw.colors[lk] = s
		}
	}
	return raw, nil
}

// parseYAML walks the node tree so unquoted values such as 000000 keep their
// literal text instead of becoming integers.
func parseYAML(b []byte) (rawTheme, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return rawTheme{}, fmt.Errorf("parse YAML: %w", err)
	}
	raw := rawTheme{colors: map[string]string{}}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return raw, fmt.Errorf("parse YAML: top level must be a mapping")
	}
	root := doc.Content[0]
	for i := 0; i+1 < len(root.Content); i += 2 {
		k, v := strings.ToLower(root.Content[i].Value), root.Content[i+1]
		switch {
		case k == "scheme" || k == "name":
			if raw.name == "" || k == "name" {
				raw.name = v.Value
			}
		case k == "slug":
			raw.slug = v.Value
		case k == "light":
			bv := strings.EqualFold(v.Value, "true")
			raw.light = &bv
		case k == "variant":
			setVariant(&raw, v.Value)
		case k == "roles" || k == "palette":
			if v.Kind != yaml.MappingNode {
				return raw, fmt.Errorf("%s must be a mapping", k)
			}
			if k == "roles" {
				raw.roles = map[string]string{}
			}
			for j := 0; j+1 < len(v.Content); j += 2 {
				if k == "roles" {
					raw.roles[v.Content[j].Value] = v.Content[j+1].Value
				} else {
					raw.colors[strings.ToLower(v.Content[j].Value)] = v.Content[j+1].Value
				}
			}
		case isBaseKey(k):
			raw.colors[k] = v.Value
		}
	}
	return raw, nil
}

func setVariant(r *rawTheme, v string) {
	switch strings.ToLower(v) {
	case "light":
		b := true
		r.light = &b
	case "dark":
		b := false
		r.light = &b
	}
}

func isBaseKey(k string) bool {
	for _, b := range baseKeys {
		if b == k {
			return true
		}
	}
	return false
}

func build(raw rawTheme, fileBase string) (Theme, []string, error) {
	var warns []string
	var cols [16]lipgloss.Color
	for i, k := range baseKeys {
		v, ok := raw.colors[k]
		if !ok {
			return Theme{}, warns, fmt.Errorf("missing %s", displayKey(k))
		}
		hex, ok := normHex(v)
		if !ok {
			return Theme{}, warns, fmt.Errorf("%s = %q is not a #rrggbb colour", displayKey(k), v)
		}
		cols[i] = lipgloss.Color(hex)
	}
	t := Theme{
		Name:   strings.TrimSpace(raw.name),
		Slug:   strings.TrimSpace(raw.slug),
		Base00: cols[0], Base01: cols[1], Base02: cols[2], Base03: cols[3],
		Base04: cols[4], Base05: cols[5], Base06: cols[6], Base07: cols[7],
		Base08: cols[8], Base09: cols[9], Base0A: cols[10], Base0B: cols[11],
		Base0C: cols[12], Base0D: cols[13], Base0E: cols[14], Base0F: cols[15],
	}
	if t.Name == "" {
		t.Name = fileBase
	}
	if t.Slug == "" {
		t.Slug = Slugify(t.Name)
	} else {
		t.Slug = Slugify(t.Slug)
	}
	if t.Slug == "" {
		t.Slug = Slugify(fileBase)
	}
	if raw.light != nil {
		t.Light = *raw.light
	} else {
		t.Light = luminance(hexToColorful(string(t.Base00))) > 0.5
	}
	if len(raw.roles) > 0 {
		t.Overrides = RoleOverrides{}
		keys := make([]string, 0, len(raw.roles))
		for k := range raw.roles {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := strings.TrimSpace(raw.roles[k])
			rk := snake(k)
			if (&Roles{}).slot(rk) == nil {
				warns = append(warns, fmt.Sprintf("[roles] unknown role %q ignored", k))
				continue
			}
			if hex, ok := normHex(v); ok && strings.HasPrefix(v, "#") {
				v = hex
			}
			if t.resolve(v) == "" {
				warns = append(warns, fmt.Sprintf("[roles] %s = %q is not a base key or #rrggbb; ignored", k, v))
				continue
			}
			t.Overrides[rk] = v
		}
	}
	if c := Contrast(t.Base05, t.Base00); c < MinContrast {
		warns = append(warns, fmt.Sprintf("low contrast between fg (base05) and bg (base00): %.2f:1, want at least %.1f:1", c, MinContrast))
	}
	return t, warns, nil
}

// normHex accepts "rrggbb" or "#rrggbb" and returns lower-case "#rrggbb".
func normHex(v string) (string, bool) {
	v = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(v), "#"))
	if len(v) != 6 {
		return "", false
	}
	for _, c := range v {
		if !unicode.Is(unicode.ASCII_Hex_Digit, c) {
			return "", false
		}
	}
	return "#" + strings.ToLower(v), true
}

func displayKey(k string) string { return "base0" + strings.ToUpper(k[5:]) }

// Slugify turns a display name into a slug: "Midnight Harbor" -> "midnight-harbor".
func Slugify(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if b.Len() > 0 && !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimSuffix(b.String(), "-")
}

// snake normalises a role key: "tagBg", "tag-bg", "Tag BG" -> "tag_bg".
func snake(s string) string {
	var b strings.Builder
	prevLower := false
	for _, r := range strings.TrimSpace(s) {
		switch {
		case r == '-' || r == ' ' || r == '_':
			if b.Len() > 0 {
				b.WriteByte('_')
			}
			prevLower = false
		case unicode.IsUpper(r):
			if prevLower {
				b.WriteByte('_')
			}
			b.WriteRune(unicode.ToLower(r))
			prevLower = false
		default:
			b.WriteRune(r)
			prevLower = true
		}
	}
	return b.String()
}
