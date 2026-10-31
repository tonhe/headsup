package badge

import (
	"strings"

	"github.com/tonhe/headsup/internal/icons"
	"github.com/tonhe/headsup/internal/ui/styles"
)

// ServicePhase is the coarse state of the headscale service, for badges.
type ServicePhase int

const (
	ServiceUnknown    ServicePhase = iota
	ServiceRunning                 // ● running
	ServiceStopped                 // ○ stopped
	ServiceTransition              // ◐ starting, stopping, restarting, reloading
	ServiceFailed                  // ○ failed (danger colour)
)

// ServicePhaseOf maps a service manager state ("active", "inactive",
// "failed", "activating", "running", "exited", ...) to a phase. busy is a
// transition the app itself started ("restarting"); it wins when set.
func ServicePhaseOf(state string, running bool, busy string) ServicePhase {
	if busy != "" {
		return ServiceTransition
	}
	switch strings.ToLower(state) {
	case "activating", "deactivating", "reloading", "restarting", "starting", "stopping", "created":
		return ServiceTransition
	case "failed", "dead", "crashed":
		return ServiceFailed
	case "", "unknown":
		return ServiceUnknown
	}
	if running {
		return ServiceRunning
	}
	return ServiceStopped
}

// String is the phase's plain word.
func (p ServicePhase) String() string {
	return [...]string{"unknown", "running", "stopped", "restarting", "failed"}[p]
}

// ServiceGlyph is the unstyled glyph for a phase: ● running, ○ stopped,
// ◐ in transition (per icon set). Failed shares the stopped glyph; Service
// colours it as danger.
func ServiceGlyph(ic icons.Set, p ServicePhase) string {
	switch p {
	case ServiceRunning:
		return ic.Online
	case ServiceTransition:
		return ic.Idle
	case ServiceStopped, ServiceFailed:
		return ic.Offline
	}
	return ic.Pending
}

// Service renders the glyph in the phase's colour, optionally followed by label.
func Service(s *styles.Styles, ic icons.Set, p ServicePhase, label string) string {
	st := s.Muted
	switch p {
	case ServiceRunning:
		st = s.Online
	case ServiceTransition:
		st = s.Idle
	case ServiceFailed:
		st = s.Danger
	case ServiceStopped:
		st = s.Offline
	}
	out := ServiceGlyph(ic, p)
	if label != "" {
		out += " " + label
	}
	return st.Render(out)
}
