// Package form renders a module.FormSpec as a modal form and emits SubmitMsg
// or CancelMsg.
package form

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/tonhe/fathom/internal/icons"
	"github.com/tonhe/fathom/internal/module"
	"github.com/tonhe/fathom/internal/ui/styles"
)

// SubmitMsg carries validated, normalised values keyed by field ID.
type SubmitMsg struct{ Values module.Values }

// CancelMsg is sent when the user presses esc.
type CancelMsg struct{}

// MaxBoxWidth caps the form box width.
const MaxBoxWidth = 72

type field struct {
	spec module.Field
	in   textinput.Model // text, secret, duration, tags
	on   bool            // toggle
	opt  int             // select
	err  string
}

// Model is the form state.
type Model struct {
	s      *styles.Styles
	ic     icons.Set
	spec   module.FormSpec
	fields []*field
	focus  int
}

// New builds a form from spec, seeding each field with Field.Value.
func New(s *styles.Styles, ic icons.Set, spec module.FormSpec) *Model {
	m := &Model{s: s, ic: ic, spec: spec}
	for _, f := range spec.Fields {
		fd := &field{spec: f}
		switch f.Kind {
		case module.FieldToggle:
			fd.on = truthy(f.Value)
		case module.FieldSelect:
			for i, o := range f.Options {
				if o == f.Value {
					fd.opt = i
				}
			}
		default:
			in := textinput.New()
			in.Prompt = ""
			in.SetValue(f.Value)
			in.Placeholder = f.Placeholder
			in.PlaceholderStyle = s.Muted
			in.TextStyle = s.Input
			in.Cursor.Style = s.Accent
			if f.Kind == module.FieldSecret {
				in.EchoMode = textinput.EchoPassword
				in.EchoCharacter = '•'
				if ic.Name == "ascii" {
					in.EchoCharacter = '*'
				}
			}
			fd.in = in
		}
		m.fields = append(m.fields, fd)
	}
	m.setFocus(0)
	return m
}

func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on", "y":
		return true
	}
	return false
}

// Init returns the cursor blink command.
func (m *Model) Init() tea.Cmd { return textinput.Blink }

// Focus returns the focused field index.
func (m *Model) Focus() int { return m.focus }

func (m *Model) setFocus(i int) tea.Cmd {
	if len(m.fields) == 0 {
		return nil
	}
	m.focus = (i + len(m.fields)) % len(m.fields)
	var cmd tea.Cmd
	for j, f := range m.fields {
		if !isText(f.spec.Kind) {
			continue
		}
		if j == m.focus {
			f.in.TextStyle = m.s.InputFocus
			cmd = f.in.Focus()
		} else {
			f.in.TextStyle = m.s.Input
			f.in.Blur()
		}
	}
	return cmd
}

func isText(k module.FieldKind) bool {
	return k != module.FieldToggle && k != module.FieldSelect
}

func (f *field) raw() string {
	switch f.spec.Kind {
	case module.FieldToggle:
		return strconv.FormatBool(f.on)
	case module.FieldSelect:
		if len(f.spec.Options) == 0 {
			return ""
		}
		return f.spec.Options[f.opt]
	default:
		return f.in.Value()
	}
}

// Values returns the current values (normalised, not validated).
func (m *Model) Values() module.Values {
	v := module.Values{}
	for _, f := range m.fields {
		r := f.raw()
		switch f.spec.Kind {
		case module.FieldTags:
			r = NormalizeTags(r)
		case module.FieldText, module.FieldDuration:
			r = strings.TrimSpace(r)
		}
		v[f.spec.ID] = r
	}
	return v
}

// Errors returns the current per-field errors keyed by field ID.
func (m *Model) Errors() map[string]string {
	out := map[string]string{}
	for _, f := range m.fields {
		if f.err != "" {
			out[f.spec.ID] = f.err
		}
	}
	return out
}

// Validate checks every field, records errors and reports success.
func (m *Model) Validate() bool {
	vals := m.Values()
	first := -1
	for i, f := range m.fields {
		f.err = ""
		v := vals[f.spec.ID]
		var err error
		switch {
		case f.spec.Required && v == "":
			err = errors.New("required")
		case f.spec.Kind == module.FieldDuration && v != "":
			_, err = ParseDuration(v)
		}
		if err == nil && f.spec.Validate != nil && (v != "" || f.spec.Required) {
			err = f.spec.Validate(v)
		}
		if err != nil {
			f.err = err.Error()
			if first < 0 {
				first = i
			}
		}
	}
	if first >= 0 {
		m.setFocus(first)
		return false
	}
	return true
}

func (m *Model) submit() tea.Cmd {
	if !m.Validate() {
		return nil
	}
	v := m.Values()
	return func() tea.Msg { return SubmitMsg{Values: v} }
}

// Update handles navigation and editing.
func (m *Model) Update(msg tea.Msg) tea.Cmd {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		if len(m.fields) > 0 && isText(m.fields[m.focus].spec.Kind) {
			var cmd tea.Cmd
			f := m.fields[m.focus]
			f.in, cmd = f.in.Update(msg)
			return cmd
		}
		return nil
	}
	switch km.String() {
	case "esc":
		return func() tea.Msg { return CancelMsg{} }
	case "ctrl+s":
		return m.submit()
	case "tab", "down":
		return m.setFocus(m.focus + 1)
	case "shift+tab", "up":
		return m.setFocus(m.focus - 1)
	case "enter":
		if m.focus >= len(m.fields)-1 {
			return m.submit()
		}
		return m.setFocus(m.focus + 1)
	}
	if len(m.fields) == 0 {
		return nil
	}
	f := m.fields[m.focus]
	switch f.spec.Kind {
	case module.FieldToggle:
		switch km.String() {
		case " ", "left", "right", "h", "l", "x":
			f.on = !f.on
			f.err = ""
		}
		return nil
	case module.FieldSelect:
		n := len(f.spec.Options)
		if n == 0 {
			return nil
		}
		switch km.String() {
		case "left", "h":
			f.opt = (f.opt - 1 + n) % n
		case "right", "l", " ":
			f.opt = (f.opt + 1) % n
		}
		f.err = ""
		return nil
	}
	before := f.in.Value()
	var cmd tea.Cmd
	f.in, cmd = f.in.Update(msg)
	if f.in.Value() != before {
		f.err = ""
	}
	return cmd
}

// View renders the form box, at most w cells wide.
func (m *Model) View(w int) string {
	box := m.s.Modal
	boxW := min(MaxBoxWidth, w)
	cw := max(1, boxW-box.GetHorizontalFrameSize())
	fit := func(s string) string {
		if lipgloss.Width(s) > cw {
			return ansi.Truncate(s, cw, m.ic.Ellipsis) + ansi.ResetStyle
		}
		return s
	}
	labelW := 0
	for _, f := range m.fields {
		labelW = max(labelW, lipgloss.Width(f.spec.Label)+2)
	}
	labelW = min(labelW, cw/3)
	inW := max(1, cw-labelW-1)

	var lines []string
	if m.spec.Title != "" {
		lines = append(lines, fit(m.s.ModalTitle.Render(m.spec.Title)), "")
	}
	for i, f := range m.fields {
		foc := i == m.focus
		marker := " "
		if foc {
			marker = m.s.Accent.Render(m.ic.Arrow)
		}
		lbl := f.spec.Label
		if f.spec.Required {
			lbl += "*"
		}
		lbl = fitCell(lbl, labelW-1)
		ls := m.s.Label
		if foc {
			ls = m.s.Strong
		}
		var val string
		switch f.spec.Kind {
		case module.FieldToggle:
			val = m.s.Muted.Render("[ ]")
			if f.on {
				val = m.s.OK.Render("[" + m.ic.Check + "]")
			}
		case module.FieldSelect:
			opt := ""
			if len(f.spec.Options) > 0 {
				opt = f.spec.Options[f.opt]
			}
			st := m.s.Input
			if foc {
				st = m.s.InputFocus
			}
			val = m.s.Muted.Render("‹ ") + st.Render(opt) + m.s.Muted.Render(" ›")
			if m.ic.Name == "ascii" {
				val = m.s.Muted.Render("< ") + st.Render(opt) + m.s.Muted.Render(" >")
			}
		default:
			f.in.Width = max(1, inW-1)
			val = f.in.View()
		}
		lines = append(lines, fit(marker+ls.Render(lbl)+" "+val))
		if f.err != "" {
			lines = append(lines, fit(strings.Repeat(" ", labelW+1)+m.s.Danger.Render(m.ic.Cross+" "+f.err)))
		} else if foc && f.spec.Help != "" {
			lines = append(lines, fit(strings.Repeat(" ", labelW+1)+m.s.Muted.Render(f.spec.Help)))
		}
	}
	submit := m.spec.Submit
	if submit == "" {
		submit = "Save"
	}
	btn := m.s.Button.Render(submit)
	if m.focus == len(m.fields)-1 || len(m.fields) == 0 {
		btn = m.s.ButtonFo.Render(submit)
	}
	lines = append(lines, "", fit(btn), fit(m.s.Muted.Render("tab next · enter/ctrl+s save · esc cancel")))
	return box.Width(cw + box.GetHorizontalPadding()).Render(strings.Join(lines, "\n"))
}

func fitCell(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) > w {
		s = ansi.Truncate(s, w, "")
	}
	return s + strings.Repeat(" ", w-lipgloss.Width(s))
}

// ParseDuration is time.ParseDuration plus a leading day component: "7d",
// "1d12h", "1.5d".
func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, 'd'); i >= 0 {
		days, err := strconv.ParseFloat(s[:i], 64)
		if err != nil || i == 0 {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		d := time.Duration(days * float64(24*time.Hour))
		if rest := s[i+1:]; rest != "" {
			r, err := time.ParseDuration(rest)
			if err != nil {
				return 0, fmt.Errorf("invalid duration %q", s)
			}
			if days < 0 {
				r = -r
			}
			d += r
		}
		return d, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	return d, nil
}

// NormalizeTags splits on commas and whitespace, adds the "tag:" prefix,
// drops empties and duplicates, and joins with ",".
func NormalizeTags(s string) string {
	seen := map[string]bool{}
	var out []string
	for _, t := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
		if !strings.HasPrefix(t, "tag:") {
			t = "tag:" + t
		}
		if t == "tag:" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return strings.Join(out, ",")
}
