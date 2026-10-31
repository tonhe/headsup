// Package serverconfig is the "Server Settings" editor for headscale's
// config.yaml: catalog-driven sections and field editors, deprecated-key
// migration, an $EDITOR escape hatch, and the managed-file pipeline (validate
// with headscale's own check, diff, confirm, atomic write with backup), then
// a restart offer with a health check and a one-key restore.
package serverconfig

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/host/files"
	"github.com/tonhe/headsup/internal/host/hsconfig"
	"github.com/tonhe/headsup/internal/icons"
	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/modules/registry"
	"github.com/tonhe/headsup/internal/store"
	"github.com/tonhe/headsup/internal/ui/pane"
	"github.com/tonhe/headsup/internal/ui/table"
)

func init() { registry.Register(func() module.Module { return New() }) }

type mode int

const (
	modeBrowse    mode = iota
	modeDiff           // unsaved changes vs. the file as opened
	modeReview         // validated plan: issues + diff, ctrl+s writes
	modeConflict       // file changed on disk: three-way view
	modeRestart        // written; restart offer
	modeUnhealthy      // restart left headscale unhealthy: logs + restore offer
)

// DefaultSettle is how long the health check waits between looks.
const DefaultSettle = 1500 * time.Millisecond

// Model is the Server Settings module.
type Model struct {
	file    *host.File // as opened
	loadErr error
	staged  []byte
	doc     *hsconfig.Doc // parse of staged; nil when staged is not valid YAML
	docErr  error
	base    *hsconfig.Doc // parse of file.Raw
	version string

	sec, cur, top int
	mode          mode
	scroll        int
	busy          string

	change        *host.Change  // last plan
	applied       *host.Applied // last write this session, for rollback
	validated     bool          // the live file is a write whose plan passed validation
	restartNeeded bool
	restarted     bool // a restart ran since the last write
	conflict      *conflict
	health        healthReport
	notice        string // last outcome line on the restart screen

	settle time.Duration
}

// New returns the module.
func New() *Model { return &Model{settle: DefaultSettle} }

func (m *Model) ID() string               { return "serverconfig" }
func (m *Model) Title() string            { return "Server Settings" }
func (m *Model) Icon(ic icons.Set) string { return ic.Settings }
func (m *Model) Order() int               { return 110 }
func (m *Model) Modes() module.Modes      { return module.ModeHost }
func (m *Model) Chord() string            { return "s" }
func (m *Model) Capturing() bool          { return false }

// Badge shows unsaved edits, then a pending restart.
func (m *Model) Badge(*store.Store) string {
	switch {
	case m.dirty():
		return "*"
	case m.restartNeeded:
		return "!"
	}
	return ""
}

func (m *Model) Init(ctx module.Context) tea.Cmd {
	if ctx.Host() == nil {
		return nil
	}
	m.version = ctx.Host().Env().Version
	return loadCmd(ctx.Host())
}

func (m *Model) dirty() bool {
	return m.file != nil && !bytes.Equal(m.staged, m.file.Raw)
}

func (m *Model) setFile(f *host.File) {
	m.file, m.loadErr = f, nil
	m.base, _ = hsconfig.Parse(f.Raw)
	m.setStaged(f.Raw)
}

func (m *Model) setStaged(b []byte) {
	m.staged = append([]byte(nil), b...)
	m.doc, m.docErr = hsconfig.Parse(m.staged)
	if m.docErr != nil {
		m.doc = nil
	}
}

func (m *Model) section() section { return sections[m.sec] }

func (m *Model) rows() []row {
	if m.section().link != "" || m.doc == nil {
		return nil
	}
	return rowsFor(m.section().name, m.version, m.doc, m.base)
}

func (m *Model) selected() (row, bool) {
	rs := m.rows()
	if m.cur >= 0 && m.cur < len(rs) {
		return rs[m.cur], true
	}
	return row{}, false
}

func (m *Model) moveSection(d int) {
	m.sec = (m.sec + d + len(sections)) % len(sections)
	m.cur, m.top = 0, 0
}

// plan starts validation of the staged text and shows the review.
func (m *Model) plan(ctx module.Context) tea.Cmd {
	m.busy = "Validating with headscale's config check..."
	return planCmd(ctx.Host(), m.file, m.staged)
}

func (m *Model) Update(ctx module.Context, msg tea.Msg) (module.Module, tea.Cmd) {
	switch msg := msg.(type) {
	case loadedMsg:
		m.busy = ""
		if msg.err != nil {
			m.loadErr = msg.err
			return m, nil
		}
		if !m.dirty() {
			m.setFile(msg.file)
		}
	case module.NavigateMsg:
		if ctx.Host() != nil && !m.dirty() && m.mode == modeBrowse && m.busy == "" {
			m.version = ctx.Host().Env().Version
			return m, loadCmd(ctx.Host())
		}
	case plannedMsg:
		m.busy = ""
		if msg.err != nil {
			return m, ctx.Toast(module.ToastError, errText(msg.err))
		}
		m.change, m.mode, m.scroll = msg.change, modeReview, 0
	case writtenMsg:
		return m, m.written(ctx, msg)
	case restartedMsg:
		return m, m.restartDone(ctx, msg)
	case editedMsg:
		return m, m.edited(ctx, msg)
	case tea.KeyMsg:
		return m, m.key(ctx, msg)
	}
	return m, nil
}

func (m *Model) written(ctx module.Context, msg writtenMsg) tea.Cmd {
	m.busy = ""
	const id = "serverconfig.apply"
	var cod *host.ErrChangedOnDisk
	switch {
	case errors.As(msg.err, &cod):
		m.conflict = &conflict{base: m.file.Raw, mine: m.staged, theirs: cod.Current}
		m.mode, m.scroll = modeConflict, 0
		return result(id, nil, "", true)
	case msg.err != nil:
		return result(id, errors.New(errText(msg.err)), "", false)
	}
	a := msg.applied
	m.applied, m.validated, m.restarted = &a, true, false
	m.setFile(msg.file)
	m.change = nil
	m.notice = fmt.Sprintf("Wrote %s. Backup: %s", a.File.Path, a.BackupPath)
	text := "Saved " + filepath.Base(a.File.Path) + "."
	if msg.change.Effect == host.EffectRestart {
		m.restartNeeded = true
		m.mode = modeRestart
		text += " Restart headscale to apply it."
	} else {
		m.mode = modeBrowse
	}
	return result(id, nil, text, false)
}

func (m *Model) restartDone(ctx module.Context, msg restartedMsg) tea.Cmd {
	m.busy = ""
	if msg.file != nil {
		m.setFile(msg.file)
	}
	if msg.action == "serverconfig.rollback" && msg.file != nil {
		// The restore happened even if the restart after it did not.
		m.applied, m.validated = nil, false
	}
	if msg.err != nil {
		return result(msg.action, errors.New(errText(msg.err)), "", false)
	}
	m.health = msg.report
	rollback := msg.action == "serverconfig.rollback"
	ranRestart := !rollback || m.restarted
	if !msg.report.healthy {
		m.restarted = true
		m.mode, m.scroll = modeUnhealthy, 0
		return result(msg.action, errors.New("headscale did not come back healthy (state "+orUnknown(msg.report.status.State)+")"), "", false)
	}
	m.mode = modeBrowse
	// After a restore without a restart the service never ran the undone
	// change, so nothing is pending either way.
	m.restartNeeded = false
	text := "headscale restarted and is healthy."
	switch {
	case rollback && ranRestart:
		text = "Restored the backup; headscale restarted and is healthy."
	case rollback:
		text = "Restored the backup."
	}
	m.restarted = ranRestart
	return result(msg.action, nil, text, false)
}

func (m *Model) edited(ctx module.Context, msg editedMsg) tea.Cmd {
	if msg.err != nil {
		return ctx.Toast(module.ToastError, msg.err.Error())
	}
	if bytes.Equal(msg.data, m.staged) {
		return ctx.Toast(module.ToastInfo, "No changes made in the editor.")
	}
	m.setStaged(msg.data)
	if !m.dirty() {
		m.mode = modeBrowse
		return ctx.Toast(module.ToastInfo, "The text matches the file again; nothing to save.")
	}
	return m.plan(ctx)
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

func result(id string, err error, msg string, silent bool) tea.Cmd {
	r := module.ActionResult{ActionID: id, Err: err, Message: msg, Silent: silent}
	return func() tea.Msg { return r }
}

func (m *Model) key(ctx module.Context, k tea.KeyMsg) tea.Cmd {
	switch m.mode {
	case modeBrowse:
		n := len(m.rows())
		switch k.String() {
		case "j", "down":
			m.cur = min(m.cur+1, max(0, n-1))
		case "k", "up":
			m.cur = max(m.cur-1, 0)
		case "home":
			m.cur = 0
		case "end", "G":
			m.cur = max(0, n-1)
		case "tab", "l", "right":
			m.moveSection(1)
		case "shift+tab", "h", "left":
			m.moveSection(-1)
		}
	case modeConflict:
		switch k.String() {
		case "r":
			return m.rebase(ctx)
		case "esc":
			m.mode, m.conflict = modeBrowse, nil
		default:
			m.scrollKey(k)
		}
	default:
		if k.String() == "esc" {
			m.mode = modeBrowse
			return nil
		}
		m.scrollKey(k)
	}
	return nil
}

func (m *Model) scrollKey(k tea.KeyMsg) {
	switch k.String() {
	case "j", "down":
		m.scroll++
	case "k", "up":
		m.scroll = max(0, m.scroll-1)
	case "pgdown", "ctrl+d", " ":
		m.scroll += 10
	case "pgup", "ctrl+u":
		m.scroll = max(0, m.scroll-10)
	case "home":
		m.scroll = 0
	}
}

// rebase carries the staged edits over onto the version now on disk.
func (m *Model) rebase(ctx module.Context) tea.Cmd {
	c := m.conflict
	merged, keys, err := Rebase(c.base, c.mine, c.theirs)
	if err != nil {
		c.rebaseErr = err
		return ctx.Toast(module.ToastError, "Cannot carry your changes over: "+err.Error())
	}
	f := *m.file
	f.Raw, f.SHA256 = append([]byte(nil), c.theirs...), files.Sum(c.theirs)
	m.file = &f
	m.base, _ = hsconfig.Parse(f.Raw)
	m.setStaged(merged)
	m.conflict = nil
	m.mode = modeBrowse
	return tea.Batch(ctx.Toast(module.ToastInfo, fmt.Sprintf("Carried %d change(s) over onto the version on disk.", len(keys))), m.plan(ctx))
}

// --- actions ------------------------------------------------------------------

func (m *Model) Actions() []module.Action {
	loaded := func(ctx module.Context) (bool, string) {
		switch {
		case m.loadErr != nil:
			return false, errText(m.loadErr)
		case m.file == nil:
			return false, "The config is still loading."
		}
		return true, ""
	}
	browsing := func(ctx module.Context) (bool, string) {
		if ok, why := loaded(ctx); !ok {
			return ok, why
		}
		if m.mode != modeBrowse {
			return false, "Press esc to go back to the fields first."
		}
		if m.busy != "" {
			return false, "Busy: " + m.busy
		}
		return true, ""
	}
	staging := func(ctx module.Context) (bool, string) {
		if ok, why := browsing(ctx); !ok {
			return ok, why
		}
		if ctx.ReadOnly() {
			return false, "This profile is read-only."
		}
		if m.doc == nil {
			return false, "The staged text is not valid YAML; fix it in $EDITOR (E) or discard (U)."
		}
		return true, ""
	}
	editable := func(ctx module.Context) (bool, string) {
		if m.section().link != "" {
			return browsing(ctx)
		}
		if ok, why := staging(ctx); !ok {
			return ok, why
		}
		if m.section().viewOnly {
			return false, "Database settings are view only here; change them by hand with $EDITOR (E)."
		}
		if _, ok := m.selected(); !ok {
			return false, "No field selected."
		}
		return true, ""
	}
	fileTarget := func(module.Context) []module.Target {
		if m.file == nil {
			return nil
		}
		return []module.Target{{Kind: "file", ID: m.file.Path, Name: filepath.Base(m.file.Path)}}
	}
	serviceTarget := func(module.Context) []module.Target {
		return []module.Target{{Kind: "service", ID: "headscale", Name: "headscale"}}
	}

	acts := []module.Action{
		{
			ID: "serverconfig.edit", Title: "Edit field", Keys: []string{"enter"}, Needs: host.NeedConfigR, Enabled: editable,
			Params: func(ctx module.Context) *module.FormSpec {
				if m.section().link != "" {
					return nil
				}
				r, _ := m.selected()
				return editForm(r.field, r.value, r.present)
			},
			Run: func(ctx module.Context, v module.Values) tea.Cmd {
				if l := m.section().link; l != "" {
					return ctx.Navigate(l, nil)
				}
				r, ok := m.selected()
				if !ok || m.doc == nil {
					return nil
				}
				changed, err := stage(m.doc, r.field, v["value"])
				if err != nil {
					return ctx.Toast(module.ToastError, err.Error())
				}
				m.setStaged(m.doc.Bytes())
				if !changed {
					return ctx.Toast(module.ToastInfo, "No change.")
				}
				return nil
			},
		},
		{
			ID: "serverconfig.reset", Title: "Reset field to default", Keys: []string{"x"}, Needs: host.NeedConfigR,
			Enabled: func(ctx module.Context) (bool, string) {
				if ok, why := editable(ctx); !ok || m.section().link != "" {
					return false, why
				}
				if r, _ := m.selected(); !r.present {
					return false, "Already at the default (not set in the file)."
				}
				return true, ""
			},
			Run: func(ctx module.Context, _ module.Values) tea.Cmd {
				r, _ := m.selected()
				if err := m.doc.Delete(r.field.Key); err != nil {
					return ctx.Toast(module.ToastError, err.Error())
				}
				m.setStaged(m.doc.Bytes())
				return nil
			},
		},
		{
			ID: "serverconfig.editor", Title: "Edit in $EDITOR", Keys: []string{"E"}, Needs: host.NeedConfigR,
			Enabled: func(ctx module.Context) (bool, string) {
				if ok, why := loaded(ctx); !ok {
					return ok, why
				}
				if ctx.ReadOnly() {
					return false, "This profile is read-only."
				}
				if m.mode != modeBrowse && m.mode != modeDiff && m.mode != modeReview {
					return false, "Press esc first."
				}
				return true, ""
			},
			Run: func(ctx module.Context, _ module.Values) tea.Cmd {
				m.mode = modeBrowse
				return editorCmd(m.staged)
			},
		},
		{
			ID: "serverconfig.migrate", Title: "Migrate deprecated keys", Keys: []string{"m"}, Needs: host.NeedConfigR,
			Enabled: func(ctx module.Context) (bool, string) {
				if ok, why := staging(ctx); !ok {
					return ok, why
				}
				if len(deprecatedPresent(m.doc, m.version)) == 0 {
					return false, "No deprecated keys for headscale " + orUnknown(m.version) + "."
				}
				return true, ""
			},
			Run: func(ctx module.Context, _ module.Values) tea.Cmd {
				next, moves, err := Migrate(m.staged, m.version)
				if err != nil {
					return ctx.Toast(module.ToastError, "Migration failed: "+err.Error())
				}
				var done, skipped []string
				for _, mv := range moves {
					if mv.Skipped != "" {
						skipped = append(skipped, mv.String())
					} else {
						done = append(done, mv.String())
					}
				}
				m.setStaged(next)
				cmds := []tea.Cmd{ctx.Toast(module.ToastInfo, fmt.Sprintf("Staged %d migration(s): %s", len(done), strings.Join(done, "; ")))}
				if len(skipped) > 0 {
					cmds = append(cmds, ctx.Toast(module.ToastWarn, strings.Join(skipped, "; ")))
				}
				if m.dirty() {
					cmds = append(cmds, m.plan(ctx))
				}
				return tea.Batch(cmds...)
			},
		},
		{
			ID: "serverconfig.diff", Title: "Show unsaved changes", Keys: []string{"d"},
			Enabled: func(ctx module.Context) (bool, string) {
				if !m.dirty() {
					return false, "No unsaved changes."
				}
				if m.mode != modeBrowse && m.mode != modeDiff {
					return false, "Press esc first."
				}
				return true, ""
			},
			Run: func(module.Context, module.Values) tea.Cmd {
				if m.mode == modeDiff {
					m.mode = modeBrowse
				} else {
					m.mode, m.scroll = modeDiff, 0
				}
				return nil
			},
		},
		m.applyAction(loaded, fileTarget),
		{
			ID: "serverconfig.restart", Title: "Restart headscale", Keys: []string{"X"}, Mutating: true,
			Danger: module.DangerConfirm, Needs: host.NeedService, Consequence: RestartConsequence, Targets: serviceTarget,
			Enabled: func(ctx module.Context) (bool, string) {
				if ok, why := loaded(ctx); !ok {
					return ok, why
				}
				if m.busy != "" {
					return false, "Busy: " + m.busy
				}
				if !m.restartNeeded && m.mode != modeUnhealthy {
					return false, "No saved change is waiting for a restart (the service module restarts on demand)."
				}
				return true, ""
			},
			Run: func(ctx module.Context, _ module.Values) tea.Cmd {
				m.busy = "Checking the config, restarting headscale..."
				return restartCmd(ctx.Host(), m.file.Path, "serverconfig.restart", m.validated, m.settle)
			},
		},
		m.rollbackAction(fileTarget),
		{
			ID: "serverconfig.discard", Title: "Discard unsaved changes", Keys: []string{"U"}, Danger: module.DangerConfirm,
			Consequence: "Your unsaved edits are thrown away; the file is not touched.",
			Enabled: func(module.Context) (bool, string) {
				if !m.dirty() {
					return false, "No unsaved changes."
				}
				return true, ""
			},
			Run: func(module.Context, module.Values) tea.Cmd {
				m.setStaged(m.file.Raw)
				m.mode, m.change, m.conflict = modeBrowse, nil, nil
				return nil
			},
		},
	}
	return acts
}

// applyAction is ctrl+s: in the field view it validates and shows the
// review; on the review it writes, confirmed according to the change's
// danger.
func (m *Model) applyAction(loaded func(module.Context) (bool, string), fileTarget func(module.Context) []module.Target) module.Action {
	a := module.Action{ID: "serverconfig.apply", Title: "Review and save", Keys: []string{"ctrl+s"}, Needs: host.NeedConfigR}
	if m.mode == modeReview && m.change != nil {
		c := m.change
		a.Title = "Write " + filepath.Base(c.File.Path)
		a.Mutating, a.Needs, a.Danger = true, host.NeedConfigW, asDanger(c.Danger)
		a.Consequence, a.Targets = writeConsequence(c), fileTarget
		a.Enabled = func(ctx module.Context) (bool, string) {
			if m.busy != "" {
				return false, "Busy: " + m.busy
			}
			if !c.Valid() {
				return false, "Validation failed; nothing was written. Fix the errors (esc, or E for $EDITOR)."
			}
			return true, ""
		}
		a.Run = func(ctx module.Context, _ module.Values) tea.Cmd {
			m.busy = "Writing..."
			return writeCmd(ctx.Host(), c)
		}
		return a
	}
	a.Enabled = func(ctx module.Context) (bool, string) {
		if ok, why := loaded(ctx); !ok {
			return ok, why
		}
		if m.busy != "" {
			return false, "Busy: " + m.busy
		}
		if !m.dirty() {
			return false, "No unsaved changes."
		}
		if m.mode == modeConflict {
			return false, "The file changed on disk: press r to carry your changes over, or esc."
		}
		return true, ""
	}
	a.Run = func(ctx module.Context, _ module.Values) tea.Cmd { return m.plan(ctx) }
	return a
}

func (m *Model) rollbackAction(fileTarget func(module.Context) []module.Target) module.Action {
	a := module.Action{ID: "serverconfig.rollback", Title: "Restore the backup", Keys: []string{"u"}, Mutating: true,
		Danger: module.DangerConfirm, Needs: host.NeedConfigW, Targets: fileTarget}
	restart := m.restarted
	if m.applied != nil {
		a.Consequence = fmt.Sprintf("Restores %s from the backup taken at %s.", m.applied.File.Path, m.applied.At.Format(time.TimeOnly))
		if restart {
			a.Title = "Restore the backup and restart"
			a.Needs |= host.NeedService
			a.Consequence += " Then " + RestartConsequence
		}
	}
	a.Enabled = func(module.Context) (bool, string) {
		if m.applied == nil {
			return false, "Nothing written in this session to restore."
		}
		if m.busy != "" {
			return false, "Busy: " + m.busy
		}
		if m.dirty() {
			return false, "Discard (U) or save your unsaved edits first."
		}
		return true, ""
	}
	a.Run = func(ctx module.Context, _ module.Values) tea.Cmd {
		m.busy = "Restoring the backup..."
		return rollbackCmd(ctx.Host(), *m.applied, restart, m.settle)
	}
	return a
}

func (m *Model) Hints() []module.Hint {
	switch m.mode {
	case modeDiff:
		return []module.Hint{{Key: "ctrl+s", Desc: "review & save"}, {Key: "d/esc", Desc: "back"}}
	case modeReview:
		if m.change != nil && !m.change.Valid() {
			return []module.Hint{{Key: "E", Desc: "$EDITOR"}, {Key: "esc", Desc: "back"}}
		}
		return []module.Hint{{Key: "ctrl+s", Desc: "write"}, {Key: "j/k", Desc: "scroll"}, {Key: "esc", Desc: "back"}}
	case modeConflict:
		return []module.Hint{{Key: "r", Desc: "carry changes over"}, {Key: "esc", Desc: "back"}}
	case modeRestart:
		return []module.Hint{{Key: "X", Desc: "restart now"}, {Key: "u", Desc: "undo write"}, {Key: "esc", Desc: "later"}}
	case modeUnhealthy:
		return []module.Hint{{Key: "u", Desc: "restore & restart"}, {Key: "X", Desc: "retry restart"}, {Key: "esc", Desc: "back"}}
	}
	if m.section().link != "" {
		return []module.Hint{{Key: "enter", Desc: "open " + m.section().name}, {Key: "tab", Desc: "section"}}
	}
	h := []module.Hint{{Key: "enter", Desc: "edit"}, {Key: "tab", Desc: "section"}, {Key: "E", Desc: "$EDITOR"}}
	if len(deprecatedPresent(m.doc, m.version)) > 0 {
		h = append(h, module.Hint{Key: "m", Desc: "migrate"})
	}
	if m.dirty() {
		h = append(h, module.Hint{Key: "d", Desc: "diff"}, module.Hint{Key: "ctrl+s", Desc: "save"})
	}
	if m.restartNeeded {
		h = append(h, module.Hint{Key: "X", Desc: "restart"})
	}
	return h
}

// --- view -------------------------------------------------------------------

func (m *Model) View(ctx module.Context, r module.Rect) string {
	s, ic := ctx.Styles(), ctx.Icons()
	if m.loadErr != nil {
		body := s.Danger.Render(ic.Cross+" Cannot read the headscale config.") + "\n\n" + s.Muted.Render(errText(m.loadErr))
		return pane.Render(s, m.Title(), body, r.W, r.H, true)
	}
	if m.file == nil {
		return pane.Render(s, m.Title(), s.Muted.Render(ic.Pending+" Loading the config..."), r.W, r.H, true)
	}
	switch m.mode {
	case modeDiff:
		return m.diffView(ctx, r)
	case modeReview:
		return m.reviewView(ctx, r)
	case modeConflict:
		return m.conflictView(ctx, r)
	case modeRestart, modeUnhealthy:
		return m.restartView(ctx, r)
	}
	return m.browseView(ctx, r)
}

func (m *Model) titleRight() string {
	switch {
	case m.dirty():
		return "unsaved changes"
	case m.restartNeeded:
		return "restart needed"
	}
	return ""
}

func (m *Model) busyLine(ctx module.Context, w int) []string {
	if m.busy == "" {
		return nil
	}
	return []string{ctx.Styles().Pending.Render(table.Fit(ctx.Icons().Pending+" "+m.busy, w, lipgloss.Left, ctx.Icons().Ellipsis))}
}

func (m *Model) sectionTabs(ctx module.Context, w int) string {
	s := ctx.Styles()
	var parts []string
	for i, sec := range sections {
		label := " " + sec.name + " "
		if i == m.sec {
			parts = append(parts, s.RowSel.Render(label))
		} else if sec.link != "" {
			parts = append(parts, s.Link.Render(label))
		} else {
			parts = append(parts, s.Muted.Render(label))
		}
	}
	return table.Fit(strings.Join(parts, ""), w, lipgloss.Left, ctx.Icons().Ellipsis)
}

const detailH = 5

func (m *Model) browseView(ctx module.Context, r module.Rect) string {
	s, ic := ctx.Styles(), ctx.Icons()
	w := r.W - 4
	mainH := r.H - detailH
	bodyH := mainH - 2
	extra := m.file.Path
	if m.version != "" {
		extra += " " + ic.Bullet + " headscale " + m.version
	}
	lines := []string{m.sectionTabs(ctx, w)}
	lines = append(lines, m.busyLine(ctx, w)...)
	if m.docErr != nil {
		lines = append(lines, "", s.Danger.Render(table.Fit(ic.Cross+" The staged text is not valid YAML: "+m.docErr.Error(), w, lipgloss.Left, ic.Ellipsis)),
			s.Muted.Render("Fix it in $EDITOR (E) or discard your changes (U)."))
		return pane.Render(s, m.Title(), strings.Join(lines, "\n"), r.W, r.H, true, pane.WithExtra(extra), pane.WithRight(m.titleRight()))
	}
	sec := m.section()
	var detail string
	switch {
	case sec.link != "":
		lines = append(lines, "",
			s.Text.Render(sec.name+" settings have their own editor."),
			s.Muted.Render("Press enter to open it ("+sec.chord+")."))
		detail = pane.Render(s, sec.name, s.Muted.Render(table.Fit(sec.name+" keys live in config.yaml but are edited in the "+sec.name+" module.", w, lipgloss.Left, ic.Ellipsis)), r.W, detailH, false)
	default:
		rs := m.rows()
		m.cur = min(m.cur, max(0, len(rs)-1))
		if sec.viewOnly {
			lines = append(lines, s.Warn.Render(table.Fit(ic.Lock+" View only: database settings are changed by hand ($EDITOR, E).", w, lipgloss.Left, ic.Ellipsis)))
		}
		lines = append(lines, m.rowLines(ctx, rs, w, bodyH-len(lines))...)
		detail = m.detailView(ctx, rs, r.W)
	}
	main := pane.Render(s, m.Title(), strings.Join(lines, "\n"), r.W, mainH, true, pane.WithExtra(extra), pane.WithRight(m.titleRight()))
	return lipgloss.JoinVertical(lipgloss.Left, main, detail)
}

// columns returns the key, value, default and help widths for w.
func columns(rs []row, w int) (kw, vw, dw, hw int) {
	const effW = 13
	for _, r := range rs {
		kw = max(kw, lipgloss.Width(r.field.Key))
	}
	kw = min(kw, 36, w*2/5)
	if w >= 88 {
		dw = 20
	}
	if w >= 150 {
		hw = 40
	}
	// cursor+mark (4), key, gap, value, gap, danger, gap, effect.
	vw = w - 4 - kw - 1 - 1 - 1 - 1 - effW
	if dw > 0 {
		vw -= dw + 1
	}
	if hw > 0 {
		vw -= hw + 1
	}
	return kw, max(4, vw), dw, hw
}

func (m *Model) rowLines(ctx module.Context, rs []row, w, h int) []string {
	s, ic := ctx.Styles(), ctx.Icons()
	if len(rs) == 0 {
		return []string{s.Muted.Render("No settings in this section for headscale " + orUnknown(m.version) + ".")}
	}
	kw, vw, dw, hw := columns(rs, w)
	fit := func(t string, n int) string { return table.Fit(t, n, lipgloss.Left, ic.Ellipsis) }
	head := "    " + fit("KEY", kw) + " " + fit("VALUE", vw)
	if dw > 0 {
		head += " " + fit("DEFAULT", dw)
	}
	head += "   " + fit("EFFECT", 13)
	if hw > 0 {
		head += " " + fit("HELP", hw)
	}
	out := []string{s.TableHeader.Render(fit(head, w))}
	h--
	if h <= 0 {
		return out
	}
	if m.cur < m.top {
		m.top = m.cur
	}
	if m.cur >= m.top+h {
		m.top = m.cur - h + 1
	}
	m.top = max(0, min(m.top, len(rs)-h))
	for i := m.top; i < len(rs) && i < m.top+h; i++ {
		r := rs[i]
		sel := i == m.cur
		mark := " "
		switch {
		case r.modified:
			mark = s.Accent.Render(ic.Bullet)
		case r.deprecated:
			mark = s.Warn.Render(ic.Warn)
		}
		val := displayValue(ic, r)
		valSt := s.Text
		if !r.present {
			val, valSt = "unset, default "+defaultText(r.field), s.Muted
			if dw > 0 {
				val = "unset"
			}
		}
		if r.deprecated {
			to := r.field.ReplacedBy
			if to == "" {
				to = "removed"
			}
			val += " " + ic.Arrow + " " + to
			valSt = s.Warn
		}
		danger := " "
		if r.field.Danger == host.DangerCritical {
			danger = s.Danger.Render(ic.Lock)
		}
		cursor := "  "
		if sel {
			cursor = s.Accent.Render(ic.Arrow) + " "
		}
		line := cursor + mark + " " + s.Strong.Render(fit(r.field.Key, kw)) + " " + valSt.Render(fit(val, vw))
		if dw > 0 {
			line += " " + s.Muted.Render(fit(defaultText(r.field), dw))
		}
		line += " " + danger + " " + s.Muted.Render(fit(effectLabel(r.field.Effect), 13))
		if hw > 0 {
			line += " " + s.Muted.Render(fit(r.field.Help, hw))
		}
		if sel {
			line = s.RowSel.Render(fit(line, w))
		}
		out = append(out, line)
	}
	return out
}

func (m *Model) detailView(ctx module.Context, rs []row, w int) string {
	s, ic := ctx.Styles(), ctx.Icons()
	r, ok := m.selected()
	if !ok || len(rs) == 0 {
		return pane.Render(s, "Field", "", w, detailH, false)
	}
	iw := w - 4
	fit := func(t string) string { return table.Fit(t, iw, lipgloss.Left, ic.Ellipsis) }
	x := r.field
	l1 := x.Help
	meta := fmt.Sprintf("default %s %s %s %s %s", defaultText(x), ic.Bullet, typeLabel(x), ic.Bullet, effectLabel(x.Effect))
	if x.Since != "" {
		meta += " " + ic.Bullet + " since " + x.Since
	}
	var l3 string
	switch {
	case r.deprecated && x.ReplacedBy != "":
		l3 = s.Warn.Render(fit(fmt.Sprintf("%s Deprecated in %s: use %s. Press m to migrate.", ic.Warn, x.Deprecated, x.ReplacedBy)))
	case r.deprecated:
		l3 = s.Warn.Render(fit(fmt.Sprintf("%s Not used since %s. Press m to remove it.", ic.Warn, x.Deprecated)))
	case m.section().viewOnly:
		l3 = s.Muted.Render(fit(ic.Lock + " View only."))
	case x.Danger == host.DangerCritical:
		l3 = s.Danger.Render(fit(ic.Lock + " Critical: saving asks you to type the file name to confirm."))
	case r.modified:
		l3 = s.Accent.Render(fit(ic.Bullet + " Changed, not saved yet (d diff, ctrl+s save)."))
	}
	body := strings.Join([]string{s.Text.Render(fit(l1)), s.Muted.Render(fit(meta)), l3}, "\n")
	return pane.Render(s, x.Key, body, w, detailH, false)
}

func (m *Model) diffView(ctx module.Context, r module.Rect) string {
	s, ic := ctx.Styles(), ctx.Icons()
	w, h := r.W-4, r.H-2
	d := files.Diff(m.file.Raw, m.staged)
	added, removed := files.Changed(d)
	ls := diffLines(s, ic, d, w)
	ls, m.scroll = window(ls, m.scroll, h)
	return pane.Render(s, "Unsaved changes", strings.Join(ls, "\n"), r.W, r.H, true,
		pane.WithExtra(m.file.Path), pane.WithRight(fmt.Sprintf("+%d -%d", added, removed)))
}

func (m *Model) reviewView(ctx module.Context, r module.Rect) string {
	s, ic := ctx.Styles(), ctx.Icons()
	c := m.change
	w, h := r.W-4, r.H-2
	added, removed := files.Changed(c.Diff)
	var top []string
	top = append(top, m.busyLine(ctx, w)...)
	if c.Valid() {
		how := "y/N"
		if asDanger(c.Danger) == module.DangerCritical {
			how = "type " + filepath.Base(c.File.Path)
		}
		top = append(top, s.OK.Render(table.Fit(fmt.Sprintf("%s Valid. %s. ctrl+s writes (confirm: %s).", ic.Check, effectSentence(c.Effect), how), w, lipgloss.Left, ic.Ellipsis)))
	} else {
		top = append(top, s.Danger.Render(table.Fit(ic.Cross+" Validation failed; nothing will be written. Fix the errors below (esc, or E for $EDITOR).", w, lipgloss.Left, ic.Ellipsis)))
	}
	top = append(top, issueLines(s, ic, c.Issues, w)...)
	top = append(top, "")
	ls := append(top, diffLines(s, ic, c.Diff, w)...)
	ls, m.scroll = window(ls, m.scroll, h)
	return pane.Render(s, "Review "+filepath.Base(c.File.Path), strings.Join(ls, "\n"), r.W, r.H, true,
		pane.WithExtra(c.File.Path), pane.WithRight(fmt.Sprintf("+%d -%d", added, removed)))
}

func effectSentence(e host.Effect) string {
	switch e {
	case host.EffectLive:
		return "Applies on save"
	case host.EffectReload:
		return "Applies on reload"
	}
	return "Needs a restart to take effect"
}

func (m *Model) conflictView(ctx module.Context, r module.Rect) string {
	s, ic := ctx.Styles(), ctx.Icons()
	c := m.conflict
	w := r.W - 4
	headH := 4
	half := (r.H - headH) / 2
	head := []string{
		s.Warn.Render(table.Fit(ic.Warn+" "+m.file.Path+" changed on disk since it was opened. Nothing was written.", w, lipgloss.Left, ic.Ellipsis)),
		s.Muted.Render(table.Fit("r carries your changes over onto the version on disk, then shows the review; esc keeps them unsaved.", w, lipgloss.Left, ic.Ellipsis)),
	}
	if c.rebaseErr != nil {
		head[1] = s.Danger.Render(table.Fit(ic.Cross+" "+c.rebaseErr.Error()+" (use E to merge by hand)", w, lipgloss.Left, ic.Ellipsis))
	}
	theirs, _ := window(diffLines(s, ic, files.Diff(c.base, c.theirs), w), m.scroll, half-2)
	mine, _ := window(diffLines(s, ic, files.Diff(c.base, c.mine), w), m.scroll, r.H-headH-half-2)
	return lipgloss.JoinVertical(lipgloss.Left,
		pane.Render(s, "Changed on disk", strings.Join(head, "\n"), r.W, headH, true),
		pane.Render(s, "On disk now vs. when opened", strings.Join(theirs, "\n"), r.W, half, false),
		pane.Render(s, "Your changes vs. when opened", strings.Join(mine, "\n"), r.W, r.H-headH-half, false))
}

func (m *Model) restartView(ctx module.Context, r module.Rect) string {
	s, ic := ctx.Styles(), ctx.Icons()
	w, h := r.W-4, r.H-2
	fit := func(t string) string { return table.Fit(t, w, lipgloss.Left, ic.Ellipsis) }
	wrap := func(st lipgloss.Style, t string) []string {
		return strings.Split(st.Render(lipgloss.NewStyle().Width(w).Render(t)), "\n")
	}
	var ls []string
	ls = append(ls, m.busyLine(ctx, w)...)
	title := "Saved"
	if m.mode == modeUnhealthy {
		title = "Restart failed"
		st := m.health.status
		ls = append(ls, s.Danger.Render(fit(fmt.Sprintf("%s headscale did not come back healthy (state %s).", ic.Cross, orUnknown(st.State)))), "")
		if m.applied != nil {
			ls = append(ls, s.Text.Render(fit("u restores the backup taken before your change and restarts headscale again.")))
		}
		ls = append(ls, s.Muted.Render(fit("X retries the restart "+ic.Bullet+" esc goes back to the fields")), "", s.Strong.Render("Last log lines"))
		switch {
		case m.health.logErr != nil:
			ls = append(ls, s.Muted.Render(fit("Logs unavailable: "+errText(m.health.logErr))))
		case len(m.health.logs) == 0:
			ls = append(ls, s.Muted.Render("(no log lines)"))
		}
		for _, l := range m.health.logs {
			ls = append(ls, s.Muted.Render(fit(l)))
		}
	} else {
		ls = append(ls, s.OK.Render(fit(ic.Check+" "+m.notice)), "")
		ls = append(ls, s.Text.Render(fit("This change takes effect after headscale restarts.")))
		ls = append(ls, wrap(s.Warn, ic.Warn+" "+RestartConsequence)...)
		ls = append(ls, "", s.Muted.Render(fit("X restart now (config check first) "+ic.Bullet+" u undo the write "+ic.Bullet+" esc later")))
	}
	ls, m.scroll = window(ls, m.scroll, h)
	return pane.Render(s, title, strings.Join(ls, "\n"), r.W, r.H, true, pane.WithExtra(m.file.Path))
}
