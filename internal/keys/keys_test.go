package keys

import (
	"reflect"
	"testing"
)

var defaults = map[string][]string{
	"app.palette":      {":", "ctrl+p"},
	"app.quit":         {"q", "ctrl+c"},
	"app.goto_dash":    {"g d"},
	"app.select":       {" "},
	"machines.expire":  {"e"},
	"machines.delete":  {"d"},
	"machines.quit":    {"q"}, // module shadows global
	"users.rename":     {"r"},
	"machines.rename":  {"r"}, // different scope: no conflict with users.rename
	"machines.goto_ip": {"g  i"},
}

func TestMergeOverridesAndConflicts(t *testing.T) {
	m, conf := Merge(defaults, map[string][]string{
		"machines.expire": {"d"}, // collides with machines.delete
		"app.palette":     {":"},
		"users.rename":    {},
	})
	if got := m.Keys("app.palette"); !reflect.DeepEqual(got, []string{":"}) {
		t.Fatalf("palette = %v", got)
	}
	if len(m.Keys("users.rename")) != 0 {
		t.Fatal("unbind failed")
	}
	if got := m.Keys("machines.goto_ip"); !reflect.DeepEqual(got, []string{"g i"}) {
		t.Fatalf("normalize = %v", got)
	}
	want := []Conflict{{Scope: "machines", Key: "d", Kind: "dup", Actions: []string{"machines.delete", "machines.expire"}}}
	if !reflect.DeepEqual(conf, want) {
		t.Fatalf("conflicts = %+v", conf)
	}
	if _, defConf := Merge(defaults, nil); len(defConf) != 0 {
		t.Fatalf("defaults should not conflict: %+v", defConf)
	}
}

func TestChordPrefixConflict(t *testing.T) {
	_, conf := Merge(map[string][]string{"app.go": {"g"}, "app.goto_dash": {"g d"}}, nil)
	if len(conf) != 1 || conf[0].Kind != "prefix" || conf[0].Key != "g d" {
		t.Fatalf("conflicts = %+v", conf)
	}
}

func TestLookup(t *testing.T) {
	m, _ := Merge(defaults, nil)
	tests := []struct {
		scope, key, want string
		ok               bool
	}{
		{"machines", "q", "machines.quit", true},
		{"machines.", "e", "machines.expire", true},
		{"users", "q", "app.quit", true},
		{"users", "e", "", false},
		{"", "ctrl+p", "app.palette", true},
		{"machines", "g d", "app.goto_dash", true},
		{"machines", "g i", "machines.goto_ip", true},
		{"users", "g i", "", false},
		{"users", " ", "app.select", true},
		{"users", "space", "app.select", true},
	}
	for _, tt := range tests {
		got, ok := m.Lookup(tt.scope, tt.key)
		if got != tt.want || ok != tt.ok {
			t.Errorf("Lookup(%q,%q) = %q,%v want %q,%v", tt.scope, tt.key, got, ok, tt.want, tt.ok)
		}
	}
}

func TestPending(t *testing.T) {
	m, _ := Merge(defaults, nil)
	tests := []struct {
		scope, prefix string
		want          bool
	}{
		{"machines", "g", true},
		{"users", "g", true}, // global g d
		{"users", "e", false},
		{"machines", "", false},
		{"machines", "g d", false},
	}
	for _, tt := range tests {
		if got := m.Pending(tt.scope, tt.prefix); got != tt.want {
			t.Errorf("Pending(%q,%q) = %v", tt.scope, tt.prefix, got)
		}
	}
}

func TestBinding(t *testing.T) {
	m, _ := Merge(defaults, nil)
	b := m.Binding("app.palette", "commands")
	if b.Help().Key != ":/ctrl+p" || !b.Enabled() {
		t.Fatalf("help = %+v", b.Help())
	}
	if m.Binding("nope", "x").Enabled() {
		t.Fatal("unbound action should be disabled")
	}
}
