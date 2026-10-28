// Package hsconfig is a typed, comment-preserving model of headscale's
// config.yaml. It parses with yaml.v3's Node tree to locate keys and applies
// edits as minimal text splices on the original bytes, so comments, blank
// lines, key order and quoting survive and editing one key changes one line.
package hsconfig

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// ErrFlowStyle means the edit targets the inside of a non-empty flow
// collection ({a: 1} or [a, b]), which this editor does not rewrite.
var ErrFlowStyle = errors.New("hsconfig: cannot edit inside a flow-style collection")

// Doc is a parsed config file.
type Doc struct {
	src   []byte
	root  *yaml.Node // top-level mapping, nil for an empty document
	lines []int      // byte offset of each line start
}

// Parse parses a config document. An empty document is valid.
func Parse(b []byte) (*Doc, error) {
	d := &Doc{src: append([]byte(nil), b...)}
	if err := d.reparse(); err != nil {
		return nil, err
	}
	return d, nil
}

func (d *Doc) reparse() error {
	var n yaml.Node
	if err := yaml.Unmarshal(d.src, &n); err != nil {
		return err
	}
	d.root = nil
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		r := n.Content[0]
		switch {
		case r.Kind == yaml.MappingNode:
			d.root = r
		case r.Kind == yaml.ScalarNode && r.Tag == "!!null":
		default:
			return fmt.Errorf("hsconfig: top level must be a mapping, got %s", kindName(r.Kind))
		}
	}
	d.lines = d.lines[:0]
	d.lines = append(d.lines, 0)
	for i, c := range d.src {
		if c == '\n' {
			d.lines = append(d.lines, i+1)
		}
	}
	return nil
}

func kindName(k yaml.Kind) string {
	switch k {
	case yaml.SequenceNode:
		return "sequence"
	case yaml.ScalarNode:
		return "scalar"
	case yaml.AliasNode:
		return "alias"
	}
	return "mapping"
}

// Bytes returns the current document text.
func (d *Doc) Bytes() []byte { return append([]byte(nil), d.src...) }

// Root returns the top-level mapping node (nil when empty). Callers must not modify it.
func (d *Doc) Root() *yaml.Node { return d.root }

// --- lookup -----------------------------------------------------------------

type hit struct {
	key, val *yaml.Node
	parent   *yaml.Node // mapping containing key
}

// find resolves segs greedily: at each mapping the longest run of segments
// matching an existing key wins, so keys containing dots (split DNS
// domains) are addressable with dotted paths.
func (d *Doc) find(segs []string) (path []hit, rest []string) {
	m := d.root
	for len(segs) > 0 && m != nil && m.Kind == yaml.MappingNode {
		found := false
		for n := len(segs); n >= 1 && !found; n-- {
			name := strings.Join(segs[:n], ".")
			for i := 0; i+1 < len(m.Content); i += 2 {
				if m.Content[i].Value == name {
					h := hit{key: m.Content[i], val: m.Content[i+1], parent: m}
					path = append(path, h)
					segs = segs[n:]
					m = h.val
					found = true
					break
				}
			}
		}
		if !found {
			break
		}
	}
	return path, segs
}

func (d *Doc) lookup(key string) (hit, bool) {
	if key == "" {
		return hit{}, false
	}
	path, rest := d.find(strings.Split(key, "."))
	if len(rest) > 0 || len(path) == 0 {
		return hit{}, false
	}
	return path[len(path)-1], true
}

// Node returns the value node for key.
func (d *Doc) Node(key string) (*yaml.Node, bool) {
	h, ok := d.lookup(key)
	return h.val, ok
}

// Has reports whether key is present (even with a null value).
func (d *Doc) Has(key string) bool { _, ok := d.lookup(key); return ok }

// Line returns the 1-based line of key, or 0.
func (d *Doc) Line(key string) int {
	if h, ok := d.lookup(key); ok {
		return h.key.Line
	}
	return 0
}

// Get returns a scalar value. Sequences of scalars are joined with ","; a
// mapping returns "" and true; a null value returns "" and true.
func (d *Doc) Get(key string) (string, bool) {
	h, ok := d.lookup(key)
	if !ok {
		return "", false
	}
	switch h.val.Kind {
	case yaml.ScalarNode:
		if h.val.Tag == "!!null" {
			return "", true
		}
		return h.val.Value, true
	case yaml.SequenceNode:
		l, _ := d.List(key)
		return strings.Join(l, ","), true
	}
	return "", true
}

// List returns a sequence of scalars. A scalar value is returned as a
// one-element list; null as an empty one.
func (d *Doc) List(key string) ([]string, bool) {
	h, ok := d.lookup(key)
	if !ok {
		return nil, false
	}
	switch h.val.Kind {
	case yaml.SequenceNode:
		out := make([]string, 0, len(h.val.Content))
		for _, c := range h.val.Content {
			if c.Kind == yaml.ScalarNode {
				out = append(out, c.Value)
			}
		}
		return out, true
	case yaml.ScalarNode:
		if h.val.Tag == "!!null" || h.val.Value == "" {
			return nil, true
		}
		return []string{h.val.Value}, true
	}
	return nil, true
}

// Keys returns the flattened dotted paths of every scalar or sequence leaf,
// mapped to their Get value. Used to compute which keys a change touches.
func (d *Doc) Keys() map[string]string {
	out := map[string]string{}
	var walk func(prefix string, n *yaml.Node)
	walk = func(prefix string, n *yaml.Node) {
		if n == nil {
			return
		}
		if n.Kind == yaml.MappingNode {
			for i := 0; i+1 < len(n.Content); i += 2 {
				k := n.Content[i].Value
				if prefix != "" {
					k = prefix + "." + k
				}
				walk(k, n.Content[i+1])
			}
			return
		}
		switch n.Kind {
		case yaml.SequenceNode:
			var parts []string
			for _, c := range n.Content {
				if c.Kind == yaml.ScalarNode {
					parts = append(parts, c.Value)
				} else {
					b, _ := yaml.Marshal(c)
					parts = append(parts, string(b))
				}
			}
			out[prefix] = strings.Join(parts, "\n")
		case yaml.ScalarNode:
			if n.Tag == "!!null" {
				out[prefix] = ""
			} else {
				out[prefix] = n.Value
			}
		default:
			out[prefix] = n.Value
		}
	}
	walk("", d.root)
	return out
}

// --- positions --------------------------------------------------------------

// off converts a 1-based line and (rune) column to a byte offset.
func (d *Doc) off(line, col int) int {
	if line < 1 {
		return 0
	}
	if line > len(d.lines) {
		return len(d.src)
	}
	o := d.lines[line-1]
	for c := 1; c < col && o < len(d.src) && d.src[o] != '\n'; c++ {
		_, sz := utf8.DecodeRune(d.src[o:])
		o += sz
	}
	return o
}

func (d *Doc) lineEnd(line int) int {
	if line >= len(d.lines) {
		return len(d.src)
	}
	return d.lines[line] - 1 // position of '\n'
}

func (d *Doc) lineOf(off int) int {
	lo, hi := 0, len(d.lines)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if d.lines[mid] <= off {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo + 1
}

func (d *Doc) lineText(line int) string {
	if line < 1 || line > len(d.lines) {
		return ""
	}
	return string(d.src[d.lines[line-1]:d.lineEnd(line)])
}

func indentOf(s string) int { return len(s) - len(strings.TrimLeft(s, " ")) }

func isEmptyNull(n *yaml.Node) bool {
	return n.Kind == yaml.ScalarNode && n.Tag == "!!null" && n.Value == "" && n.Style == 0
}

func isBlockScalar(n *yaml.Node) bool {
	return n.Kind == yaml.ScalarNode && n.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0
}

func isFlow(n *yaml.Node) bool {
	return (n.Kind == yaml.MappingNode || n.Kind == yaml.SequenceNode) && n.Style&yaml.FlowStyle != 0
}

// scalarEnd returns the byte offset just past a single-line scalar starting at start.
func (d *Doc) scalarEnd(n *yaml.Node, start int) int {
	s := d.src
	switch {
	case n.Style&yaml.DoubleQuotedStyle != 0:
		for i := start + 1; i < len(s); i++ {
			if s[i] == '\\' {
				i++
				continue
			}
			if s[i] == '"' {
				return i + 1
			}
		}
		return len(s)
	case n.Style&yaml.SingleQuotedStyle != 0:
		for i := start + 1; i < len(s); i++ {
			if s[i] == '\'' {
				if i+1 < len(s) && s[i+1] == '\'' {
					i++
					continue
				}
				return i + 1
			}
		}
		return len(s)
	}
	end := start
	for i := start; i < len(s) && s[i] != '\n'; i++ {
		if s[i] == '#' && i > start && (s[i-1] == ' ' || s[i-1] == '\t') {
			break
		}
		if s[i] != ' ' && s[i] != '\t' && s[i] != '\r' {
			end = i + 1
		}
	}
	return end
}

// flowEnd returns the offset just past the flow collection starting at start.
func (d *Doc) flowEnd(start int) int {
	s := d.src
	depth := 0
	var q byte
	for i := start; i < len(s); i++ {
		c := s[i]
		if q != 0 {
			if q == '"' && c == '\\' {
				i++
			} else if c == q {
				q = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			q = c
		case '[', '{':
			depth++
		case ']', '}':
			depth--
			if depth == 0 {
				return i + 1
			}
		case '#':
			if i > 0 && (s[i-1] == ' ' || s[i-1] == '\t') {
				for i < len(s) && s[i] != '\n' {
					i++
				}
			}
		}
	}
	return len(s)
}

// afterColon returns the offset just past the ':' that ends key k.
func (d *Doc) afterColon(k *yaml.Node) int {
	start := d.off(k.Line, k.Column)
	var e int
	if k.Style&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle) != 0 {
		e = d.scalarEnd(k, start)
	} else {
		e = start + len(k.Value)
		if e > len(d.src) || string(d.src[start:e]) != k.Value {
			e = start
		}
	}
	for e < len(d.src) && d.src[e] != ':' && d.src[e] != '\n' {
		e++
	}
	if e < len(d.src) && d.src[e] == ':' {
		e++
	}
	return e
}

// lastLine is the last line holding content of value v of key k.
func (d *Doc) lastLine(k, v *yaml.Node) int {
	keyIndent := 0
	if k != nil {
		keyIndent = k.Column - 1
		if isEmptyNull(v) {
			return k.Line
		}
	}
	switch {
	case v.Kind == yaml.AliasNode:
		return v.Line
	case isBlockScalar(v):
		last := v.Line
		for ln := v.Line + 1; ln <= len(d.lines); ln++ {
			t := d.lineText(ln)
			if strings.TrimSpace(t) == "" {
				continue
			}
			if indentOf(t) <= keyIndent {
				break
			}
			last = ln
		}
		return last
	case isFlow(v):
		return d.lineOf(d.flowEnd(d.off(v.Line, v.Column)) - 1)
	case v.Kind == yaml.ScalarNode:
		if v.Style&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle) != 0 {
			return d.lineOf(d.scalarEnd(v, d.off(v.Line, v.Column)) - 1)
		}
		return v.Line
	case v.Kind == yaml.MappingNode:
		last := v.Line
		for i := 0; i+1 < len(v.Content); i += 2 {
			if l := d.lastLine(v.Content[i], v.Content[i+1]); l > last {
				last = l
			}
		}
		return last
	case v.Kind == yaml.SequenceNode:
		last := v.Line
		for _, c := range v.Content {
			if l := d.lastLine(&yaml.Node{Line: c.Line, Column: v.Column}, c); l > last {
				last = l
			}
		}
		return last
	}
	return v.Line
}

// --- edits ------------------------------------------------------------------

func (d *Doc) splice(start, end int, text string) error {
	old := d.src
	d.src = append(append(append([]byte(nil), old[:start]...), text...), old[end:]...)
	if err := d.reparse(); err != nil {
		d.src = old
		_ = d.reparse()
		return fmt.Errorf("hsconfig: edit produced invalid YAML: %w", err)
	}
	return nil
}

// Set sets key to a scalar value, creating missing intermediate maps next
// to related keys. Existing quoting style is kept; unix_socket_permission is
// always double-quoted.
func (d *Doc) Set(key, value string) error {
	return d.SetPath(strings.Split(key, "."), value)
}

// SetPath is Set with explicit path segments (for keys containing dots).
func (d *Doc) SetPath(segs []string, value string) error {
	key := strings.Join(segs, ".")
	path, rest := d.find(segs)
	if len(rest) == 0 && len(path) > 0 {
		h := path[len(path)-1]
		text := formatScalar(key, value, h.val)
		v := h.val
		if v.Kind == yaml.ScalarNode && !isBlockScalar(v) && !isEmptyNull(v) {
			start := d.off(v.Line, v.Column)
			return d.splice(start, d.scalarEnd(v, start), text)
		}
		if h.parent != nil && isFlow(h.parent) {
			return ErrFlowStyle
		}
		return d.replaceValue(h, " "+text)
	}
	return d.insert(path, rest, func(indent int) string { return " " + formatScalar(key, value, nil) })
}

// SetList sets key to a sequence of strings, keeping flow style if the
// existing value is a flow sequence and block style otherwise.
func (d *Doc) SetList(key string, items []string) error {
	return d.SetListPath(strings.Split(key, "."), items)
}

// SetListPath is SetList with explicit path segments.
func (d *Doc) SetListPath(segs []string, items []string) error {
	path, rest := d.find(segs)
	render := func(indent int, flow bool) string {
		if len(items) == 0 {
			return " []"
		}
		if flow {
			q := make([]string, len(items))
			for i, it := range items {
				q[i] = formatItem(it, true)
			}
			return " [" + strings.Join(q, ", ") + "]"
		}
		var b strings.Builder
		for _, it := range items {
			b.WriteString("\n" + strings.Repeat(" ", indent) + "- " + formatItem(it, false))
		}
		return b.String()
	}
	if len(rest) == 0 && len(path) > 0 {
		h := path[len(path)-1]
		if h.parent != nil && isFlow(h.parent) {
			return ErrFlowStyle
		}
		indent := h.key.Column - 1 + 2
		if h.val.Kind == yaml.SequenceNode && !isFlow(h.val) && len(h.val.Content) > 0 {
			indent = h.val.Column - 1
		}
		flow := isFlow(h.val) && d.lineOf(d.flowEnd(d.off(h.val.Line, h.val.Column))-1) == h.val.Line
		return d.replaceValue(h, render(indent, flow))
	}
	return d.insert(path, rest, func(indent int) string { return render(indent+2, false) })
}

func (d *Doc) replaceValue(h hit, text string) error {
	start := d.afterColon(h.key)
	end := d.lineEnd(d.lastLine(h.key, h.val))
	if isEmptyNull(h.val) {
		end = d.lineEnd(h.key.Line)
		// Keep a trailing comment on the key line.
		if c := bytes.Index(d.src[start:end], []byte(" #")); c >= 0 {
			end = start + c
		}
	}
	return d.splice(start, end, text)
}

// insert creates rest under the last hit in path (or the root).
func (d *Doc) insert(path []hit, rest []string, value func(indent int) string) error {
	if len(rest) == 0 {
		return errors.New("hsconfig: empty key")
	}
	var parentKey *yaml.Node
	m := d.root
	baseIndent := 0
	if len(path) > 0 {
		h := path[len(path)-1]
		parentKey, m = h.key, h.val
		baseIndent = h.key.Column - 1 + 2
		switch {
		case m.Kind == yaml.MappingNode && !isFlow(m):
		case isEmptyNull(m) || (isFlow(m) && len(m.Content) == 0) || (m.Kind == yaml.ScalarNode && m.Tag == "!!null"):
			return d.replaceValue(h, "\n"+render(rest, baseIndent, value))
		case isFlow(m):
			return ErrFlowStyle
		default:
			return fmt.Errorf("hsconfig: %s is a %s, not a mapping", h.key.Value, kindName(m.Kind))
		}
	}
	if m == nil {
		text := render(rest, 0, value) + "\n"
		at := len(d.src)
		if at > 0 && d.src[at-1] != '\n' {
			text = "\n" + text
		}
		return d.splice(at, at, text)
	}
	if len(m.Content) > 0 {
		baseIndent = m.Content[0].Column - 1
	}
	_ = parentKey
	// Insert after the sibling sharing the longest name prefix, else last.
	best, bestScore := len(m.Content)-2, 0
	for i := 0; i+1 < len(m.Content); i += 2 {
		if s := commonPrefix(m.Content[i].Value, rest[0]); s >= 3 && s >= bestScore {
			best, bestScore = i, s
		}
	}
	at := d.lineEnd(d.lastLine(m.Content[best], m.Content[best+1]))
	return d.splice(at, at, "\n"+render(rest, baseIndent, value))
}

func render(segs []string, indent int, value func(indent int) string) string {
	var b strings.Builder
	for i, s := range segs {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(strings.Repeat(" ", indent+2*i) + formatKey(s) + ":")
	}
	b.WriteString(value(indent + 2*(len(segs)-1)))
	return b.String()
}

func commonPrefix(a, b string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

// Delete removes key and its value. Deleting a missing key is a no-op.
func (d *Doc) Delete(key string) error {
	h, ok := d.lookup(key)
	if !ok {
		return nil
	}
	if isFlow(h.parent) {
		return ErrFlowStyle
	}
	start := d.lines[h.key.Line-1]
	end := d.lineEnd(d.lastLine(h.key, h.val))
	if end < len(d.src) {
		end++
	}
	return d.splice(start, end, "")
}

// --- formatting -------------------------------------------------------------

func plainSafe(v string) bool {
	if v == "" || strings.TrimSpace(v) != v || strings.ContainsAny(v, "\n\r\t") {
		return false
	}
	if strings.Contains(v, ": ") || strings.Contains(v, " #") || strings.HasSuffix(v, ":") {
		return false
	}
	if strings.ContainsRune("&*!|>'\"%@`{[]},#?-", rune(v[0])) && !(v[0] == '-' && len(v) > 1 && v[1] != ' ') {
		return false
	}
	return true
}

// resolvesAsString reports whether a plain scalar v would load as the string v.
func resolvesAsString(v string) bool {
	var n yaml.Node
	if yaml.Unmarshal([]byte("k: "+v), &n) != nil || len(n.Content) == 0 || len(n.Content[0].Content) < 2 {
		return false
	}
	val := n.Content[0].Content[1]
	return val.Kind == yaml.ScalarNode && val.ShortTag() == "!!str" && val.Value == v
}

func doubleQuote(v string) string { return strconv.Quote(v) }

func singleQuote(v string) string { return "'" + strings.ReplaceAll(v, "'", "''") + "'" }

func formatScalar(key, value string, existing *yaml.Node) string {
	if key == "unix_socket_permission" || strings.HasSuffix(key, ".unix_socket_permission") {
		return doubleQuote(value)
	}
	if existing != nil && existing.Kind == yaml.ScalarNode {
		switch {
		case existing.Style&yaml.DoubleQuotedStyle != 0:
			return doubleQuote(value)
		case existing.Style&yaml.SingleQuotedStyle != 0:
			return singleQuote(value)
		}
	}
	if !plainSafe(value) {
		return doubleQuote(value)
	}
	if f, ok := Lookup(key); ok {
		switch f.Type {
		case TypeBool, TypeInt:
			return value
		}
		if !resolvesAsString(value) {
			return doubleQuote(value)
		}
	}
	return value
}

func formatItem(v string, flow bool) string {
	if !plainSafe(v) || (flow && strings.ContainsAny(v, ",[]{}")) {
		return doubleQuote(v)
	}
	return v
}

func formatKey(k string) string {
	if plainSafe(k) && resolvesAsString(k) {
		return k
	}
	return doubleQuote(k)
}
