package filter

import (
	"fmt"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/tonhe/fathom/internal/icons"
	"github.com/tonhe/fathom/internal/ui/styles"
)

// ChangedMsg is emitted whenever the bar's query text changes.
type ChangedMsg struct {
	Value string
	Query Query
}

// Bar is the "/" filter bar: open with "/", enter keeps the filter and closes
// the input, esc clears it and closes.
type Bar struct {
	s            *styles.Styles
	ic           icons.Set
	in           textinput.Model
	active       bool
	match, total int
	q            Query
}

// NewBar builds an empty, inactive bar.
func NewBar(s *styles.Styles, ic icons.Set) *Bar {
	in := textinput.New()
	in.Prompt = ""
	in.Placeholder = "text  tag:x  user:x  is:online  -neg  ~fuzzy"
	in.PlaceholderStyle = s.Muted
	in.TextStyle = s.InputFocus
	in.Cursor.Style = s.Accent
	return &Bar{s: s, ic: ic, in: in}
}

// Active reports whether the input is open (it then captures all keys).
func (b *Bar) Active() bool { return b.active }

// Value returns the raw query text.
func (b *Bar) Value() string { return b.in.Value() }

// Query returns the parsed query.
func (b *Bar) Query() Query { return b.q }

// SetValue sets the query text without opening the bar.
func (b *Bar) SetValue(v string) {
	b.in.SetValue(v)
	b.in.CursorEnd()
	b.q = Parse(v)
}

// SetCounts sets the "match/total" counter.
func (b *Bar) SetCounts(match, total int) { b.match, b.total = match, total }

// Open focuses the input.
func (b *Bar) Open() tea.Cmd {
	b.active = true
	return b.in.Focus()
}

// Close blurs the input, keeping the filter.
func (b *Bar) Close() { b.active = false; b.in.Blur() }

// Clear empties the filter and closes the bar.
func (b *Bar) Clear() tea.Cmd {
	changed := b.in.Value() != ""
	b.SetValue("")
	b.Close()
	if changed {
		return b.changed()
	}
	return nil
}

func (b *Bar) changed() tea.Cmd {
	m := ChangedMsg{Value: b.in.Value(), Query: b.q}
	return func() tea.Msg { return m }
}

// Update handles keys. When inactive only "/" (opens) is consumed.
func (b *Bar) Update(msg tea.Msg) tea.Cmd {
	km, ok := msg.(tea.KeyMsg)
	if !b.active {
		if ok && km.String() == "/" {
			return b.Open()
		}
		return nil
	}
	if ok {
		switch km.String() {
		case "esc":
			return b.Clear()
		case "enter":
			b.Close()
			return nil
		}
	}
	before := b.in.Value()
	var cmd tea.Cmd
	b.in, cmd = b.in.Update(msg)
	if b.in.Value() != before {
		b.q = Parse(b.in.Value())
		return tea.Batch(cmd, b.changed())
	}
	return cmd
}

// View renders one line of exactly w cells; empty-padded when inactive and
// the filter is empty.
func (b *Bar) View(w int) string {
	if w <= 0 {
		return ""
	}
	count := ""
	if b.active || b.in.Value() != "" {
		count = b.s.Muted.Render(fmt.Sprintf(" %d/%d", b.match, b.total))
	}
	cw := lipgloss.Width(count)
	var left string
	switch {
	case b.active:
		prompt := b.s.Accent.Render("/ ")
		b.in.Width = max(1, w-cw-lipgloss.Width(prompt)-1)
		left = prompt + b.in.View()
	case b.in.Value() != "":
		left = b.s.Muted.Render("filter: ") + b.s.Text.Render(b.in.Value())
	}
	room := w - cw
	if room < 1 {
		count, room = "", w
	}
	if lipgloss.Width(left) > room {
		left = ansi.Truncate(left, room, b.ic.Ellipsis) + ansi.ResetStyle
	}
	if lipgloss.Width(left) > room {
		left = ansi.Truncate(left, room, "") + ansi.ResetStyle
	}
	gap := room - lipgloss.Width(left)
	return left + spaces(gap) + count
}

func spaces(n int) string {
	if n <= 0 {
		return ""
	}
	b := make([]byte, n)
	for i := range b {
		b[i] = ' '
	}
	return string(b)
}
