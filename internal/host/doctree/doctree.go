// Package doctree edits small structured files (JSON, HuJSON or YAML) in
// place while keeping their format, key spelling, comments and layout of the
// parts that did not change. It backs the extra-records and DERP map file
// models: JSON and HuJSON are edited through the hujson syntax tree (byte
// exact for untouched values), YAML through yaml.v3's node tree (comments
// survive; blank lines between entries are normalised by the encoder).
package doctree

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tailscale/hujson"
	"gopkg.in/yaml.v3"
)

// Format is a file syntax.
type Format int

const (
	JSON Format = iota
	HuJSON
	YAML
)

func (f Format) String() string { return [...]string{"json", "hujson", "yaml"}[f] }

// FormatFor picks the format headscale uses for path, by extension:
// .yaml/.yml are YAML, .hujson is HuJSON, anything else JSON.
func FormatFor(path string) Format {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".yaml", ".yml":
		return YAML
	case ".hujson":
		return HuJSON
	}
	return JSON
}

// Sniff guesses the format of b from its content: valid JSON is JSON,
// anything HuJSON accepts is HuJSON, the rest YAML.
func Sniff(b []byte) Format {
	t := bytes.TrimSpace(b)
	if json.Valid(t) {
		return JSON
	}
	if _, err := hujson.Parse(t); err == nil {
		return HuJSON
	}
	return YAML
}

// Obj is an ordered object literal to insert.
type Obj []KV

// KV is one member of an Obj.
type KV struct {
	K string
	V any // string, int, bool, Obj or Arr
}

// Arr is an array literal to insert.
type Arr []any

// Doc is a parsed file.
type Doc struct {
	f    Format
	hj   *hujson.Value
	y    *yaml.Node // document node
	unit string     // one indentation step
}

// Parse parses b in format f. Empty input is an error; use Empty.
func Parse(b []byte, f Format) (*Doc, error) {
	d := &Doc{f: f}
	if f == YAML {
		var n yaml.Node
		if err := yaml.Unmarshal(b, &n); err != nil {
			return nil, err
		}
		if n.Kind != yaml.DocumentNode || len(n.Content) == 0 {
			return nil, errors.New("empty document")
		}
		d.y = &n
		d.unit = strings.Repeat(" ", yamlIndent(b))
		return d, nil
	}
	v, err := hujson.Parse(b)
	if err != nil {
		return nil, err
	}
	if f == JSON && !json.Valid(b) {
		return nil, errors.New("not valid JSON (comments or trailing commas need a .hujson file)")
	}
	d.hj = &v
	d.unit = jsonIndent(b)
	return d, nil
}

// Empty returns a new document whose root is an empty object (root Obj{})
// or array (Arr{}).
func Empty(f Format, root any) *Doc {
	d := &Doc{f: f, unit: "  "}
	if f == YAML {
		d.y = &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{yamlValue(root)}}
		return d
	}
	b := []byte("{}\n")
	if _, ok := root.(Arr); ok {
		b = []byte("[]\n")
	}
	v, _ := hujson.Parse(b)
	d.hj = &v
	return d
}

// Format returns the document's format.
func (d *Doc) Format() Format { return d.f }

// Root returns the top-level value.
func (d *Doc) Root() Node {
	if d.y != nil {
		return Node{d: d, y: d.y.Content[0]}
	}
	return Node{d: d, hj: d.hj}
}

// Bytes serialises the document.
func (d *Doc) Bytes() ([]byte, error) {
	if d.y != nil {
		var buf bytes.Buffer
		enc := yaml.NewEncoder(&buf)
		enc.SetIndent(len(d.unit))
		if err := enc.Encode(d.y); err != nil {
			return nil, err
		}
		if err := enc.Close(); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}
	return d.hj.Pack(), nil
}

func yamlIndent(b []byte) int {
	for _, l := range strings.Split(string(b), "\n") {
		t := strings.TrimLeft(l, " ")
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if n := len(l) - len(t); n > 0 {
			return min(max(n, 2), 8)
		}
	}
	return 2
}

func jsonIndent(b []byte) string {
	for _, l := range strings.Split(string(b), "\n") {
		t := strings.TrimLeft(l, " \t")
		if t == "" || strings.HasPrefix(t, "//") {
			continue
		}
		if n := len(l) - len(t); n > 0 {
			return l[:n]
		}
	}
	return "  "
}

// Node is a value inside a Doc. The zero Node is invalid.
type Node struct {
	d   *Doc
	hj  *hujson.Value
	y   *yaml.Node
	ind string // indentation of the line this value starts on (JSON)
}

// Valid reports whether n refers to a value.
func (n Node) Valid() bool { return n.hj != nil || n.y != nil }

// IsObject reports whether n is an object / mapping.
func (n Node) IsObject() bool {
	if n.y != nil {
		return n.y.Kind == yaml.MappingNode
	}
	if n.hj != nil {
		_, ok := n.hj.Value.(*hujson.Object)
		return ok
	}
	return false
}

// IsArray reports whether n is an array / sequence.
func (n Node) IsArray() bool {
	if n.y != nil {
		return n.y.Kind == yaml.SequenceNode
	}
	if n.hj != nil {
		_, ok := n.hj.Value.(*hujson.Array)
		return ok
	}
	return false
}

// IsNull reports whether n is null (or an empty YAML scalar).
func (n Node) IsNull() bool {
	if n.y != nil {
		return n.y.Kind == yaml.ScalarNode && n.y.Tag == "!!null"
	}
	if n.hj != nil {
		l, ok := n.hj.Value.(hujson.Literal)
		return ok && l.Kind() == 'n'
	}
	return true
}

// Len is the number of elements or members.
func (n Node) Len() int {
	switch {
	case n.y != nil && n.y.Kind == yaml.MappingNode:
		return len(n.y.Content) / 2
	case n.y != nil && n.y.Kind == yaml.SequenceNode:
		return len(n.y.Content)
	case n.hj != nil:
		switch v := n.hj.Value.(type) {
		case *hujson.Object:
			return len(v.Members)
		case *hujson.Array:
			return len(v.Elements)
		}
	}
	return 0
}

func childIndent(parent string, before []byte) string {
	if i := bytes.LastIndexByte(before, '\n'); i >= 0 {
		return string(before[i+1:])
	}
	return parent
}

// At returns array element i.
func (n Node) At(i int) Node {
	if n.y != nil && n.y.Kind == yaml.SequenceNode && i >= 0 && i < len(n.y.Content) {
		return Node{d: n.d, y: n.y.Content[i]}
	}
	if n.hj != nil {
		if a, ok := n.hj.Value.(*hujson.Array); ok && i >= 0 && i < len(a.Elements) {
			e := &a.Elements[i]
			return Node{d: n.d, hj: e, ind: childIndent(n.ind, e.BeforeExtra)}
		}
	}
	return Node{}
}

// Keys returns member names in document order.
func (n Node) Keys() []string {
	var out []string
	if n.y != nil && n.y.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.y.Content); i += 2 {
			out = append(out, n.y.Content[i].Value)
		}
	}
	if n.hj != nil {
		if o, ok := n.hj.Value.(*hujson.Object); ok {
			for _, m := range o.Members {
				out = append(out, litString(m.Name.Value))
			}
		}
	}
	return out
}

func (n Node) memberIndex(key string) int {
	if n.y != nil && n.y.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.y.Content); i += 2 {
			if strings.EqualFold(n.y.Content[i].Value, key) {
				return i / 2
			}
		}
	}
	if n.hj != nil {
		if o, ok := n.hj.Value.(*hujson.Object); ok {
			for i, m := range o.Members {
				if strings.EqualFold(litString(m.Name.Value), key) {
					return i
				}
			}
		}
	}
	return -1
}

// Get returns the member named key, matched case-insensitively (Go's JSON
// decoder and headscale's YAML loader both ignore key case).
func (n Node) Get(key string) (Node, bool) {
	i := n.memberIndex(key)
	if i < 0 {
		return Node{}, false
	}
	if n.y != nil {
		return Node{d: n.d, y: n.y.Content[2*i+1]}, true
	}
	m := &n.hj.Value.(*hujson.Object).Members[i]
	return Node{d: n.d, hj: &m.Value, ind: childIndent(n.ind, m.Name.BeforeExtra)}, true
}

// Str returns a scalar as text ("" for null and composites).
func (n Node) Str() string {
	if n.y != nil {
		if n.y.Kind != yaml.ScalarNode || n.y.Tag == "!!null" {
			return ""
		}
		return n.y.Value
	}
	if n.hj != nil {
		return litString(n.hj.Value)
	}
	return ""
}

// Int parses a scalar as an integer.
func (n Node) Int() (int, bool) {
	s := n.Str()
	if s == "" {
		return 0, false
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		f, err := strconv.ParseFloat(s, 64)
		if err != nil || f != float64(int(f)) {
			return 0, false
		}
		return int(f), true
	}
	return v, true
}

// Bool parses a scalar as a boolean.
func (n Node) Bool() bool {
	switch strings.ToLower(n.Str()) {
	case "true", "yes", "on":
		return true
	}
	return false
}

func litString(v hujson.ValueTrimmed) string {
	l, ok := v.(hujson.Literal)
	if !ok {
		return ""
	}
	switch l.Kind() {
	case '"':
		return l.String()
	case 'n':
		return ""
	}
	return string(l)
}

// ErrNotComposite means the edit targets a scalar.
var ErrNotComposite = errors.New("doctree: not an object or array")

// Set sets member key to v, replacing an existing member (keeping its key
// spelling and surrounding comments) or appending a new one.
func (n Node) Set(key string, v any) error {
	if !n.IsObject() {
		if n.IsNull() && n.Valid() {
			n.becomeEmpty(Obj{})
		} else {
			return ErrNotComposite
		}
	}
	if i := n.memberIndex(key); i >= 0 {
		if n.y != nil {
			setYAML(n.y.Content[2*i+1], v)
			return nil
		}
		m := &n.hj.Value.(*hujson.Object).Members[i]
		return n.setHJ(&m.Value, v, childIndent(n.ind, m.Name.BeforeExtra))
	}
	if n.y != nil {
		k := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
		if _, err := strconv.Atoi(key); err == nil && (len(n.y.Content) == 0 || n.y.Content[0].Tag == "!!int") {
			k.Tag = "!!int"
		}
		if len(n.y.Content) == 0 {
			n.y.Style &^= yaml.FlowStyle
		}
		n.y.Content = append(n.y.Content, k, yamlValue(v))
		return nil
	}
	o := n.hj.Value.(*hujson.Object)
	trail := len(o.Members) > 0 && o.Members[len(o.Members)-1].Value.AfterExtra != nil
	sep, ind, compact := n.newSep(func(i int) ([]byte, bool) {
		if i < 0 || i >= len(o.Members) {
			return nil, false
		}
		return o.Members[i].Name.BeforeExtra, true
	}, len(o.Members))
	val, err := n.parseHJ(v, ind, compact)
	if err != nil {
		return err
	}
	if len(o.Members) == 0 && !compact {
		o.AfterExtra = []byte("\n" + n.ind)
	}
	val.BeforeExtra = []byte(" ")
	o.Members = append(o.Members, hujson.ObjectMember{
		Name:  hujson.Value{BeforeExtra: sep, Value: hujson.String(key)},
		Value: val,
	})
	fixTrailing(lastOfObj(o), &o.AfterExtra, trail && n.d.f == HuJSON)
	return nil
}

// Delete removes member key; it reports whether it existed.
func (n Node) Delete(key string) bool {
	i := n.memberIndex(key)
	if i < 0 {
		return false
	}
	if n.y != nil {
		n.y.Content = append(n.y.Content[:2*i], n.y.Content[2*i+2:]...)
		return true
	}
	o := n.hj.Value.(*hujson.Object)
	trail := o.Members[len(o.Members)-1].Value.AfterExtra != nil
	o.Members = append(o.Members[:i], o.Members[i+1:]...)
	if len(o.Members) == 0 {
		o.AfterExtra = nil
		return true
	}
	fixTrailing(lastOfObj(o), &o.AfterExtra, trail && n.d.f == HuJSON)
	return true
}

// Append adds v at the end of an array.
func (n Node) Append(v any) error {
	if !n.IsArray() {
		if n.IsNull() && n.Valid() {
			n.becomeEmpty(Arr{})
		} else {
			return ErrNotComposite
		}
	}
	if n.y != nil {
		if len(n.y.Content) == 0 {
			n.y.Style &^= yaml.FlowStyle
		}
		n.y.Content = append(n.y.Content, yamlValue(v))
		return nil
	}
	a := n.hj.Value.(*hujson.Array)
	trail := len(a.Elements) > 0 && a.Elements[len(a.Elements)-1].AfterExtra != nil
	sep, ind, compact := n.newSep(func(i int) ([]byte, bool) {
		if i < 0 || i >= len(a.Elements) {
			return nil, false
		}
		return a.Elements[i].BeforeExtra, true
	}, len(a.Elements))
	val, err := n.parseHJ(v, ind, compact)
	if err != nil {
		return err
	}
	if len(a.Elements) == 0 {
		a.AfterExtra = []byte("\n" + n.ind)
	}
	val.BeforeExtra = sep
	a.Elements = append(a.Elements, val)
	fixTrailing(lastOfArr(a), &a.AfterExtra, trail && n.d.f == HuJSON)
	return nil
}

// SetAt replaces array element i with v, keeping its leading comments.
func (n Node) SetAt(i int, v any) error {
	if !n.IsArray() || i < 0 || i >= n.Len() {
		return fmt.Errorf("doctree: no element %d", i)
	}
	if n.y != nil {
		setYAML(n.y.Content[i], v)
		return nil
	}
	e := &n.hj.Value.(*hujson.Array).Elements[i]
	return n.setHJ(e, v, childIndent(n.ind, e.BeforeExtra))
}

// RemoveAt deletes array element i.
func (n Node) RemoveAt(i int) error {
	if !n.IsArray() || i < 0 || i >= n.Len() {
		return fmt.Errorf("doctree: no element %d", i)
	}
	if n.y != nil {
		n.y.Content = append(n.y.Content[:i], n.y.Content[i+1:]...)
		return nil
	}
	a := n.hj.Value.(*hujson.Array)
	trail := a.Elements[len(a.Elements)-1].AfterExtra != nil
	a.Elements = append(a.Elements[:i], a.Elements[i+1:]...)
	if len(a.Elements) == 0 {
		a.AfterExtra = nil
		return nil
	}
	fixTrailing(lastOfArr(a), &a.AfterExtra, trail && n.d.f == HuJSON)
	return nil
}

func (n Node) becomeEmpty(v any) {
	if n.y != nil {
		*n.y = *yamlValue(v)
		return
	}
	if _, ok := v.(Arr); ok {
		n.hj.Value = &hujson.Array{}
	} else {
		n.hj.Value = &hujson.Object{}
	}
}

func lastOfObj(o *hujson.Object) *hujson.Value {
	if len(o.Members) == 0 {
		return nil
	}
	return &o.Members[len(o.Members)-1].Value
}

func lastOfArr(a *hujson.Array) *hujson.Value {
	if len(a.Elements) == 0 {
		return nil
	}
	return &a.Elements[len(a.Elements)-1]
}

// fixTrailing makes the composite end with (want) or without a trailing
// comma; a non-nil AfterExtra on the last value means "trailing comma".
func fixTrailing(last *hujson.Value, after *hujson.Extra, want bool) {
	if last == nil {
		return
	}
	switch {
	case want && last.AfterExtra == nil:
		last.AfterExtra = []byte{}
	case !want && last.AfterExtra != nil:
		*after = append(append(hujson.Extra{}, last.AfterExtra...), *after...)
		last.AfterExtra = nil
	}
}

// newSep works out the whitespace before a new member or element, its
// indentation, and whether a new composite value should be written on one
// line, by copying the last sibling.
func (n Node) newSep(before func(int) ([]byte, bool), count int) ([]byte, string, bool) {
	if b, ok := before(count - 1); ok {
		if i := bytes.LastIndexByte(b, '\n'); i >= 0 {
			ind := string(b[i+1:])
			return []byte("\n" + ind), ind, n.siblingCompact(count - 1)
		}
		return []byte(" "), n.ind, true
	}
	ind := n.ind + n.d.unit
	return []byte("\n" + ind), ind, false
}

// siblingCompact reports whether element/member i's value fits on one line.
func (n Node) siblingCompact(i int) bool {
	var v *hujson.Value
	switch c := n.hj.Value.(type) {
	case *hujson.Array:
		v = &c.Elements[i]
	case *hujson.Object:
		v = &c.Members[i].Value
	}
	if v == nil {
		return false
	}
	if _, lit := v.Value.(hujson.Literal); lit {
		return false
	}
	t := hujson.Value{Value: v.Value}
	return !bytes.ContainsRune(t.Pack(), '\n')
}

func (n Node) setHJ(dst *hujson.Value, v any, ind string) error {
	compact := false
	if _, lit := dst.Value.(hujson.Literal); !lit {
		t := hujson.Value{Value: dst.Value}
		compact = !bytes.ContainsRune(t.Pack(), '\n')
	}
	val, err := n.parseHJ(v, ind, compact)
	if err != nil {
		return err
	}
	dst.Value = val.Value
	return nil
}

func (n Node) parseHJ(v any, ind string, compact bool) (hujson.Value, error) {
	var b strings.Builder
	renderJSON(&b, v, ind, n.d.unit, compact)
	return hujson.Parse([]byte(b.String()))
}

// flat reports whether an Obj has only scalar members.
func flat(o Obj) bool {
	for _, kv := range o {
		switch kv.V.(type) {
		case Obj, Arr:
			return false
		}
	}
	return true
}

func renderJSON(b *strings.Builder, v any, ind, unit string, compact bool) {
	switch x := v.(type) {
	case Obj:
		if len(x) == 0 {
			b.WriteString("{}")
			return
		}
		one := compact || (flat(x) && len(x) <= 3)
		b.WriteString("{")
		for i, kv := range x {
			if i > 0 {
				b.WriteString(",")
			}
			if one {
				if i > 0 {
					b.WriteString(" ")
				}
			} else {
				b.WriteString("\n" + ind + unit)
			}
			kb, _ := json.Marshal(kv.K)
			b.Write(kb)
			b.WriteString(": ")
			renderJSON(b, kv.V, ind+unit, unit, one)
		}
		if !one {
			b.WriteString("\n" + ind)
		}
		b.WriteString("}")
	case Arr:
		if len(x) == 0 {
			b.WriteString("[]")
			return
		}
		b.WriteString("[")
		for i, e := range x {
			if i > 0 {
				b.WriteString(",")
			}
			if compact {
				if i > 0 {
					b.WriteString(" ")
				}
			} else {
				b.WriteString("\n" + ind + unit)
			}
			renderJSON(b, e, ind+unit, unit, compact)
		}
		if !compact {
			b.WriteString("\n" + ind)
		}
		b.WriteString("]")
	case []string:
		a := make(Arr, len(x))
		for i, s := range x {
			a[i] = s
		}
		renderJSON(b, a, ind, unit, compact)
	default:
		jb, err := json.Marshal(x)
		if err != nil {
			jb = []byte("null")
		}
		b.Write(jb)
	}
}

func yamlValue(v any) *yaml.Node {
	switch x := v.(type) {
	case Obj:
		n := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		for _, kv := range x {
			k := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: kv.K}
			if _, err := strconv.Atoi(kv.K); err == nil {
				k.Tag = "!!int"
			}
			n.Content = append(n.Content, k, yamlValue(kv.V))
		}
		return n
	case Arr:
		n := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, e := range x {
			n.Content = append(n.Content, yamlValue(e))
		}
		return n
	case []string:
		a := make(Arr, len(x))
		for i, s := range x {
			a[i] = s
		}
		return yamlValue(a)
	case string:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: x}
	case int:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(x)}
	case bool:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(x)}
	case nil:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}
	}
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: fmt.Sprint(v)}
}

// setYAML replaces dst's value with v, keeping dst's comments and, for a
// scalar replaced by a scalar of the same kind, its quoting style.
func setYAML(dst *yaml.Node, v any) {
	nv := yamlValue(v)
	nv.HeadComment, nv.LineComment, nv.FootComment = dst.HeadComment, dst.LineComment, dst.FootComment
	if dst.Kind == yaml.ScalarNode && nv.Kind == yaml.ScalarNode && dst.Tag == nv.Tag {
		nv.Style = dst.Style
	}
	if dst.Kind == nv.Kind && dst.Kind != yaml.ScalarNode && dst.Style&yaml.FlowStyle != 0 && len(nv.Content) > 0 {
		nv.Style = dst.Style
	}
	*dst = *nv
}
