package form

import (
	"errors"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tonhe/fathom/internal/icons"
	"github.com/tonhe/fathom/internal/module"
	"github.com/tonhe/fathom/internal/ui/uitest"
)

func typeStr(m *Model, s string) {
	for _, r := range s {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

func spec() module.FormSpec {
	return module.FormSpec{Title: "New key", Fields: []module.Field{
		{ID: "name", Label: "Name", Kind: module.FieldText, Required: true,
			Validate: func(v string) error {
				if v == "bad" {
					return errors.New("no")
				}
				return nil
			}},
		{ID: "pw", Label: "Secret", Kind: module.FieldSecret},
		{ID: "reuse", Label: "Reusable", Kind: module.FieldToggle},
		{ID: "user", Label: "User", Kind: module.FieldSelect, Options: []string{"alice", "bob", "carol"}},
		{ID: "exp", Label: "Expiry", Kind: module.FieldDuration, Value: "90d"},
		{ID: "tags", Label: "Tags", Kind: module.FieldTags},
	}}
}

func submitOf(cmd tea.Cmd) (module.Values, bool) {
	if cmd == nil {
		return nil, false
	}
	s, ok := cmd().(SubmitMsg)
	return s.Values, ok
}

func TestSubmitFlow(t *testing.T) {
	m := New(uitest.StyleFor(""), icons.Unicode, spec())
	if _, ok := submitOf(m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})); ok {
		t.Fatal("submitted with required field empty")
	}
	if m.Errors()["name"] != "required" || m.Focus() != 0 {
		t.Fatalf("errors %v focus %d", m.Errors(), m.Focus())
	}
	typeStr(m, "srv")
	m.Update(tea.KeyMsg{Type: tea.KeyTab})
	typeStr(m, "hunter2")
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")})
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	typeStr(m, "server, db,tag:server")
	vals, ok := submitOf(m.Update(tea.KeyMsg{Type: tea.KeyEnter}))
	if !ok {
		t.Fatalf("not submitted: %v", m.Errors())
	}
	want := module.Values{"name": "srv", "pw": "hunter2", "reuse": "true", "user": "carol", "exp": "90d", "tags": "tag:server,tag:db"}
	for k, v := range want {
		if vals[k] != v {
			t.Errorf("%s = %q want %q", k, vals[k], v)
		}
	}
}

func TestValidation(t *testing.T) {
	m := New(uitest.StyleFor(""), icons.Unicode, spec())
	typeStr(m, "bad")
	for i := 0; i < 4; i++ {
		m.Update(tea.KeyMsg{Type: tea.KeyTab})
	}
	typeStr(m, "x")
	if m.Validate() {
		t.Fatal("expected errors")
	}
	errs := m.Errors()
	if errs["name"] != "no" || errs["exp"] == "" {
		t.Fatalf("errs = %v", errs)
	}
	if _, ok := m.Update(tea.KeyMsg{Type: tea.KeyEsc})().(CancelMsg); !ok {
		t.Fatal("esc should cancel")
	}
}

func TestParseDuration(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
		err  bool
	}{
		{"90d", 90 * 24 * time.Hour, false},
		{"1d12h", 36 * time.Hour, false},
		{"1.5d", 36 * time.Hour, false},
		{"30m", 30 * time.Minute, false},
		{"d", 0, true},
		{"5x", 0, true},
		{"1dx", 0, true},
	}
	for _, c := range cases {
		got, err := ParseDuration(c.in)
		if (err != nil) != c.err || got != c.want {
			t.Errorf("%q = %v, %v", c.in, got, err)
		}
	}
}

func TestViewWidth(t *testing.T) {
	uitest.ForceColor()
	m := New(uitest.StyleFor(""), icons.Unicode, spec())
	m.Validate()
	for _, w := range []int{80, 50, 30} {
		if mw := uitest.MaxWidth(m.View(w)); mw > w {
			t.Errorf("w=%d got %d", w, mw)
		}
	}
}
