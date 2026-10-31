// Package confirm is the confirmation modal for dangerous actions.
// DangerConfirm asks y/N; DangerCritical makes the user type the target name.
package confirm

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/tonhe/headsup/internal/icons"
	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/ui/styles"
)

// MaxListed is how many targets are listed before "and N more".
const MaxListed = 10

// Needed reports whether an action of danger d needs a modal.
func Needed(d module.Danger) bool { return d != module.DangerNone }

// Request describes what to confirm.
type Request struct {
	ID          string // echoed in ResultMsg
	Title       string // "Expire machine"
	Danger      module.Danger
	Targets     []module.Target
	Consequence string // "The node must re-authenticate."
	Confirm     string // button label, default "Confirm"
	// Expect is the text to type for DangerCritical. Default: the single
	// target's Name (or ID), or the number of targets when there are several.
	Expect string
}

// ResultMsg is emitted once the user decides.
type ResultMsg struct {
	ID string
	OK bool
}

// Model is the modal state.
type Model struct {
	s     *styles.Styles
	ic    icons.Set
	req   Request
	focus int // 0 cancel, 1 confirm
	in    textinput.Model
	err   bool
	done  bool
}

// New builds a modal for req.
func New(s *styles.Styles, ic icons.Set, req Request) *Model {
	m := &Model{s: s, ic: ic, req: req}
	if m.req.Confirm == "" {
		m.req.Confirm = "Confirm"
	}
	if req.Danger == module.DangerCritical {
		if m.req.Expect == "" {
			m.req.Expect = expect(req.Targets)
		}
		m.in = textinput.New()
		m.in.Prompt = s.Danger.Render(ic.Arrow + " ")
		m.in.TextStyle = s.InputFocus
		m.in.Placeholder = m.req.Expect
		m.in.PlaceholderStyle = s.Muted
		m.in.Focus()
	}
	return m
}

func expect(ts []module.Target) string {
	switch len(ts) {
	case 0:
		return "yes"
	case 1:
		if ts[0].Name != "" {
			return ts[0].Name
		}
		return ts[0].ID
	default:
		return fmt.Sprint(len(ts))
	}
}

// Init returns the cursor blink for the critical input.
func (m *Model) Init() tea.Cmd {
	if m.req.Danger == module.DangerCritical {
		return textinput.Blink
	}
	return nil
}

// Request returns the request being confirmed.
func (m *Model) Request() Request { return m.req }

// Done reports whether a result was emitted.
func (m *Model) Done() bool { return m.done }

func (m *Model) result(ok bool) tea.Cmd {
	m.done = true
	r := ResultMsg{ID: m.req.ID, OK: ok}
	return func() tea.Msg { return r }
}

// Update handles keys and emits ResultMsg.
func (m *Model) Update(msg tea.Msg) tea.Cmd {
	if m.done {
		return nil
	}
	km, ok := msg.(tea.KeyMsg)
	if m.req.Danger == module.DangerCritical {
		if ok {
			switch km.String() {
			case "esc", "ctrl+c":
				return m.result(false)
			case "enter":
				if m.in.Value() == m.req.Expect {
					return m.result(true)
				}
				m.err = true
				return nil
			}
		}
		var cmd tea.Cmd
		m.in, cmd = m.in.Update(msg)
		m.err = false
		return cmd
	}
	if !ok {
		return nil
	}
	switch km.String() {
	case "y", "Y":
		return m.result(true)
	case "n", "N", "esc", "q", "ctrl+c":
		return m.result(false)
	case "left", "right", "tab", "shift+tab", "h", "l":
		m.focus = 1 - m.focus
	case "enter", " ":
		return m.result(m.focus == 1)
	}
	return nil
}

// View renders the modal box, at most w cells wide.
func (m *Model) View(w int) string {
	box := m.s.Modal
	if m.req.Danger == module.DangerCritical {
		box = m.s.ModalDanger
	}
	boxW := min(64, w)
	cw := max(1, boxW-box.GetHorizontalFrameSize())
	fit := func(s string) string {
		if lipgloss.Width(s) > cw {
			return ansi.Truncate(s, cw, m.ic.Ellipsis)
		}
		return s
	}
	var lines []string
	title := m.req.Title
	if m.req.Danger == module.DangerCritical {
		lines = append(lines, fit(m.s.Danger.Bold(true).Render(m.ic.Warn+" "+title)))
	} else {
		lines = append(lines, fit(m.s.ModalTitle.Render(title)))
	}
	if n := len(m.req.Targets); n > 0 {
		lines = append(lines, "")
		for i, t := range m.req.Targets {
			if i == MaxListed {
				lines = append(lines, m.s.Muted.Render(fmt.Sprintf("  and %d more", n-MaxListed)))
				break
			}
			name := t.Name
			if name == "" {
				name = t.ID
			}
			kind := ""
			if t.Kind != "" {
				kind = m.s.Muted.Render(t.Kind + " ")
			}
			lines = append(lines, fit("  "+m.s.Muted.Render(m.ic.Bullet)+" "+kind+m.s.Strong.Render(name)))
		}
	}
	if m.req.Consequence != "" {
		lines = append(lines, "")
		for _, l := range strings.Split(lipgloss.NewStyle().Width(cw).Render(m.req.Consequence), "\n") {
			lines = append(lines, m.s.Warn.Render(strings.TrimRight(l, " ")))
		}
	}
	lines = append(lines, "")
	if m.req.Danger == module.DangerCritical {
		lines = append(lines, fit(m.s.Label.Render("Type ")+m.s.Strong.Render(m.req.Expect)+m.s.Label.Render(" to confirm:")))
		m.in.Width = max(1, cw-lipgloss.Width(m.in.Prompt)-1)
		lines = append(lines, fit(m.in.View()))
		if m.err {
			lines = append(lines, m.s.Danger.Render("Does not match."))
		}
		lines = append(lines, fit(m.s.Muted.Render("enter confirm · esc cancel")))
	} else {
		cancel := m.s.RenderButton("Cancel", m.focus == 0)
		ok := m.s.RenderButton(m.req.Confirm, m.focus != 0)
		lines = append(lines, fit(cancel+"  "+ok), fit(m.s.Muted.Render("y confirm · n/esc cancel")))
	}
	return box.Width(cw + box.GetHorizontalPadding()).Render(strings.Join(lines, "\n"))
}
