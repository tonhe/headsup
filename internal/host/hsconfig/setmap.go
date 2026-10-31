package hsconfig

import (
	"strings"

	"gopkg.in/yaml.v3"
)

// MapEntry is one key of a mapping value written by SetMap. A nil Values
// with List false writes the scalar Value; List true writes Values as a
// block sequence (split DNS: domain -> resolvers).
type MapEntry struct {
	Key    string
	Value  string
	Values []string
	List   bool
}

// SetMap replaces key's value with a block mapping of entries (or {} when
// entries is empty), creating the key next to related keys when missing.
// Comments outside the replaced value survive.
func (d *Doc) SetMap(key string, entries []MapEntry) error {
	segs := strings.Split(key, ".")
	path, rest := d.find(segs)
	render := func(indent int) string {
		if len(entries) == 0 {
			return " {}"
		}
		var b strings.Builder
		pad := strings.Repeat(" ", indent)
		for _, e := range entries {
			b.WriteString("\n" + pad + formatKey(e.Key) + ":")
			if !e.List {
				b.WriteString(" " + formatMapScalar(e.Value))
				continue
			}
			if len(e.Values) == 0 {
				b.WriteString(" []")
				continue
			}
			for _, v := range e.Values {
				b.WriteString("\n" + pad + "  - " + formatItem(v, false))
			}
		}
		return b.String()
	}
	if len(rest) == 0 && len(path) > 0 {
		h := path[len(path)-1]
		if h.parent != nil && isFlow(h.parent) {
			return ErrFlowStyle
		}
		indent := h.key.Column - 1 + 2
		if h.val.Kind == yaml.MappingNode && !isFlow(h.val) && len(h.val.Content) > 0 {
			indent = h.val.Content[0].Column - 1
		}
		return d.replaceValue(h, render(indent))
	}
	return d.insert(path, rest, func(indent int) string { return render(indent + 2) })
}

// Map returns a mapping value as entries in document order. Scalar values
// fill Value, sequences fill Values with List set. ok is false when key is
// missing or not a mapping (null counts as an empty mapping).
func (d *Doc) Map(key string) ([]MapEntry, bool) {
	h, ok := d.lookup(key)
	if !ok {
		return nil, false
	}
	n := h.val
	if n.Kind == yaml.ScalarNode && n.Tag == "!!null" {
		return nil, true
	}
	if n.Kind != yaml.MappingNode {
		return nil, false
	}
	var out []MapEntry
	for i := 0; i+1 < len(n.Content); i += 2 {
		e := MapEntry{Key: n.Content[i].Value}
		v := n.Content[i+1]
		switch v.Kind {
		case yaml.SequenceNode:
			e.List = true
			for _, c := range v.Content {
				if c.Kind == yaml.ScalarNode {
					e.Values = append(e.Values, c.Value)
				}
			}
		case yaml.ScalarNode:
			if v.Tag != "!!null" {
				e.Value = v.Value
			}
		}
		out = append(out, e)
	}
	return out, true
}

func formatMapScalar(v string) string {
	if !plainSafe(v) || !resolvesAsString(v) {
		return doubleQuote(v)
	}
	return v
}
