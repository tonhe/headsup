// Package service is the Service and Logs page (host mode): the headscale
// service's state with start, stop, restart and reload, and a live log
// viewer over the detected log source.
package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/icons"
	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/modules/registry"
	"github.com/tonhe/headsup/internal/store"
	"github.com/tonhe/headsup/internal/ui/kv"
	"github.com/tonhe/headsup/internal/ui/pane"
)

func init() { registry.Register(func() module.Module { return New() }) }

// Model is the service module.
type Model struct {
	ic icons.Set

	status     host.ServiceStatus
	statusErr  error
	haveStatus bool
	busy       string // a transition this module started: "restarting", ...
	refused    *RefusedError
	unhealthy  *UnhealthyError
	lastOK     string

	logs      *Viewer
	streamID  int
	ch        <-chan host.LogEntry
	streamErr error
	ended     bool
	cancel    context.CancelFunc

	search    textinput.Model
	searching bool
}

// New returns the module.
func New() *Model {
	in := textinput.New()
	in.Prompt = ""
	in.Placeholder = "regular expression"
	return &Model{ic: icons.Unicode, logs: NewViewer(RingSize), search: in}
}

func (m *Model) ID() string               { return "service" }
func (m *Model) Title() string            { return "Service" }
func (m *Model) Icon(ic icons.Set) string { return ic.Service }
func (m *Model) Order() int               { return 116 }
func (m *Model) Modes() module.Modes      { return module.ModeHost }
func (m *Model) Chord() string            { return "l" }
func (m *Model) Section() module.Section  { return module.SectionServer }
func (m *Model) Capturing() bool          { return m.searching }

func (m *Model) Init(ctx module.Context) tea.Cmd {
	m.ic = ctx.Icons()
	h := ctx.Host()
	if h == nil {
		return nil
	}
	return tea.Batch(fetchStatus(h), m.openLogs(ctx))
}

// openLogs (re)starts the log stream, cancelling any previous one.
func (m *Model) openLogs(ctx module.Context) tea.Cmd {
	h := ctx.Host()
	if h == nil {
		return nil
	}
	if m.cancel != nil {
		m.cancel()
	}
	c, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.streamID++
	m.streamErr, m.ended = nil, false
	return openStream(c, h, m.streamID)
}

func (m *Model) Update(ctx module.Context, msg tea.Msg) (module.Module, tea.Cmd) {
	switch msg := msg.(type) {
	case statusMsg:
		m.statusErr = msg.err
		if msg.err == nil {
			m.status, m.haveStatus = msg.st, true
		}
	case streamMsg:
		if msg.id != m.streamID {
			return m, nil
		}
		m.ch = msg.ch
		return m, readBatch(msg.id, msg.ch)
	case logErrMsg:
		if msg.id == m.streamID {
			m.streamErr = msg.err
		}
	case logMsg:
		if msg.id != m.streamID {
			return m, nil
		}
		m.logs.Append(msg.entries)
		if msg.closed {
			m.ended = true
			return m, nil
		}
		return m, readBatch(msg.id, m.ch)
	case store.UpdatedMsg:
		if m.busy == "" {
			return m, fetchStatus(ctx.Host())
		}
	case module.ActionResult:
		return m, m.onResult(ctx, msg)
	case tea.KeyMsg:
		return m, m.key(ctx, msg)
	}
	return m, nil
}

func (m *Model) key(ctx module.Context, k tea.KeyMsg) tea.Cmd {
	if m.searching {
		switch k.String() {
		case "enter":
			m.searching = false
			m.search.Blur()
			return nil
		case "esc":
			m.searching = false
			m.search.Blur()
			m.search.SetValue("")
			m.logs.SetSearch("")
			return nil
		}
		before := m.search.Value()
		var cmd tea.Cmd
		m.search, cmd = m.search.Update(k)
		if v := m.search.Value(); v != before {
			m.logs.SetSearch(v)
		}
		return cmd
	}
	switch k.String() {
	case "j", "down":
		m.logs.Move(1)
	case "k", "up":
		m.logs.Move(-1)
	case "pgdown", "ctrl+d", "ctrl+f":
		m.logs.Move(10)
	case "pgup", "ctrl+u", "ctrl+b":
		m.logs.Move(-10)
	case "home":
		m.logs.Home()
	case "end":
		m.logs.SetFollow(true)
	case "esc":
		switch {
		case m.logs.Expanded():
			m.logs.ToggleExpanded()
		case m.logs.Search() != "":
			m.search.SetValue("")
			m.logs.SetSearch("")
		}
	}
	return nil
}

// --- log actions ------------------------------------------------------------

func (m *Model) logActions() []module.Action {
	act := func(id, title string, keys []string, f func(module.Context) tea.Cmd) module.Action {
		return module.Action{ID: "service." + id, Title: title, Keys: keys,
			Run: func(ctx module.Context, _ module.Values) tea.Cmd { return f(ctx) }}
	}
	return []module.Action{
		act("follow", "Follow newest log lines", []string{"f", "G"}, func(ctx module.Context) tea.Cmd {
			if m.ended || m.streamErr != nil {
				m.logs.SetFollow(true)
				return m.openLogs(ctx)
			}
			m.logs.SetFollow(!m.logs.Following() || m.logs.Paused())
			m.logs.SetPaused(false)
			return nil
		}),
		act("level", "Cycle log level filter", []string{"l"}, func(module.Context) tea.Cmd {
			m.logs.CycleLevel()
			return nil
		}),
		act("search", "Search logs (regex)", []string{"/"}, func(module.Context) tea.Cmd {
			m.searching = true
			m.search.SetValue(m.logs.Search())
			m.search.CursorEnd()
			return m.search.Focus()
		}),
		act("pause", "Pause or resume logs", []string{"space"}, func(module.Context) tea.Cmd {
			m.logs.SetPaused(!m.logs.Paused())
			return nil
		}),
		act("nexterr", "Jump to next error", []string{"n"}, func(ctx module.Context) tea.Cmd {
			if !m.logs.NextError(1) {
				return ctx.Toast(module.ToastInfo, "No errors in the shown lines.")
			}
			return nil
		}),
		act("preverr", "Jump to previous error", []string{"N"}, func(ctx module.Context) tea.Cmd {
			if !m.logs.NextError(-1) {
				return ctx.Toast(module.ToastInfo, "No errors in the shown lines.")
			}
			return nil
		}),
		act("expand", "Show line fields", []string{"enter"}, func(module.Context) tea.Cmd {
			m.logs.ToggleExpanded()
			return nil
		}),
		{
			ID: "service.machine", Title: "Open mentioned machine", Keys: []string{"m"},
			Enabled: func(ctx module.Context) (bool, string) {
				e, ok := m.logs.Cursor()
				if !ok || len(nodeRefs(e, ctx.Store())) == 0 {
					return false, "This log line does not mention a machine."
				}
				return true, ""
			},
			Run: func(ctx module.Context, _ module.Values) tea.Cmd {
				e, _ := m.logs.Cursor()
				refs := nodeRefs(e, ctx.Store())
				if len(refs) == 0 {
					return nil
				}
				return ctx.Navigate("machines", module.SelectArgs{ID: refs[0]})
			},
		},
	}
}

func (m *Model) Actions() []module.Action {
	return append(m.serviceActions(), m.logActions()...)
}

func (m *Model) Hints() []module.Hint {
	if m.searching {
		return []module.Hint{{Key: "enter", Desc: "keep search"}, {Key: "esc", Desc: "clear"}}
	}
	pause := "pause"
	if m.logs.Paused() {
		pause = "resume"
	}
	return []module.Hint{
		{Key: "X", Desc: "restart"}, {Key: "H", Desc: "reload"}, {Key: "f", Desc: "follow"},
		{Key: "l", Desc: "level"}, {Key: "/", Desc: "search"}, {Key: "space", Desc: pause},
		{Key: "n", Desc: "next error"}, {Key: "enter", Desc: "fields"},
	}
}

// --- view -------------------------------------------------------------------

func (m *Model) View(ctx module.Context, r module.Rect) string {
	s := ctx.Styles()
	if r.W < 10 || r.H < 4 {
		return ""
	}
	lines := m.statusLines(ctx)
	statusH := min(len(lines)+2, max(3, r.H/3))
	iw, _ := pane.Inner(r.W, statusH)
	for i, l := range lines {
		lines[i] = pane.Fit(l, iw)
	}
	statusPane := pane.Render(s, "Service", strings.Join(lines, "\n"), r.W, statusH, false)

	rest := r.H - statusH
	searchBar := m.searching || m.logs.Search() != ""
	if searchBar {
		rest--
	}
	detailH := 0
	cur, hasCur := m.logs.Cursor()
	var detail string
	if m.logs.Expanded() && hasCur && rest >= 10 {
		pairs := detailPairs(s, cur, ctx.Store(), ctx.Now().Location())
		detailH = min(len(pairs)+2, rest/2)
		diw, _ := pane.Inner(r.W, detailH)
		detail = pane.Render(s, "Line", kv.Render(s, pairs, diw), r.W, detailH, false)
	}
	logsH := rest - detailH
	parts := []string{statusPane, m.logsPane(ctx, r.W, logsH)}
	if detail != "" {
		parts = append(parts, detail)
	}
	if searchBar {
		parts = append(parts, m.searchView(ctx, r.W))
	}
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func (m *Model) logsPane(ctx module.Context, w, h int) string {
	s, ic := ctx.Styles(), ctx.Icons()
	iw, ih := pane.Inner(w, h)
	title := fmt.Sprintf("Logs %s %d", ic.Bullet, m.logs.Len())
	if m.logs.Len() != m.logs.Total() {
		title += fmt.Sprintf("/%d", m.logs.Total())
	}
	var tags []string
	switch {
	case m.logs.Paused():
		t := "paused"
		if n := m.logs.Held(); n > 0 {
			t += fmt.Sprintf(" %s %d new", ic.Bullet, n)
		}
		tags = append(tags, s.Warn.Render(t))
	case m.ended:
		tags = append(tags, s.Muted.Render("stream ended (f to reconnect)"))
	case m.logs.Following():
		tags = append(tags, s.OK.Render("following"))
	}
	if lvl := m.logs.MinLevel(); lvl > host.LevelTrace {
		tags = append(tags, "level "+levelStyle(s, lvl).Render(">= "+lvl.String()))
	}
	extra := strings.Join(tags, " "+s.Muted.Render(ic.Bullet)+" ")

	var body string
	switch {
	case m.streamErr != nil:
		body = m.streamErrView(ctx, iw, ih)
	case m.logs.Total() == 0:
		body = s.Muted.Render("Waiting for log lines" + ic.Ellipsis)
	case m.logs.Len() == 0:
		body = s.Muted.Render("No lines match the current filter.")
	default:
		es, cur := m.logs.Window(ih)
		loc := ctx.Now().Location()
		rows := make([]string, len(es))
		for i, e := range es {
			rows[i] = renderLine(s, ic, e, loc, iw, i == cur)
		}
		body = strings.Join(rows, "\n")
	}
	return pane.Render(s, title, body, w, h, true, pane.WithExtra(extra))
}

func (m *Model) streamErrView(ctx module.Context, w, h int) string {
	s := ctx.Styles()
	var pe *host.ErrPermission
	lines := []string{s.Danger.Render("Cannot read headscale's logs.")}
	switch {
	case errors.As(m.streamErr, &pe):
		lines = append(lines, "", "This user is not allowed to read them.")
		if pe.FixCommand != "" {
			lines = append(lines, "Run this yourself, then press f to retry:", "", "  "+s.Accent.Render(pe.FixCommand))
		}
	case errors.Is(m.streamErr, host.ErrNoHost):
		lines = append(lines, "", "No log source was detected (journald, container or file).")
	default:
		lines = append(lines, "", m.streamErr.Error(), "", s.Muted.Render("Press f to retry."))
	}
	for i, l := range lines {
		lines[i] = pane.Fit(l, w)
	}
	if len(lines) > h {
		lines = lines[:h]
	}
	return strings.Join(lines, "\n")
}

func (m *Model) searchView(ctx module.Context, w int) string {
	s := ctx.Styles()
	prompt := s.Accent.Render("/ ")
	count := s.Muted.Render(fmt.Sprintf(" %d/%d", m.logs.Len(), m.logs.Total()))
	if err := m.logs.SearchErr(); err != nil {
		count = s.Warn.Render(" not a regex, matching literally") + count
	}
	room := w - lipgloss.Width(prompt) - lipgloss.Width(count)
	var left string
	if m.searching {
		m.search.Width = max(1, room-1)
		left = prompt + m.search.View()
	} else {
		left = s.Muted.Render("search: ") + s.Text.Render(m.logs.Search())
	}
	if lipgloss.Width(left) > room {
		left = ansi.Truncate(left, max(0, room), m.ic.Ellipsis) + ansi.ResetStyle
	}
	gap := max(0, w-lipgloss.Width(left)-lipgloss.Width(count))
	return left + strings.Repeat(" ", gap) + count
}
