package app

import (
	"fmt"

	"github.com/tonhe/headsup/internal/api"
	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/module"
)

// GateEnv is what the gate needs to decide whether an action may proceed.
type GateEnv struct {
	ReadOnly bool
	Caps     api.Capabilities
	HostMode bool
	Perms    host.Perms
}

// Verdict is the gate's decision for one action.
type Verdict struct {
	Allowed    bool
	Reason     string // shown to the user when not allowed
	FixCommand string // for missing host permissions
}

// Gate applies the fixed policy every action passes through before any form
// or confirmation: enabled, read-only, server capability, host permission.
// Modules cannot bypass it because the app, not the module, calls it.
func Gate(a module.Action, ctx module.Context, env GateEnv) Verdict {
	if a.Mutating && env.ReadOnly {
		return Verdict{Reason: "This profile is read-only."}
	}
	if a.Cap != api.CapNone && !env.Caps.Has(a.Cap) {
		return Verdict{Reason: fmt.Sprintf("Not supported by this headscale server (%s).", a.Cap)}
	}
	if a.Needs != host.NeedNone {
		if !env.HostMode {
			return Verdict{Reason: "Available when run on the headscale server (host mode)."}
		}
		if p, missing := env.Perms.Missing(a.Needs); missing {
			return Verdict{Reason: p.Reason, FixCommand: p.FixCommand}
		}
	}
	if a.Enabled != nil {
		if ok, why := a.Enabled(ctx); !ok {
			if why == "" {
				why = "Not available right now."
			}
			return Verdict{Reason: why}
		}
	}
	return Verdict{Allowed: true}
}

// NeedsConfirm reports how an action must be confirmed before running. A
// mutating action with no parameter form is at least DangerConfirm, so a
// module that forgets to set Danger still cannot mutate on a single key
// press; submitting a form counts as the explicit step for DangerNone.
func NeedsConfirm(a module.Action) module.Danger {
	if a.Mutating && a.Danger == module.DangerNone && a.Params == nil {
		return module.DangerConfirm
	}
	return a.Danger
}
