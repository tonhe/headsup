package filter

import (
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tonhe/headsup/internal/icons"
	"github.com/tonhe/headsup/internal/ui/uitest"
)

func TestParse(t *testing.T) {
	cases := []struct {
		in   string
		want Query
	}{
		{"", Query{}},
		{"nas", Query{Terms: []Term{{Value: "nas"}}}},
		{`tag:server -user:Bob "my laptop"`, Query{Terms: []Term{
			{Key: "tag", Value: "server"}, {Key: "user", Value: "Bob", Neg: true}, {Value: "my laptop"}}}},
		{"is:online -is:expired", Query{Terms: []Term{{Key: "is", Value: "online"}, {Key: "is", Value: "expired", Neg: true}}}},
		{`os:"mac os" IP:100.64`, Query{Terms: []Term{{Key: "os", Value: "mac os"}, {Key: "ip", Value: "100.64"}}}},
		{"  ~nsa  ", Query{Fuzzy: true, Text: "nsa"}},
		{"- a", Query{Terms: []Term{{Value: "-"}, {Value: "a"}}}},
		{`"a:b" :x`, Query{Terms: []Term{{Value: "a:b"}, {Value: ":x"}}}},
		{`"unterminated`, Query{Terms: []Term{{Value: "unterminated"}}}},
	}
	for _, c := range cases {
		got := Parse(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("Parse(%q) = %#v want %#v", c.in, got, c.want)
		}
	}
}

type node struct {
	text   string
	fields map[string][]string
	states map[string]bool
}

func (n node) FilterText() string            { return n.text }
func (n node) FilterField(k string) []string { return n.fields[k] }
func (n node) FilterIs(s string) bool        { return n.states[s] }

func TestMatch(t *testing.T) {
	nas := node{"nas-01 alice 100.64.0.3", map[string][]string{"tag": {"tag:server"}, "user": {"alice"}}, map[string]bool{"online": true}}
	cases := []struct {
		q    string
		want bool
	}{
		{"", true},
		{"NAS", true},
		{"tag:server", true},
		{"tag:tag:server", true},
		{"tag:db", false},
		{"-tag:db user:ali", true},
		{"is:online", true},
		{"-is:online", false},
		{"is:offline", false},
		{`"nas-01 alice"`, true},
		{"~n01ali", true},
		{"~zzz", false},
		{"tag:", true},
		{"route:", false},
	}
	for _, c := range cases {
		if got := Parse(c.q).Match(nas); got != c.want {
			t.Errorf("%q match = %v want %v", c.q, got, c.want)
		}
	}
}

func FuzzParse(f *testing.F) {
	for _, s := range []string{"", "tag:x -user:\"a b\" is:online", "~fuzzy", `"\"q\\"`, "-", "--a", "a:b:c", " x", "\xff:\xfe"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		q := Parse(s)
		again := Parse(q.String())
		if !reflect.DeepEqual(q, again) {
			t.Fatalf("round trip %q -> %q: %#v != %#v", s, q.String(), q, again)
		}
		if q.String() != again.String() {
			t.Fatalf("String not stable for %q", s)
		}
		_ = q.Match(node{text: s})
	})
}

func TestBar(t *testing.T) {
	s := uitest.StyleFor("")
	b := NewBar(s, icons.Unicode)
	b.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	if !b.Active() {
		t.Fatal("not opened")
	}
	for _, r := range "tag:x" {
		b.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	b.SetCounts(3, 51)
	for _, w := range []int{10, 40, 80} {
		v := b.View(w)
		if lipgloss.Width(v) != w {
			t.Errorf("width %d != %d", lipgloss.Width(v), w)
		}
		if w == 80 && !strings.Contains(v, "3/51") {
			t.Errorf("missing count: %q", v)
		}
	}
	b.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if b.Active() || b.Value() != "tag:x" || len(b.Query().Terms) != 1 {
		t.Fatal("enter should keep filter")
	}
	b.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	cmd := b.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if b.Active() || b.Value() != "" {
		t.Fatal("esc should clear")
	}
	if msg, ok := cmd().(ChangedMsg); !ok || msg.Value != "" {
		t.Fatal("esc should emit ChangedMsg")
	}
}
