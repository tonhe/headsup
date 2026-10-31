package policy

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tonhe/headsup/internal/api"
	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/module"
	pol "github.com/tonhe/headsup/internal/policy"
)

var errorf = fmt.Errorf

func (m *Model) loadedGate(module.Context) (bool, string) {
	if !m.loaded {
		if m.loadErr != nil {
			return false, "The policy could not be loaded: " + m.loadErr.Error()
		}
		return false, "The policy has not loaded yet."
	}
	return true, ""
}

// canEdit explains why editing is unavailable: read-only, a file-mode policy
// seen remotely, or a policy file this user cannot write.
func (m *Model) canEdit(ctx module.Context) (bool, string) {
	if ok, why := m.loadedGate(ctx); !ok {
		return false, why
	}
	if ctx.ReadOnly() {
		return false, "This profile is read-only."
	}
	if m.checking {
		return false, "Still checking the last edit."
	}
	if fileMode(ctx) {
		if m.file == nil {
			why := "the policy file could not be opened"
			if m.fileErr != nil {
				why = m.fileErr.Error()
			}
			return false, "Editing is unavailable: " + why + "."
		}
		if p, missing := ctx.Host().Env().Perms.Missing(host.NeedPolicyW); missing {
			why := "Cannot write the policy file: " + p.Reason + "."
			if p.FixCommand != "" {
				why += " Fix: " + p.FixCommand
			}
			return false, why
		}
		return true, ""
	}
	if m.viewOnly != "" || !ctx.Backend().Capabilities().Has(api.CapPolicyWritable) {
		return false, fileModeRemote
	}
	return true, ""
}

func (m *Model) Actions() []module.Action {
	hasDraft := func(module.Context) (bool, string) {
		if !m.hasDraft {
			return false, "No unsaved edits. Press e to edit the policy."
		}
		return true, ""
	}
	acts := []module.Action{
		{
			ID: "policy.edit", Title: "Edit policy", Keys: []string{"e"}, Enabled: m.canEdit,
			Run: func(ctx module.Context, _ module.Values) tea.Cmd { return m.startInline(ctx) },
		},
		{
			ID: "policy.editor", Title: "Edit policy in $EDITOR", Keys: []string{"E"}, Enabled: m.canEdit,
			Run: func(ctx module.Context, _ module.Values) tea.Cmd { return m.startExternal(ctx) },
		},
		{
			ID: "policy.check", Title: "Check policy", Keys: []string{"v"},
			Enabled: func(ctx module.Context) (bool, string) {
				if m.checking {
					return false, "A check is already running."
				}
				return m.loadedGate(ctx)
			},
			Run: func(ctx module.Context, _ module.Values) tea.Cmd { return m.startCheck(ctx, purposeCheck) },
		},
		{
			ID: "policy.diff", Title: "Diff unsaved edits", Keys: []string{"d"}, Enabled: hasDraft,
			Run: func(module.Context, module.Values) tea.Cmd {
				m.back, m.mode, m.offset = m.mode, modeDiff, 0
				return nil
			},
		},
		m.applyAction(),
		{
			ID: "policy.discard", Title: "Discard unsaved edits", Keys: []string{"D"}, Hidden: true,
			Danger: module.DangerConfirm, Enabled: hasDraft,
			Consequence: "Your unsaved policy edits are lost. The live policy is not changed.",
			Targets: func(module.Context) []module.Target {
				return []module.Target{{Kind: "policy", ID: "draft", Name: "unsaved edits"}}
			},
			Run: func(module.Context, module.Values) tea.Cmd {
				m.clearDraft()
				return result(module.ActionResult{ActionID: "policy.discard", Message: "Discarded the unsaved edits."})
			},
		},
		{
			ID: "policy.history", Title: "Policy history", Keys: []string{"h"},
			Run: func(ctx module.Context, _ module.Values) tea.Cmd { return m.openHistory(ctx) },
		},
		{
			ID: "policy.explore", Title: "Explore policy", Keys: []string{"x"},
			Enabled: func(ctx module.Context) (bool, string) {
				if ok, why := m.loadedGate(ctx); !ok {
					return false, why
				}
				if m.doc == nil {
					return false, "The live policy does not parse, so it cannot be explored."
				}
				return true, ""
			},
			Run: func(module.Context, module.Values) tea.Cmd { m.openExplore(); return nil },
		},
		{
			ID: "policy.filter", Title: "Compiled packet filter", Keys: []string{"f"}, Needs: host.NeedDebug,
			Run: func(ctx module.Context, _ module.Values) tea.Cmd { return m.openFilter(ctx) },
		},
		m.rollbackAction(),
	}
	return acts
}

func (m *Model) policyTarget(ctx module.Context) []module.Target {
	if fileMode(ctx) && m.file != nil {
		return []module.Target{{Kind: "file", ID: m.file.Path, Name: m.file.Path}}
	}
	return []module.Target{{Kind: "policy", ID: "policy", Name: "tailnet"}}
}

// applyAction is ctrl+s. Outside a review it validates the draft and opens
// the review; inside a review it is the mutating, confirmed apply.
func (m *Model) applyAction() module.Action {
	a := module.Action{ID: "policy.apply", Title: "Check and apply policy", Keys: []string{"ctrl+s"}, Targets: m.policyTarget}
	if m.mode != modeReview || m.review == nil {
		a.Enabled = func(ctx module.Context) (bool, string) {
			if ok, why := m.canEdit(ctx); !ok {
				return false, why
			}
			if !m.hasDraft {
				return false, "No unsaved edits to apply. Press e to edit the policy."
			}
			return true, ""
		}
		a.Run = func(ctx module.Context, _ module.Values) tea.Cmd { return m.startCheck(ctx, purposeApply) }
		return a
	}
	rv := m.review
	added, removed := pol.Changed(rv.lines)
	a.Title, a.Mutating, a.Danger = "Apply the reviewed policy", true, module.DangerConfirm
	a.Enabled = m.canEdit
	a.Consequence = fmt.Sprintf("Access rules change for every machine in the tailnet (+%d −%d lines).", added, removed)
	if rv.conflict != "" {
		a.Consequence += " This overwrites a change made since you started editing."
	}
	if rv.change != nil {
		a.Needs = host.NeedPolicyW
		a.Consequence += " The file is backed up, written atomically, and headscale is reloaded."
		a.Run = func(ctx module.Context, _ module.Values) tea.Cmd { return m.applyFile(ctx) }
		return a
	}
	a.Run = func(ctx module.Context, _ module.Values) tea.Cmd { return m.applyAPI(ctx) }
	return a
}

// rollbackAction is u: undo a file-mode apply whose reload failed, or, in the
// history view, restore the selected revision (through check, diff, confirm).
func (m *Model) rollbackAction() module.Action {
	a := module.Action{ID: "policy.rollback", Title: "Roll back policy", Keys: []string{"u"}}
	switch {
	case m.applied != nil:
		a.Title, a.Mutating, a.Danger, a.Needs = "Roll back the policy file", true, module.DangerConfirm, host.NeedPolicyW
		a.Consequence = "Restores the policy file from the backup taken before the last apply, then reloads headscale."
		a.Targets = func(module.Context) []module.Target {
			p := m.applied.File.Path
			return []module.Target{{Kind: "file", ID: p, Name: p}}
		}
		a.Run = func(ctx module.Context, _ module.Values) tea.Cmd { return m.rollbackFile(ctx) }
	case m.mode == modeHistory:
		a.Title = "Restore this revision"
		a.Enabled = func(ctx module.Context) (bool, string) {
			if ok, why := m.canEdit(ctx); !ok {
				return false, why
			}
			if m.hasDraft {
				return false, "You have unsaved edits. Apply or discard them (D) first."
			}
			if _, ok := m.hst.selected(); !ok {
				return false, "No revision selected."
			}
			return true, ""
		}
		a.Run = func(ctx module.Context, _ module.Values) tea.Cmd { return m.restore(ctx) }
	default:
		a.Enabled = func(module.Context) (bool, string) {
			return false, "Nothing to roll back. Press h to restore an earlier version from history."
		}
	}
	return a
}
