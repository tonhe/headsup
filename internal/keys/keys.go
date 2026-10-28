// Package keys holds the user-overridable keymap. Action IDs are scoped by
// their prefix: "app.*" is global, "machines.*" belongs to the machines
// module, and so on. A key string is a bubbletea key name ("e", "ctrl+p");
// a two-key chord is written with a space ("g d").
package keys

import (
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/key"
)

// GlobalScope is the scope of app-wide actions.
const GlobalScope = "app"

// Map maps action ID to its keys.
type Map map[string][]string

// Conflict is two or more actions in one scope sharing a key (Kind "dup"),
// or a single key shadowing a chord that starts with it (Kind "prefix").
type Conflict struct {
	Scope   string
	Key     string
	Kind    string
	Actions []string
}

// Scope returns the scope of an action ID: the text before the first '.'.
func Scope(actionID string) string {
	if i := strings.IndexByte(actionID, '.'); i >= 0 {
		return actionID[:i]
	}
	return actionID
}

// Normalize collapses whitespace in a key or chord ("g  d" -> "g d"). The
// space bar, which bubbletea reports as " ", is canonically "space".
func Normalize(k string) string {
	if k != "" && strings.TrimSpace(k) == "" {
		return "space"
	}
	return strings.Join(strings.Fields(k), " ")
}

// Merge applies overrides on top of defaults. An override replaces an
// action's keys entirely; an empty list unbinds it. Conflicts are reported
// per scope, sorted, and are not fatal.
func Merge(defaults, overrides map[string][]string) (Map, []Conflict) {
	m := Map{}
	for id, ks := range defaults {
		m[id] = normAll(ks)
	}
	for id, ks := range overrides {
		m[id] = normAll(ks)
	}
	return m, m.Conflicts()
}

// Conflicts lists duplicate bindings and chord-prefix shadowing within each scope.
func (m Map) Conflicts() []Conflict {
	// scope -> key -> actions
	idx := map[string]map[string][]string{}
	for id, ks := range m {
		s := Scope(id)
		if idx[s] == nil {
			idx[s] = map[string][]string{}
		}
		for _, k := range ks {
			if !containsStr(idx[s][k], id) {
				idx[s][k] = append(idx[s][k], id)
			}
		}
	}
	var out []Conflict
	for s, byKey := range idx {
		for k, ids := range byKey {
			if len(ids) > 1 {
				sort.Strings(ids)
				out = append(out, Conflict{Scope: s, Key: k, Kind: "dup", Actions: ids})
			}
			if strings.Contains(k, " ") {
				first := strings.Fields(k)[0]
				if single, ok := byKey[first]; ok {
					acts := append(append([]string{}, single...), ids...)
					sort.Strings(acts)
					out = append(out, Conflict{Scope: s, Key: k, Kind: "prefix", Actions: acts})
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Scope != out[j].Scope {
			return out[i].Scope < out[j].Scope
		}
		if out[i].Key != out[j].Key {
			return out[i].Key < out[j].Key
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}

// Lookup finds the action bound to key in the given module scope, falling
// back to the global scope. scopePrefix may be "machines" or "machines.".
// When several actions in a scope share the key, the lexically first wins.
func (m Map) Lookup(scopePrefix, k string) (actionID string, ok bool) {
	scope := strings.TrimSuffix(scopePrefix, ".")
	k = Normalize(k)
	if scope != "" && scope != GlobalScope {
		if id, ok := m.lookupIn(scope, k); ok {
			return id, true
		}
	}
	return m.lookupIn(GlobalScope, k)
}

func (m Map) lookupIn(scope, k string) (string, bool) {
	best := ""
	for id, ks := range m {
		if Scope(id) != scope || !containsStr(ks, k) {
			continue
		}
		if best == "" || id < best {
			best = id
		}
	}
	return best, best != ""
}

// Pending reports whether prefix (keys typed so far, e.g. "g") begins a
// longer chord bound in the module scope or the global scope, so the caller
// should wait for the next key instead of dispatching.
func (m Map) Pending(scopePrefix, prefix string) bool {
	scope := strings.TrimSuffix(scopePrefix, ".")
	p := Normalize(prefix) + " "
	if p == " " {
		return false
	}
	for id, ks := range m {
		s := Scope(id)
		if s != GlobalScope && s != scope {
			continue
		}
		for _, k := range ks {
			if strings.HasPrefix(k, p) {
				return true
			}
		}
	}
	return false
}

// Keys returns the keys bound to an action.
func (m Map) Keys(actionID string) []string { return m[actionID] }

// Binding builds a bubbles key.Binding for help rendering. Chords are kept
// verbatim; key.Matches only sees single keys, so dispatch should go
// through Lookup/Pending.
func (m Map) Binding(actionID, help string) key.Binding {
	ks := m[actionID]
	if len(ks) == 0 {
		return key.NewBinding(key.WithDisabled())
	}
	raw := make([]string, len(ks))
	for i, k := range ks {
		raw[i] = k
		if k == "space" {
			raw[i] = " " // what bubbletea's KeyMsg.String reports
		}
	}
	return key.NewBinding(key.WithKeys(raw...), key.WithHelp(strings.Join(ks, "/"), help))
}

func normAll(ks []string) []string {
	out := make([]string, 0, len(ks))
	for _, k := range ks {
		if n := Normalize(k); n != "" && !containsStr(out, n) {
			out = append(out, n)
		}
	}
	return out
}

func containsStr(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
