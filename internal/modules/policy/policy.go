// Package policy is the Access Controls module: view the HuJSON policy with
// syntax highlighting, edit it inline or in $EDITOR, validate it, review the
// diff, apply it after confirmation, browse it as a tree with where-used
// cross-links, roll back from local history, and (on the host) see the packet
// filter headscale compiled from it.
//
// In host mode with `policy.mode: file`, edits go through the managed-file
// pipeline (validate with `headscale policy check`, back up, atomic write,
// reload, confirm active, roll back on failure). In remote mode a file-mode
// policy is view-only.
package policy

import (
	"context"
	"os/exec"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/tonhe/headsup/internal/api"
	"github.com/tonhe/headsup/internal/brand"
	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/icons"
	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/modules/registry"
	pol "github.com/tonhe/headsup/internal/policy"
	"github.com/tonhe/headsup/internal/store"
	"github.com/tonhe/headsup/internal/ui/codeview"
)

func init() { registry.Register(func() module.Module { return New() }) }

type mode int

const (
	modeView    mode = iota
	modeEdit         // inline editor owns the keys
	modeInvalid      // validation failed: error position over the draft
	modeReview       // validated: diff, waiting for ctrl+s to confirm and apply
	modeDiff         // unsaved draft against the current policy
	modeExplore      // tree of groups, tags, hosts, and rules
	modeHistory      // locally kept revisions
	modeFilter       // compiled packet filter (host)
	modePreview      // access preview: who can reach what
)

// refreshEvery is how often the viewer refetches the policy in the background.
const refreshEvery = 30 * time.Second

// fileModeRemote explains acceptance scenario 3 (remote mode, file policy).
var fileModeRemote = "This server reads its policy from a file (policy.mode: file). File-mode policies can only be " +
	"edited on the host: run " + brand.Name + " on the headscale server. Viewing and exploring still work here."

// Model is the policy module.
type Model struct {
	// Seams for tests.
	hist     *pol.History
	editor   func() []string
	execProc func(*exec.Cmd, tea.ExecCallback) tea.Cmd

	// The live policy.
	loading   bool
	loaded    bool
	loadErr   error
	fetchedAt time.Time
	cur       string
	curAt     time.Time  // server updatedAt (zero when unknown)
	file      *host.File // file mode: the opened policy file
	fileErr   error      // file mode: why it could not be opened
	doc       *pol.Document
	docErr    error
	code      codeview.View
	offset    int
	cursor    int // 1-based highlighted line in the viewer (from the explorer)

	// The draft and what it was based on.
	hasDraft bool
	draft    string
	baseRaw  string
	baseAt   time.Time
	baseFile *host.File

	mode mode
	back mode // where esc returns from diff/invalid/review
	ta   textarea.Model
	taOK bool

	// Validation and review.
	checking bool
	invalid  *problem
	review   *review

	// File-mode outcome.
	applied   *host.Applied
	reloadErr string

	// Remote file mode, learned when SetPolicy is refused.
	viewOnly string

	exp  explorer
	hst  historyView
	filt filterView
	prev previewer
}

// problem is a validation failure with its position.
type problem struct {
	line, col int
	msg       string
	source    string // "server", "local", "policy check", "schema"
}

// New returns the module.
func New() *Model {
	return &Model{
		hist:     pol.NewHistory(pol.DefaultHistoryDir()),
		editor:   editorCommand,
		execProc: tea.ExecProcess,
	}
}

func (m *Model) ID() string               { return "policy" }
func (m *Model) Title() string            { return "Access controls" }
func (m *Model) Icon(ic icons.Set) string { return ic.Policy }
func (m *Model) Order() int               { return 70 }
func (m *Model) Modes() module.Modes      { return module.ModeAPIHost }
func (m *Model) Chord() string            { return "a" }
func (m *Model) Section() module.Section  { return module.SectionTailnet }
func (m *Model) Badge(*store.Store) string {
	return ""
}

// Capturing is true while the inline editor owns the keyboard.
func (m *Model) Capturing() bool { return m.mode == modeEdit }

// Dirty reports unsaved policy edits, for the app's quit guard.
func (m *Model) Dirty() bool { return m.hasDraft }

// Unsaved implements module.Unsaved.
func (m *Model) Unsaved() bool { return m.Dirty() }

func (m *Model) Init(module.Context) tea.Cmd { return nil }

// fileMode is true when this app is on the host and headscale reads the policy from a file.
func fileMode(ctx module.Context) bool {
	return ctx.HostMode() && ctx.Host() != nil && ctx.Host().Env().PolicyMode == "file"
}

// profileKey names this connection's history directory: the active
// profile, falling back to the host name or server URL when the app was
// given no profile name.
func profileKey(ctx module.Context) string {
	if p := ctx.Profile(); p != "" {
		return p
	}
	if ctx.HostMode() && ctx.Host() != nil {
		return "host-" + ctx.Host().Env().Hostname
	}
	return ctx.ServerURL()
}

// --- messages -----------------------------------------------------------------

type loadedMsg struct {
	raw     string
	at      time.Time
	file    *host.File
	err     error
	fileErr error // file mode: why the policy file could not be opened
	when    time.Time
}

func call(f func(context.Context) error) error {
	c, cancel := api.WithTimeout(context.Background())
	defer cancel()
	return f(c)
}

// load fetches the live policy: the file in host file mode, otherwise the API.
func (m *Model) load(ctx module.Context) tea.Cmd {
	if m.loading {
		return nil
	}
	m.loading = true
	b, h, fm := ctx.Backend(), ctx.Host(), fileMode(ctx)
	hist, prof, now := m.hist, profileKey(ctx), ctx.Now()
	return func() tea.Msg {
		msg := loadedMsg{when: now}
		if fm {
			f, err := h.Open(host.FilePolicy, "")
			if err == nil {
				msg.raw, msg.file = string(f.Raw), f
			} else {
				msg.fileErr = err
			}
		}
		if msg.file == nil {
			var p api.Policy
			msg.err = call(func(c context.Context) (err error) { p, err = b.GetPolicy(c); return })
			msg.raw, msg.at = p.Raw, p.UpdatedAt
		}
		if msg.err == nil && hist != nil && msg.raw != "" {
			_, _, _ = hist.Record(prof, msg.raw, pol.SourceServer, msg.at, now)
		}
		return msg
	}
}

func (m *Model) setCurrent(raw string, at time.Time, f *host.File) {
	m.cur, m.curAt, m.file = raw, at, f
	m.doc, m.docErr = pol.Parse([]byte(raw))
}

// --- update -------------------------------------------------------------------

func (m *Model) Update(ctx module.Context, msg tea.Msg) (module.Module, tea.Cmd) {
	switch msg := msg.(type) {
	case store.UpdatedMsg:
		if !m.loaded || (ctx.Now().Sub(m.fetchedAt) > refreshEvery && m.mode != modeEdit) {
			return m, m.load(ctx)
		}
	case module.NavigateMsg:
		return m, m.load(ctx)
	case loadedMsg:
		m.loading = false
		m.fetchedAt = msg.when
		m.loadErr, m.fileErr = msg.err, msg.fileErr
		if msg.err != nil {
			return m, nil
		}
		m.loaded = true
		m.setCurrent(msg.raw, msg.at, msg.file)
		return m, nil
	case checkedMsg:
		return m, m.onChecked(ctx, msg)
	case appliedMsg:
		return m, m.onApplied(ctx, msg)
	case fileAppliedMsg:
		return m, m.onFileApplied(ctx, msg)
	case rolledBackMsg:
		return m, m.onRolledBack(ctx, msg)
	case editorReadyMsg:
		return m, m.onEditorReady(msg)
	case editorDoneMsg:
		return m, m.onEditorDone(msg)
	case editorLoadedMsg:
		return m, m.onEditorLoaded(ctx, msg)
	case historyMsg:
		m.hst.revs, m.hst.err = msg.revs, msg.err
		m.hst.cursor = min(m.hst.cursor, max(0, len(m.hst.revs)-1))
		return m, m.hst.preview(m.hist)
	case revisionMsg:
		return m, m.onRevision(ctx, msg)
	case filterMsg:
		m.filt.rules, m.filt.err, m.filt.loading = msg.rules, msg.err, false
		return m, nil
	case tea.KeyMsg:
		return m, m.key(ctx, msg)
	}
	if m.mode == modeEdit {
		var cmd tea.Cmd
		m.ta, cmd = m.ta.Update(msg)
		return m, cmd
	}
	return m, nil
}

// key handles keys the app did not bind to an action.
func (m *Model) key(ctx module.Context, k tea.KeyMsg) tea.Cmd {
	if m.mode == modeEdit {
		return m.editKey(ctx, k)
	}
	switch m.mode {
	case modeExplore:
		if cmd, ok := m.exploreKey(ctx, k); ok {
			return cmd
		}
	case modeHistory:
		if cmd, ok := m.historyKey(k); ok {
			return cmd
		}
	case modePreview:
		if cmd, ok := m.previewKey(ctx, k); ok {
			return cmd
		}
	}
	switch k.String() {
	case "esc":
		switch m.mode {
		case modeView:
			m.cursor = 0
		case modeReview, modeInvalid, modeDiff:
			m.mode, m.review = modeView, nil
		default:
			m.mode = modeView
		}
		m.offset = 0
	case "j", "down":
		m.offset++
	case "k", "up":
		m.offset = max(0, m.offset-1)
	case "ctrl+d", "pgdown":
		m.offset += 10
	case "ctrl+u", "pgup":
		m.offset = max(0, m.offset-10)
	case "G", "end":
		m.offset = 1 << 20
	case "home":
		m.offset = 0
	}
	return nil
}

func (m *Model) Hints() []module.Hint {
	switch m.mode {
	case modeEdit:
		return []module.Hint{{Key: "ctrl+s", Desc: "check & review"}, {Key: "esc", Desc: "stop editing"}}
	case modeReview:
		return []module.Hint{{Key: "ctrl+s", Desc: "apply"}, {Key: "e", Desc: "edit"}, {Key: "esc", Desc: "back"}}
	case modeInvalid:
		return []module.Hint{{Key: "e", Desc: "fix"}, {Key: "E", Desc: "$EDITOR"}, {Key: "esc", Desc: "back"}}
	case modeExplore:
		return []module.Hint{{Key: "j/k", Desc: "move"}, {Key: "enter", Desc: "show in policy"}, {Key: "m", Desc: "machines"}, {Key: "esc", Desc: "back"}}
	case modeHistory:
		return []module.Hint{{Key: "j/k", Desc: "move"}, {Key: "u", Desc: "restore"}, {Key: "esc", Desc: "back"}}
	case modeFilter, modeDiff:
		return []module.Hint{{Key: "j/k", Desc: "scroll"}, {Key: "esc", Desc: "back"}}
	case modePreview:
		return []module.Hint{{Key: "j/k", Desc: "source"}, {Key: "ctrl+d/u", Desc: "scroll"}, {Key: "m", Desc: "machines"}, {Key: "esc", Desc: "back"}}
	}
	hs := []module.Hint{{Key: "e", Desc: "edit"}, {Key: "E", Desc: "$EDITOR"}, {Key: "v", Desc: "check"}}
	if m.hasDraft {
		hs = append(hs, module.Hint{Key: "d", Desc: "diff"}, module.Hint{Key: "ctrl+s", Desc: "apply"})
	}
	if m.applied != nil {
		hs = append(hs, module.Hint{Key: "u", Desc: "roll back"})
	}
	return append(hs, module.Hint{Key: "x", Desc: "explore"}, module.Hint{Key: "p", Desc: "preview"}, module.Hint{Key: "h", Desc: "history"})
}
