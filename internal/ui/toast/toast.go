// Package toast is a small auto-dismissing notification stack drawn in the
// bottom-right corner over the main view.
package toast

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/tonhe/headsup/internal/icons"
	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/ui/modal"
	"github.com/tonhe/headsup/internal/ui/styles"
)

// MaxVisible is how many toasts are drawn at once (newest at the bottom).
const MaxVisible = 3

// MaxWidth caps a toast box's width.
const MaxWidth = 48

// MaxLines is the most lines one toast wraps to.
const MaxLines = 4

// Toast is one notification.
type Toast struct {
	ID    int
	Level module.ToastLevel
	Msg   string
}

// DismissMsg removes the toast with ID; Push schedules it via tea.Tick.
type DismissMsg struct{ ID int }

// Duration is how long a toast of level stays up.
func Duration(l module.ToastLevel) time.Duration {
	if l == module.ToastError {
		return 8 * time.Second
	}
	return 4 * time.Second
}

// Stack holds active toasts.
type Stack struct {
	s     *styles.Styles
	ic    icons.Set
	items []Toast
	next  int
}

// New builds an empty stack.
func New(s *styles.Styles, ic icons.Set) *Stack { return &Stack{s: s, ic: ic} }

// SetStyles swaps styles and icons (on theme change).
func (st *Stack) SetStyles(s *styles.Styles, ic icons.Set) { st.s, st.ic = s, ic }

// Push adds a toast and returns the Cmd that dismisses it later.
func (st *Stack) Push(l module.ToastLevel, msg string) tea.Cmd {
	st.next++
	id := st.next
	st.items = append(st.items, Toast{ID: id, Level: l, Msg: msg})
	return tea.Tick(Duration(l), func(time.Time) tea.Msg { return DismissMsg{ID: id} })
}

// Dismiss removes a toast by ID.
func (st *Stack) Dismiss(id int) {
	for i, t := range st.items {
		if t.ID == id {
			st.items = append(st.items[:i], st.items[i+1:]...)
			return
		}
	}
}

// Clear removes all toasts.
func (st *Stack) Clear() { st.items = nil }

// Len returns the number of active toasts (visible or not).
func (st *Stack) Len() int { return len(st.items) }

// Items returns the active toasts, oldest first.
func (st *Stack) Items() []Toast { return st.items }

// Update handles DismissMsg.
func (st *Stack) Update(msg tea.Msg) tea.Cmd {
	if d, ok := msg.(DismissMsg); ok {
		st.Dismiss(d.ID)
	}
	return nil
}

func (st *Stack) glyph(l module.ToastLevel) string {
	switch l {
	case module.ToastSuccess:
		return st.s.OK.Render(st.ic.Check)
	case module.ToastWarn:
		return st.s.Warn.Render(st.ic.Warn)
	case module.ToastError:
		return st.s.Danger.Render(st.ic.Cross)
	default:
		return st.s.Accent.Render(st.ic.Bullet)
	}
}

// View renders the visible toasts as a right-aligned block no wider than
// maxW (or "" when empty).
func (st *Stack) View(maxW int) string {
	if len(st.items) == 0 || maxW < 8 {
		return ""
	}
	vis := st.items
	if len(vis) > MaxVisible {
		vis = vis[len(vis)-MaxVisible:]
	}
	boxW := min(MaxWidth, maxW)
	var boxes []string
	for _, t := range vis {
		style, ok := st.s.Toast[int(t.Level)]
		if !ok {
			style = st.s.Toast[0]
		}
		textW := max(1, boxW-4-lipgloss.Width(st.ic.Bullet)-1)
		msg := strings.ReplaceAll(t.Msg, "\n", " ")
		// Wrap rather than truncate: toasts often carry a fix command that
		// must be readable in full. Beyond MaxLines the tail is elided.
		lines := strings.Split(ansi.Wordwrap(msg, textW, ""), "\n")
		if len(lines) > MaxLines {
			lines = lines[:MaxLines]
			lines[MaxLines-1] = ansi.Truncate(lines[MaxLines-1]+" "+st.ic.Ellipsis, textW, st.ic.Ellipsis)
		}
		for i, l := range lines {
			if lipgloss.Width(l) > textW {
				lines[i] = ansi.Hardwrap(l, textW, true)
			}
			if i > 0 {
				lines[i] = strings.Repeat(" ", lipgloss.Width(st.glyph(t.Level))+1) + lines[i]
			}
		}
		boxes = append(boxes, style.Render(st.glyph(t.Level)+" "+strings.Join(lines, "\n")))
	}
	return lipgloss.JoinVertical(lipgloss.Right, boxes...)
}

// Overlay draws toast over base in the bottom-right corner, one cell in from
// the right edge and one line above the bottom (clear of the status bar),
// preserving the rest of base. Returns exactly w x h.
func Overlay(base, toast string, w, h int) string {
	if toast == "" {
		return modal.Place(base, "", 0, 0, w, h)
	}
	tw, th := lipgloss.Width(toast), lipgloss.Height(toast)
	return modal.Place(base, toast, w-tw-1, h-th-1, w, h)
}
