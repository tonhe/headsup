package policy

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tonhe/headsup/internal/api"
	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/module"
	pol "github.com/tonhe/headsup/internal/policy"
)

// The apply flow (T108, T111):
//
//  1. Validate: the server's CheckPolicy, or a local parse when the check is
//     unavailable; in host file mode, the managed-file Plan, which runs
//     `headscale policy check` on a temporary copy.
//  2. On failure, show the error with its line and column, keeping the buffer.
//  3. Show the diff against the live policy.
//  4. Conflict check: warn when the live policy changed since the edit began
//     (updatedAt, or the file hash), and re-check right before writing.
//  5. Confirm (the app's confirm gate; ctrl+s in the review).
//  6. SetPolicy, or in file mode: back up, atomic write, reload, and confirm
//     the policy is active, offering a one-key rollback when it is not.
//  7. Record the pre-apply and applied revisions in history.

type purpose int

const (
	purposeApply purpose = iota // validate, then review the diff
	purposeCheck                // validate only (v)
)

type review struct {
	lines    []pol.Line
	against  string // the live policy the diff is against
	at       time.Time
	conflict string
	local    bool
	tests    int
	warnings []string
	change   *host.Change // file mode
}

type checkedMsg struct {
	draft    string
	purpose  purpose
	prob     *problem
	err      error // not a validation failure (unreachable, permission, ...)
	local    bool
	live     string
	liveAt   time.Time
	liveFile *host.File
	liveErr  error
	change   *host.Change
	warnings []string
}

// text is what a check or apply works on: the draft, else the live policy.
func (m *Model) text() string {
	if m.hasDraft {
		return m.draft
	}
	return m.cur
}

func localProblem(text string) *problem {
	var pe *pol.ParseError
	if err := pol.Check([]byte(text)); errors.As(err, &pe) {
		return &problem{line: pe.Line, col: pe.Col, msg: pe.Msg, source: "local"}
	}
	return nil
}

func (m *Model) startCheck(ctx module.Context, p purpose) tea.Cmd {
	if m.checking {
		return nil
	}
	m.checking = true
	text := m.text()
	b, h, fm := ctx.Backend(), ctx.Host(), fileMode(ctx)
	base := m.baseFile
	if base == nil {
		base = m.file
	}
	return func() tea.Msg {
		r := checkedMsg{draft: text, purpose: p}
		if fm && base != nil {
			checkFile(h, base, &r)
			return r
		}
		checkAPI(b, &r)
		return r
	}
}

func checkFile(h host.Host, base *host.File, r *checkedMsg) {
	var c *host.Change
	err := call(func(ctx context.Context) (err error) { c, err = h.Plan(ctx, base, []byte(r.draft)); return })
	if err != nil {
		r.err = err
		return
	}
	r.change = c
	for _, is := range c.Issues {
		switch {
		case is.Severity == host.SevError && r.prob == nil:
			r.prob = &problem{line: is.Line, col: is.Col, msg: is.Message, source: is.Source}
		case is.Severity == host.SevWarn:
			r.warnings = append(r.warnings, is.Message)
		}
	}
	if r.prob != nil && r.prob.line == 0 {
		if lp := localProblem(r.draft); lp != nil {
			r.prob.line, r.prob.col = lp.line, lp.col
		}
	}
	if cur, err := h.Open(host.FilePolicy, base.Path); err == nil {
		r.live, r.liveFile = string(cur.Raw), cur
	} else {
		r.live, r.liveErr = string(base.Raw), err
	}
}

func checkAPI(b api.Backend, r *checkedMsg) {
	var err error
	if b.Capabilities().Has(api.CapPolicyCheck) {
		err = call(func(c context.Context) error { return b.CheckPolicy(c, r.draft) })
		if api.IsKind(err, api.KindUnsupported) {
			err, r.local = nil, true
		}
	} else {
		r.local = true
	}
	switch {
	case r.local:
		r.prob = localProblem(r.draft)
	case api.IsKind(err, api.KindInvalid):
		ae := api.AsError(err)
		r.prob = &problem{line: ae.Line, col: ae.Col, msg: ae.Message, source: "server"}
		if r.prob.line == 0 {
			if lp := localProblem(r.draft); lp != nil {
				r.prob.line, r.prob.col = lp.line, lp.col
			}
		}
	case err != nil:
		r.err = err
		return
	}
	var p api.Policy
	r.liveErr = call(func(c context.Context) (err error) { p, err = b.GetPolicy(c); return })
	r.live, r.liveAt = p.Raw, p.UpdatedAt
}

func (m *Model) onChecked(ctx module.Context, msg checkedMsg) tea.Cmd {
	m.checking = false
	if msg.err != nil {
		id := "policy.check"
		if msg.purpose == purposeApply {
			id = "policy.apply"
		}
		return result(module.ActionResult{ActionID: id, Err: msg.err})
	}
	if msg.prob != nil {
		m.invalid = msg.prob
		m.back, m.mode, m.review = m.mode, modeInvalid, nil
		m.offset = max(0, msg.prob.line-6)
		text := "The policy is invalid: " + msg.prob.String()
		if msg.purpose == purposeCheck {
			return result(module.ActionResult{ActionID: "policy.check", Err: api.Errf(api.KindInvalid, "%s", msg.prob.String())})
		}
		return ctx.Toast(module.ToastError, text)
	}
	m.invalid = nil
	if msg.purpose == purposeCheck {
		how := "by the server"
		if msg.local {
			how = "locally; the server cannot check policies"
		}
		if msg.change != nil {
			how = "with headscale policy check"
		}
		return result(module.ActionResult{ActionID: "policy.check", Message: "The policy is valid (checked " + how + ")."})
	}
	live := msg.live
	if msg.liveErr != nil || (live == "" && msg.change == nil) {
		live = m.cur
	}
	lines := pol.Diff(live, msg.draft)
	if a, d := pol.Changed(lines); a+d == 0 {
		m.hasDraft, m.draft, m.mode = false, "", modeView
		return ctx.Toast(module.ToastInfo, "No changes to apply: the draft matches the live policy.")
	}
	rv := &review{lines: lines, against: live, at: msg.liveAt, local: msg.local, warnings: msg.warnings, change: msg.change}
	if d, err := pol.Parse([]byte(msg.draft)); err == nil {
		rv.tests = len(d.Tests)
	}
	rv.conflict = m.conflict(msg)
	m.review, m.mode, m.offset = rv, modeReview, 0
	return nil
}

// conflict explains a change to the live policy since the edit began.
func (m *Model) conflict(msg checkedMsg) string {
	if !m.hasDraft {
		return ""
	}
	if msg.liveFile != nil && m.baseFile != nil {
		if msg.liveFile.SHA256 != m.baseFile.SHA256 {
			return "The policy file changed on disk since you started editing. Applying replaces those changes; the diff below is against the file as it is now."
		}
		return ""
	}
	if msg.liveErr != nil {
		return ""
	}
	changed := msg.live != m.baseRaw
	if !msg.liveAt.IsZero() && !m.baseAt.IsZero() {
		changed = changed || !msg.liveAt.Equal(m.baseAt)
	}
	if changed {
		return "Someone changed the server's policy since you started editing. Applying overwrites their change; the diff below is against the server's current policy."
	}
	return ""
}

// posInMsg matches a position already embedded in a checker's message.
var posInMsg = regexp.MustCompile(`(?i)(hujson: )?line \d+,? col(umn)? \d+:\s*`)

func (p *problem) String() string {
	if p.line > 0 {
		return fmt.Sprintf("line %d, column %d: %s", p.line, max(1, p.col), posInMsg.ReplaceAllString(p.msg, ""))
	}
	return p.msg
}

func result(r module.ActionResult) tea.Cmd { return func() tea.Msg { return r } }

// --- apply via the API -------------------------------------------------------

type appliedMsg struct {
	policy api.Policy
	draft  string
	err    error
}

func (m *Model) applyAPI(ctx module.Context) tea.Cmd {
	rv, draft := m.review, m.draft
	b, hist, prof, now := ctx.Backend(), m.hist, profileKey(ctx), ctx.Now()
	return func() tea.Msg {
		var live api.Policy
		if err := call(func(c context.Context) (err error) { live, err = b.GetPolicy(c); return }); err == nil {
			if live.Raw != rv.against || (!rv.at.IsZero() && !live.UpdatedAt.IsZero() && !live.UpdatedAt.Equal(rv.at)) {
				return appliedMsg{draft: draft, err: api.Errf(api.KindConflict,
					"the server's policy changed while you were reviewing; nothing was applied. Review the new diff")}
			}
		}
		if hist != nil {
			_, _, _ = hist.Record(prof, rv.against, pol.SourcePreApply, rv.at, now)
		}
		var p api.Policy
		err := call(func(c context.Context) (err error) { p, err = b.SetPolicy(c, draft); return })
		if err == nil && hist != nil {
			_, _, _ = hist.Record(prof, draft, pol.SourceApplied, p.UpdatedAt, now)
		}
		if p.Raw == "" {
			p.Raw = draft
		}
		return appliedMsg{policy: p, draft: draft, err: err}
	}
}

func (m *Model) onApplied(ctx module.Context, msg appliedMsg) tea.Cmd {
	res := module.ActionResult{ActionID: "policy.apply", Err: msg.err}
	if msg.err != nil {
		ae := api.AsError(msg.err)
		switch ae.Kind {
		case api.KindUnsupported:
			m.viewOnly = fileModeRemote
			m.mode, m.review = modeView, nil
		case api.KindInvalid:
			m.invalid = &problem{line: ae.Line, col: ae.Col, msg: ae.Message, source: "server"}
			m.mode, m.review, m.offset = modeInvalid, nil, max(0, ae.Line-6)
		case api.KindConflict:
			m.mode, m.review = modeView, nil
			return tea.Batch(result(res), m.load(ctx), m.startCheck(ctx, purposeApply))
		default:
			m.mode = modeReview
		}
		return result(res)
	}
	m.setCurrent(msg.policy.Raw, msg.policy.UpdatedAt, m.file)
	m.clearDraft()
	res.Message = "Policy applied."
	return result(res)
}

func (m *Model) clearDraft() {
	m.hasDraft, m.draft, m.baseRaw, m.baseAt, m.baseFile = false, "", "", time.Time{}, nil
	m.review, m.invalid, m.mode, m.offset = nil, nil, modeView, 0
}

// --- apply in host file mode ------------------------------------------------

type activeState int

const (
	activeUnknown activeState = iota
	activeYes
	activeNo
)

type fileAppliedMsg struct {
	next      string
	change    *host.Change
	applied   *host.Applied
	err       error
	reloadErr error
	active    activeState
}

func (m *Model) applyFile(ctx module.Context) tea.Cmd {
	rv, next := m.review, m.draft
	b, h, hist, prof, now := ctx.Backend(), ctx.Host(), m.hist, profileKey(ctx), ctx.Now()
	return func() tea.Msg {
		r := fileAppliedMsg{next: next, change: rv.change}
		var a host.Applied
		r.err = call(func(c context.Context) (err error) { a, err = h.Apply(c, rv.change); return })
		if r.err != nil {
			return r
		}
		r.applied = &a
		if hist != nil {
			_, _, _ = hist.Record(prof, rv.against, pol.SourcePreApply, time.Time{}, now)
			_, _, _ = hist.Record(prof, next, pol.SourceApplied, time.Time{}, now)
		}
		if r.reloadErr = reload(h); r.reloadErr != nil {
			return r
		}
		r.active = confirmActive(b, rv.against, next)
		return r
	}
}

func reload(h host.Host) error {
	svc := h.Service()
	if svc == nil || svc.Kind() == host.ServiceNone {
		return errors.New("no service manager was detected, so headscale was not told to reload (send it SIGHUP)")
	}
	return call(func(c context.Context) error { return svc.Reload(c) })
}

// confirmActive asks headscale for its policy after the reload.
func confirmActive(b api.Backend, old, next string) activeState {
	var p api.Policy
	if err := call(func(c context.Context) (err error) { p, err = b.GetPolicy(c); return }); err != nil {
		return activeUnknown
	}
	switch {
	case pol.Equivalent(p.Raw, next):
		return activeYes
	case pol.Equivalent(p.Raw, old):
		return activeNo
	}
	return activeUnknown
}

func (m *Model) onFileApplied(ctx module.Context, msg fileAppliedMsg) tea.Cmd {
	res := module.ActionResult{ActionID: "policy.apply"}
	if msg.err != nil {
		res.Err = msg.err
		var cd *host.ErrChangedOnDisk
		var inv *host.ErrInvalid
		switch {
		case errors.As(msg.err, &cd):
			res.Err = fmt.Errorf("%s changed on disk since it was opened; nothing was written. Press ctrl+s to review against the new version", cd.Path)
			m.baseFile, m.review, m.mode = nil, nil, modeView
			return tea.Batch(result(res), m.load(ctx))
		case errors.As(msg.err, &inv):
			for _, is := range inv.Issues {
				if is.Severity == host.SevError {
					m.invalid = &problem{line: is.Line, col: is.Col, msg: is.Message, source: is.Source}
					break
				}
			}
			m.mode, m.review = modeInvalid, nil
		}
		return result(res)
	}
	res.Files = []module.FileWrite{module.WriteOf(msg.change, *msg.applied)}
	m.clearDraft()
	m.cur = msg.next
	m.doc, m.docErr = pol.Parse([]byte(msg.next))
	switch {
	case msg.reloadErr != nil:
		m.applied, m.reloadErr = msg.applied, "the reload failed: "+msg.reloadErr.Error()
	case msg.active == activeNo:
		m.applied, m.reloadErr = msg.applied, "headscale still reports the previous policy after the reload"
	default:
		m.applied, m.reloadErr = nil, ""
	}
	switch {
	case m.applied != nil:
		res.Err = fmt.Errorf("the policy file was written, but %s. Press u to roll back", m.reloadErr)
	case msg.active == activeYes:
		res.Message = "Policy written and reloaded; headscale reports it active."
	default:
		res.Message = "Policy written and headscale reloaded (could not confirm the new policy is active)."
	}
	return tea.Batch(result(res), m.load(ctx))
}

// --- rollback of a file-mode apply ------------------------------------------

type rolledBackMsg struct {
	restoreErr error
	reloadErr  error
}

func (m *Model) rollbackFile(ctx module.Context) tea.Cmd {
	a, h := *m.applied, ctx.Host()
	return func() tea.Msg {
		var r rolledBackMsg
		if r.restoreErr = call(func(c context.Context) error { return h.Rollback(c, a) }); r.restoreErr == nil {
			r.reloadErr = reload(h)
		}
		return r
	}
}

func (m *Model) onRolledBack(ctx module.Context, msg rolledBackMsg) tea.Cmd {
	res := module.ActionResult{ActionID: "policy.rollback"}
	switch {
	case msg.restoreErr != nil:
		res.Err = msg.restoreErr
		return result(res)
	case msg.reloadErr != nil:
		res.Err = fmt.Errorf("restored the previous policy file, but the reload failed: %w", msg.reloadErr)
	default:
		res.Message = "Restored the previous policy file and reloaded headscale."
	}
	m.applied, m.reloadErr = nil, ""
	return tea.Batch(result(res), m.load(ctx))
}
