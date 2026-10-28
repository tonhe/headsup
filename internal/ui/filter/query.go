// Package filter parses list filter queries and provides the "/" query bar.
//
// Syntax: free words and "quoted phrases" match the row text; key:value
// matches a field (tag:x, user:x, any key); is:state matches a state
// (online, offline, idle, expired, exit, pending, ...); a leading "-" negates a
// term; all terms must match. A query starting with "~" is a fuzzy match of
// the rest against the row text.
package filter

import (
	"strings"
	"unicode"

	"github.com/sahilm/fuzzy"
)

// Filterable is something a Query can match.
type Filterable interface {
	FilterText() string
	FilterField(key string) []string
	FilterIs(state string) bool
}

// Term is one query term. Key is "" for free text, "is" for states.
type Term struct {
	Key   string
	Value string
	Neg   bool
}

// Query is a parsed filter.
type Query struct {
	Fuzzy bool
	Text  string // fuzzy pattern when Fuzzy
	Terms []Term
}

// Empty reports whether the query matches everything trivially.
func (q Query) Empty() bool {
	if q.Fuzzy {
		return q.Text == ""
	}
	return len(q.Terms) == 0
}

// Parse parses q. It never fails; malformed input degrades to free text.
func Parse(q string) Query {
	t := strings.TrimLeftFunc(q, unicode.IsSpace)
	if strings.HasPrefix(t, "~") {
		return Query{Fuzzy: true, Text: strings.TrimSpace(t[1:])}
	}
	var out Query
	rs := []rune(q)
	i := 0
	for i < len(rs) {
		for i < len(rs) && unicode.IsSpace(rs[i]) {
			i++
		}
		if i >= len(rs) {
			break
		}
		var term Term
		if rs[i] == '-' && i+1 < len(rs) && !unicode.IsSpace(rs[i+1]) {
			term.Neg = true
			i++
		}
		var buf []rune
		colon, quoted, inQ := -1, false, false
		for i < len(rs) {
			c := rs[i]
			if inQ {
				switch c {
				case '\\':
					if i+1 < len(rs) {
						i++
						buf = append(buf, rs[i])
					}
				case '"':
					inQ = false
				default:
					buf = append(buf, c)
				}
				i++
				continue
			}
			if unicode.IsSpace(c) {
				break
			}
			switch c {
			case '"':
				inQ, quoted = true, true
			case ':':
				if colon < 0 && !quoted && len(buf) > 0 {
					colon = len(buf)
				}
				buf = append(buf, c)
			default:
				buf = append(buf, c)
			}
			i++
		}
		if colon > 0 {
			term.Key = strings.ToLower(string(buf[:colon]))
			term.Value = string(buf[colon+1:])
		} else {
			term.Value = string(buf)
		}
		out.Terms = append(out.Terms, term)
	}
	return out
}

// String renders q canonically; Parse(q.String()) reproduces q.
func (q Query) String() string {
	if q.Fuzzy {
		return "~" + q.Text
	}
	parts := make([]string, len(q.Terms))
	for i, t := range q.Terms {
		var b strings.Builder
		if t.Neg {
			b.WriteByte('-')
		}
		if t.Key != "" {
			b.WriteString(t.Key)
			b.WriteByte(':')
			b.WriteString(quoteIf(t.Value, false))
		} else {
			b.WriteString(quoteIf(t.Value, true))
		}
		parts[i] = b.String()
	}
	return strings.Join(parts, " ")
}

func quoteIf(v string, free bool) string {
	need := strings.ContainsFunc(v, func(r rune) bool { return unicode.IsSpace(r) || r == '"' })
	if free && (v == "" || strings.ContainsRune(v, ':') || strings.HasPrefix(v, "-") || strings.HasPrefix(v, "~")) {
		need = true
	}
	if !need {
		return v
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range v {
		if r == '"' || r == '\\' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	b.WriteByte('"')
	return b.String()
}

// Match reports whether f satisfies every term (or the fuzzy pattern).
func (q Query) Match(f Filterable) bool {
	if q.Fuzzy {
		if q.Text == "" {
			return true
		}
		return len(fuzzy.Find(strings.ToLower(q.Text), []string{strings.ToLower(f.FilterText())})) > 0
	}
	var text string
	textDone := false
	for _, t := range q.Terms {
		var ok bool
		lv := strings.ToLower(t.Value)
		switch t.Key {
		case "":
			if !textDone {
				text, textDone = strings.ToLower(f.FilterText()), true
			}
			ok = strings.Contains(text, lv)
		case "is":
			ok = f.FilterIs(lv)
		default:
			ok = matchField(t.Key, lv, f.FilterField(t.Key))
		}
		if ok == t.Neg {
			return false
		}
	}
	return true
}

func matchField(key, lv string, vals []string) bool {
	if lv == "" {
		return len(vals) > 0
	}
	if key == "tag" {
		lv = strings.TrimPrefix(lv, "tag:")
	}
	for _, v := range vals {
		v = strings.ToLower(v)
		if key == "tag" {
			v = strings.TrimPrefix(v, "tag:")
		}
		if strings.Contains(v, lv) {
			return true
		}
	}
	return false
}

// States lists the documented is: values, for completion and help.
var States = []string{"online", "offline", "idle", "expired", "exit", "pending"}
