package serverconfig

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/host/hsconfig"
	"github.com/tonhe/headsup/internal/icons"
	"github.com/tonhe/headsup/internal/module"
)

// section is one tab of the editor. Link sections hand off to another
// module; view-only sections list values but refuse field edits.
type section struct {
	name     string
	link     string // module ID to navigate to
	chord    string // that module's jump key, for the hint
	viewOnly bool
}

var sections = []section{
	{name: hsconfig.SecGeneral},
	{name: hsconfig.SecNodes},
	{name: hsconfig.SecDNS, link: "dns", chord: "g d"},
	{name: hsconfig.SecDERP, link: "derp", chord: "g D"},
	{name: hsconfig.SecTLS},
	{name: hsconfig.SecOIDC},
	{name: hsconfig.SecPolicy},
	{name: hsconfig.SecDatabase, viewOnly: true},
	{name: hsconfig.SecLogging},
	{name: hsconfig.SecFeatures},
}

// row is one field as the editor shows it.
type row struct {
	field      hsconfig.Field
	present    bool   // the key is set in the staged document
	value      string // editable text form of the staged value
	modified   bool   // differs from the file as opened
	deprecated bool   // present but deprecated or removed for this version
}

// rowsFor lists a section's fields for version: the catalog's current keys
// plus any deprecated key the document still carries.
func rowsFor(sec string, version string, doc, base *hsconfig.Doc) []row {
	current := map[string]bool{}
	for _, x := range hsconfig.Catalog(version) {
		if x.Section == sec {
			current[x.Key] = true
		}
	}
	deps := hsconfig.Deprecations(version)
	var out []row
	for _, x := range hsconfig.All() {
		if x.Section != sec {
			continue
		}
		_, isDep := deps[x.Key]
		present := doc != nil && doc.Has(x.Key)
		if !current[x.Key] && !(isDep && present) {
			continue
		}
		r := row{field: x, present: present, deprecated: isDep && present}
		if present {
			r.value = valueText(doc, x)
		}
		if base != nil {
			bp := base.Has(x.Key)
			r.modified = bp != present || (present && valueText(base, x) != r.value)
		}
		out = append(out, r)
	}
	return out
}

// valueText is the editable text of a key: lists comma-separated, maps as
// "key=value; key=a b".
func valueText(d *hsconfig.Doc, x hsconfig.Field) string {
	switch x.Type {
	case hsconfig.TypeList:
		l, _ := d.List(x.Key)
		return strings.Join(l, ", ")
	case hsconfig.TypeMap:
		m, _ := d.Map(x.Key)
		return mapText(m)
	}
	v, _ := d.Get(x.Key)
	return v
}

func mapText(m []hsconfig.MapEntry) string {
	parts := make([]string, 0, len(m))
	for _, e := range m {
		v := e.Value
		if e.List {
			v = strings.Join(e.Values, " ")
		}
		parts = append(parts, e.Key+"="+v)
	}
	return strings.Join(parts, "; ")
}

// defaultText renders a catalog default for display.
func defaultText(x hsconfig.Field) string {
	switch d := x.Default.(type) {
	case nil:
		return "(none)"
	case string:
		if d == "" {
			return "(empty)"
		}
		return d
	case []string:
		if len(d) == 0 {
			return "[]"
		}
		return strings.Join(d, ", ")
	case map[string]string, map[string][]string:
		return "{}"
	default:
		return fmt.Sprint(d)
	}
}

// mask hides a secret value.
func mask(ic icons.Set, v string) string {
	if v == "" {
		return ""
	}
	if ic.Name == "ascii" {
		return "********"
	}
	return "••••••••"
}

// displayValue is what the row shows for r: masked for secrets.
func displayValue(ic icons.Set, r row) string {
	if !r.present {
		return ""
	}
	if r.field.Type == hsconfig.TypeSecret {
		if r.value == "" {
			return "(empty)"
		}
		return mask(ic, r.value)
	}
	if r.value == "" {
		switch r.field.Type {
		case hsconfig.TypeList:
			return "[]"
		case hsconfig.TypeMap:
			return "{}"
		}
		return `""`
	}
	return r.value
}

// effectLabel is the badge text for when a change to x takes effect.
func effectLabel(e host.Effect) string {
	if e == host.EffectLive {
		return "applies on save"
	}
	return e.String()
}

func typeLabel(x hsconfig.Field) string {
	switch x.Type {
	case hsconfig.TypeOctal:
		return "quoted octal"
	case hsconfig.TypeList:
		return "list"
	case hsconfig.TypeMap:
		return "map"
	}
	return x.Type.String()
}

// --- editors ----------------------------------------------------------------

// editForm builds the form for editing x whose staged text is cur.
func editForm(x hsconfig.Field, cur string, present bool) *module.FormSpec {
	f := module.Field{ID: "value", Label: shortLabel(x.Key), Value: cur, Validate: validator(x),
		Help: x.Help + "; default " + defaultText(x)}
	if !present {
		f.Value = seedDefault(x)
	}
	switch x.Type {
	case hsconfig.TypeBool:
		f.Kind = module.FieldToggle
	case hsconfig.TypeEnum:
		f.Kind, f.Options = module.FieldSelect, slices.Clone(x.Enum)
		if !slices.Contains(f.Options, f.Value) && f.Value != "" {
			f.Options = append([]string{f.Value}, f.Options...)
		}
		if f.Value == "" && len(f.Options) > 0 {
			f.Value = f.Options[0]
		}
	case hsconfig.TypeSecret:
		f.Kind, f.Value = module.FieldSecret, ""
		f.Placeholder = "leave empty to keep the current value"
		if !present || cur == "" {
			f.Placeholder = "not set"
		}
	case hsconfig.TypeDuration:
		f.Placeholder, f.Required = "e.g. 30m, 12h, 180d; 0 = never", true
	case hsconfig.TypeInt:
		f.Required = true
	case hsconfig.TypeOctal:
		f.Placeholder, f.Required = "e.g. 0770 (written quoted)", true
	case hsconfig.TypeList:
		f.Placeholder = "comma-separated; empty for none"
	case hsconfig.TypeMap:
		f.Placeholder = "key=value; key2=value"
		if mapOfLists(x) {
			f.Placeholder = "domain=10.0.0.53 10.0.0.54; other=1.1.1.1"
		}
	case hsconfig.TypePath:
		f.Placeholder = "absolute path"
	default:
		if hsconfig.CheckValue(x, "") != nil {
			f.Required = true
		}
	}
	return &module.FormSpec{Title: "Edit " + x.Key, Submit: "Stage", Fields: []module.Field{f}}
}

func shortLabel(key string) string {
	if i := strings.LastIndexByte(key, '.'); i >= 0 {
		return key[i+1:]
	}
	return key
}

func seedDefault(x hsconfig.Field) string {
	switch d := x.Default.(type) {
	case string:
		return d
	case []string:
		return strings.Join(d, ", ")
	case bool, int:
		return fmt.Sprint(d)
	}
	return ""
}

func mapOfLists(x hsconfig.Field) bool {
	_, ok := x.Default.(map[string][]string)
	return ok
}

// validator checks form input for x, with messages that say what to type.
func validator(x hsconfig.Field) func(string) error {
	return func(s string) error {
		switch x.Type {
		case hsconfig.TypeSecret:
			return nil
		case hsconfig.TypeList:
			items := splitList(s)
			if x.Validate != nil {
				if err := x.Validate(strings.Join(items, "\n")); err != nil {
					return err
				}
			}
			return nil
		case hsconfig.TypeMap:
			_, err := parseMap(s, mapOfLists(x))
			return err
		case hsconfig.TypePath:
			if strings.ContainsAny(s, "\n\x00") {
				return errors.New("a path cannot contain a newline")
			}
			if strings.HasPrefix(s, "~") {
				return errors.New("headscale does not expand ~; use an absolute path")
			}
		case hsconfig.TypeOctal:
			if err := hsconfig.CheckValue(x, s); err != nil {
				return fmt.Errorf("%q is not octal permission bits, e.g. 0770", s)
			}
			return nil
		}
		if err := hsconfig.CheckValue(x, s); err != nil {
			// Drop the "key: " prefix: the form already shows the field.
			return errors.New(strings.TrimPrefix(err.Error(), x.Key+": "))
		}
		return nil
	}
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '\n' || r == ' ' || r == '\t' }) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// parseMap reads "k=v; k2=v2" (entries may also be newline-separated).
func parseMap(s string, lists bool) ([]hsconfig.MapEntry, error) {
	var out []hsconfig.MapEntry
	seen := map[string]bool{}
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == ';' || r == '\n' }) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if !ok || k == "" {
			return nil, fmt.Errorf("%q: expected key=value", part)
		}
		if seen[k] {
			return nil, fmt.Errorf("%q appears twice", k)
		}
		seen[k] = true
		e := hsconfig.MapEntry{Key: k, Value: v, List: lists}
		if lists {
			e.Value, e.Values = "", splitList(v)
		}
		out = append(out, e)
	}
	return out, nil
}

// normalizeOctal adds the leading 0 to three-digit permission bits.
func normalizeOctal(s string) string {
	if len(s) == 3 {
		if _, err := strconv.ParseUint(s, 8, 32); err == nil {
			return "0" + s
		}
	}
	return s
}

// stage writes raw (form input) for x into d. It reports whether the
// document changed.
func stage(d *hsconfig.Doc, x hsconfig.Field, raw string) (bool, error) {
	if err := validator(x)(raw); err != nil {
		return false, fmt.Errorf("%s: %w", x.Key, err)
	}
	if d.Has(x.Key) && sameValue(d, x, raw) {
		return false, nil
	}
	before := string(d.Bytes())
	var err error
	switch x.Type {
	case hsconfig.TypeSecret:
		if raw == "" {
			return false, nil
		}
		err = d.Set(x.Key, raw)
	case hsconfig.TypeList:
		err = d.SetList(x.Key, splitList(raw))
	case hsconfig.TypeMap:
		var m []hsconfig.MapEntry
		if m, err = parseMap(raw, mapOfLists(x)); err == nil {
			err = d.SetMap(x.Key, m)
		}
	case hsconfig.TypeOctal:
		err = d.Set(x.Key, normalizeOctal(strings.TrimSpace(raw)))
	default:
		err = d.Set(x.Key, strings.TrimSpace(raw))
	}
	if err != nil {
		return false, fmt.Errorf("%s: %w", x.Key, err)
	}
	return string(d.Bytes()) != before, nil
}

// sameValue reports whether raw says what the document already holds, so
// re-submitting a value never rewrites its quoting.
func sameValue(d *hsconfig.Doc, x hsconfig.Field, raw string) bool {
	cur := valueText(d, x)
	switch x.Type {
	case hsconfig.TypeList:
		return strings.Join(splitList(raw), ", ") == cur
	case hsconfig.TypeMap:
		m, err := parseMap(raw, mapOfLists(x))
		return err == nil && mapText(m) == cur
	case hsconfig.TypeOctal:
		return normalizeOctal(strings.TrimSpace(raw)) == cur
	case hsconfig.TypeSecret:
		return raw == cur
	}
	return strings.TrimSpace(raw) == cur
}

// secretLeaves are the last key segments of secret fields, for masking
// diff lines.
var secretLeaves = func() []string {
	var out []string
	for _, x := range hsconfig.All() {
		if x.Type == hsconfig.TypeSecret {
			out = append(out, shortLabel(x.Key))
		}
	}
	sort.Strings(out)
	return slices.Compact(out)
}()

// maskLine hides the value on a YAML line that sets a secret key.
func maskLine(ic icons.Set, line string) string {
	t := strings.TrimLeft(line, " ")
	for _, leaf := range secretLeaves {
		if !strings.HasPrefix(t, leaf+":") {
			continue
		}
		v := strings.TrimSpace(t[len(leaf)+1:])
		if v == "" || v == `""` || v == "''" || strings.HasPrefix(v, "#") {
			return line
		}
		return line[:len(line)-len(t)] + leaf + ": " + mask(ic, v)
	}
	return line
}
