// Package spinner wraps bubbles/spinner with the theme's accent style and an
// ASCII-safe frame set.
package spinner

import (
	bs "github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/tonhe/headsup/internal/icons"
	"github.com/tonhe/headsup/internal/ui/styles"
)

// TickMsg is the spinner's tick message, re-exported for routing.
type TickMsg = bs.TickMsg

// Model is a themed spinner.
type Model struct{ m bs.Model }

// New builds a spinner; the ascii icon set uses the "|/-\" line frames.
func New(s *styles.Styles, ic icons.Set) *Model {
	sp := bs.MiniDot
	if ic.Name == "ascii" {
		sp = bs.Line
	}
	return &Model{m: bs.New(bs.WithSpinner(sp), bs.WithStyle(s.Accent))}
}

// Tick starts the animation.
func (m *Model) Tick() tea.Cmd { return m.m.Tick }

// Update advances on this spinner's TickMsg.
func (m *Model) Update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	m.m, cmd = m.m.Update(msg)
	return cmd
}

// View renders the current frame.
func (m *Model) View() string { return m.m.View() }
