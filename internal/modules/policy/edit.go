package policy

import (
	"os"
	"os/exec"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tonhe/headsup/internal/brand"
	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/ui/styles"
)

// editorCommand is $VISUAL, then $EDITOR, then vi, split into argv.
func editorCommand() []string {
	for _, k := range []string{"VISUAL", "EDITOR"} {
		if f := strings.Fields(os.Getenv(k)); len(f) > 0 {
			return f
		}
	}
	return []string{"vi"}
}

// beginDraft records what an edit starts from, unless a draft is already open.
func (m *Model) beginDraft() {
	if m.hasDraft {
		return
	}
	m.draft = m.cur
	m.baseRaw, m.baseAt, m.baseFile = m.cur, m.curAt, m.file
}

// setDraft stores an edited text; text equal to the base (allowing for the
// inline editor turning tabs into spaces) is no draft at all.
func (m *Model) setDraft(v string) {
	if v == m.baseRaw || editorNorm(v) == editorNorm(m.baseRaw) {
		m.hasDraft, m.draft = false, ""
		return
	}
	m.hasDraft, m.draft = true, v
}

func editorNorm(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\t", "    ")
}

// lossyInline reports whether the inline editor would change bytes the user
// did not touch (it stores tabs as spaces and drops carriage returns).
func lossyInline(s string) bool { return strings.ContainsAny(s, "\t\r") }

// --- inline editor -------------------------------------------------------------

func newTextarea(s *styles.Styles) textarea.Model {
	ta := textarea.New()
	ta.ShowLineNumbers = true
	ta.CharLimit = 0
	ta.MaxHeight = 0
	ta.Prompt = ""
	st := textarea.Style{
		Base:             lipgloss.NewStyle(),
		Text:             s.Text,
		LineNumber:       s.Muted,
		CursorLineNumber: s.Accent,
		CursorLine:       lipgloss.NewStyle().Background(s.R.Selection),
		EndOfBuffer:      s.Muted,
		Placeholder:      s.Muted,
		Prompt:           s.Muted,
	}
	ta.FocusedStyle, ta.BlurredStyle = st, st
	ta.Cursor.Style = lipgloss.NewStyle().Foreground(s.R.Accent)
	return ta
}

func (m *Model) startInline(ctx module.Context) tea.Cmd {
	m.beginDraft()
	ta := newTextarea(ctx.Styles())
	ta.SetWidth(76)
	ta.SetHeight(20)
	ta.SetValue(m.draft)
	ta, _ = ta.Update(tea.KeyMsg{Type: tea.KeyCtrlHome})
	if m.invalid != nil && m.invalid.line > 1 {
		for range m.invalid.line - 1 {
			ta.CursorDown()
		}
		ta.SetCursor(max(0, m.invalid.col-1))
	}
	m.ta, m.taOK = ta, true
	m.back, m.mode = m.mode, modeEdit
	return m.ta.Focus()
}

func (m *Model) stopEditing() {
	m.setDraft(m.ta.Value())
	m.ta.Blur()
	m.mode = modeView
}

func (m *Model) editKey(ctx module.Context, k tea.KeyMsg) tea.Cmd {
	switch k.String() {
	case "esc":
		m.stopEditing()
		return nil
	case "ctrl+s":
		m.stopEditing()
		if !m.hasDraft {
			return ctx.Toast(module.ToastInfo, "No changes to check.")
		}
		return m.startCheck(ctx, purposeApply)
	case "tab":
		m.ta.InsertString("  ")
		return nil
	}
	var cmd tea.Cmd
	m.ta, cmd = m.ta.Update(k)
	return cmd
}

// --- $EDITOR round trip ----------------------------------------------------------

type editorReadyMsg struct {
	path string
	err  error
}

type editorDoneMsg struct {
	path string
	err  error
}

type editorLoadedMsg struct {
	text string
	err  error
}

// startExternal writes the draft to a private temp file; the editor runs
// when editorReadyMsg arrives, through tea.ExecProcess.
func (m *Model) startExternal(module.Context) tea.Cmd {
	m.beginDraft()
	text := m.draft
	return func() tea.Msg {
		f, err := os.CreateTemp("", brand.Name+"-policy-*.hujson")
		if err != nil {
			return editorReadyMsg{err: err}
		}
		_, err = f.WriteString(text)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			os.Remove(f.Name())
			return editorReadyMsg{err: err}
		}
		return editorReadyMsg{path: f.Name()}
	}
}

func (m *Model) onEditorReady(msg editorReadyMsg) tea.Cmd {
	if msg.err != nil {
		return result(module.ActionResult{ActionID: "policy.editor", Err: msg.err})
	}
	argv := m.editor()
	c := exec.Command(argv[0], append(argv[1:], msg.path)...)
	path := msg.path
	return m.execProc(c, func(err error) tea.Msg { return editorDoneMsg{path: path, err: err} })
}

func (m *Model) onEditorDone(msg editorDoneMsg) tea.Cmd {
	return func() tea.Msg {
		b, err := os.ReadFile(msg.path)
		os.Remove(msg.path)
		if msg.err != nil {
			err = msg.err
		}
		return editorLoadedMsg{text: string(b), err: err}
	}
}

// onEditorLoaded takes the edited text back and goes straight to validation
// and the diff, so nothing is applied without a review.
func (m *Model) onEditorLoaded(ctx module.Context, msg editorLoadedMsg) tea.Cmd {
	if msg.err != nil {
		return result(module.ActionResult{ActionID: "policy.editor",
			Err: errorf("the editor exited with an error (%v); the draft is unchanged", msg.err)})
	}
	m.setDraft(msg.text)
	m.mode = modeView
	if !m.hasDraft {
		return ctx.Toast(module.ToastInfo, "No changes made in the editor.")
	}
	return m.startCheck(ctx, purposeApply)
}
