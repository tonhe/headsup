package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/ui/badge"
	"github.com/tonhe/headsup/internal/ui/humanize"
)

// Health polling after a start or restart (R22: up to 30s).
var (
	healthTimeout = 30 * time.Second
	healthEvery   = 500 * time.Millisecond
	opTimeout     = 60 * time.Second
)

// statusMsg is a Status result.
type statusMsg struct {
	st  host.ServiceStatus
	err error
}

func fetchStatus(h host.Host) tea.Cmd {
	if h == nil {
		return nil
	}
	svc := h.Service()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		st, err := svc.Status(ctx)
		return statusMsg{st, err}
	}
}

// RefusedError means a start or restart was refused because the config check
// failed. The service was not touched.
type RefusedError struct {
	Op     string // "restart" or "start"
	Issues []host.Issue
	Err    error // the check could not run at all
}

func (e *RefusedError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s refused: the config check could not run: %v", e.Op, e.Err)
	}
	for _, i := range e.Issues {
		if i.Severity == host.SevError {
			if i.Line > 0 && !strings.Contains(i.Message, "line") {
				return fmt.Sprintf("%s refused: config check failed: line %d: %s", e.Op, i.Line, i.Message)
			}
			return fmt.Sprintf("%s refused: config check failed: %s", e.Op, i.Message)
		}
	}
	return e.Op + " refused: config check failed"
}

func (e *RefusedError) Unwrap() error { return e.Err }

// UnhealthyError means the service did not come back after a start or restart.
type UnhealthyError struct {
	Op    string
	State string
}

func (e *UnhealthyError) Error() string {
	return fmt.Sprintf("headscale is not healthy after %s (state %s); check the logs below", e.Op, e.State)
}

// checkConfig runs headscale's configtest and returns a RefusedError unless it passes.
func checkConfig(ctx context.Context, svc host.ServiceManager, path, op string) error {
	issues, err := svc.Configtest(ctx, path)
	if err != nil {
		return &RefusedError{Op: op, Err: err}
	}
	for _, i := range issues {
		if i.Severity == host.SevError {
			return &RefusedError{Op: op, Issues: issues}
		}
	}
	return nil
}

// waitHealthy polls Status until the service runs, fails, or time runs out.
func waitHealthy(ctx context.Context, svc host.ServiceManager, op string) (host.ServiceStatus, error) {
	deadline := time.Now().Add(healthTimeout)
	for {
		st, err := svc.Status(ctx)
		if err == nil && st.Running {
			return st, nil
		}
		if err == nil && badge.ServicePhaseOf(st.State, st.Running, "") == badge.ServiceFailed {
			return st, &UnhealthyError{Op: op, State: st.State}
		}
		if time.Now().After(deadline) {
			state := st.State
			if err != nil {
				state = err.Error()
			}
			return st, &UnhealthyError{Op: op, State: state}
		}
		select {
		case <-ctx.Done():
			return st, ctx.Err()
		case <-time.After(healthEvery):
		}
	}
}

// --- actions ------------------------------------------------------------------

func (m *Model) serviceTarget(ctx module.Context) []module.Target {
	name := m.hostname(ctx)
	return []module.Target{{Kind: "service", ID: name, Name: name}}
}

func (m *Model) hostname(ctx module.Context) string {
	if h := ctx.Host(); h != nil {
		if n := h.Env().Hostname; n != "" {
			return n
		}
	}
	return "headscale"
}

func (m *Model) configPath(ctx module.Context) string {
	if h := ctx.Host(); h != nil {
		return h.Env().ConfigPath
	}
	return ""
}

func (m *Model) idle(want func(running bool) (bool, string)) func(module.Context) (bool, string) {
	return func(module.Context) (bool, string) {
		if m.busy != "" {
			return false, "headscale is " + m.busy + "; wait for it to finish."
		}
		if !m.haveStatus {
			return true, ""
		}
		return want(m.status.Running)
	}
}

// serviceOp builds the Run for a service action. check runs configtest
// first; health polls until the service is back.
func (m *Model) serviceOp(id, op, busy string, check, health bool, do func(host.ServiceManager, context.Context) error, done string) func(module.Context, module.Values) tea.Cmd {
	return func(ctx module.Context, _ module.Values) tea.Cmd {
		h := ctx.Host()
		if h == nil {
			return func() tea.Msg { return module.ActionResult{ActionID: id, Err: host.ErrNoHost} }
		}
		svc, path := h.Service(), m.configPath(ctx)
		m.busy, m.refused, m.unhealthy = busy, nil, nil
		return func() tea.Msg {
			c, cancel := context.WithTimeout(context.Background(), opTimeout)
			defer cancel()
			if check {
				if err := checkConfig(c, svc, path, op); err != nil {
					return module.ActionResult{ActionID: id, Err: err}
				}
			}
			if err := do(svc, c); err != nil {
				return module.ActionResult{ActionID: id, Err: err}
			}
			msg := done
			if health {
				st, err := waitHealthy(c, svc, op)
				if err != nil {
					return module.ActionResult{ActionID: id, Err: err}
				}
				if st.PID > 0 {
					msg += fmt.Sprintf(" (PID %d)", st.PID)
				}
			}
			return module.ActionResult{ActionID: id, Message: msg + "."}
		}
	}
}

func (m *Model) serviceActions() []module.Action {
	whenStopped := m.idle(func(running bool) (bool, string) {
		if running {
			return false, "headscale is already running."
		}
		return true, ""
	})
	whenRunning := m.idle(func(running bool) (bool, string) {
		if !running {
			return false, "headscale is not running."
		}
		return true, ""
	})
	return []module.Action{
		{
			ID: "service.start", Title: "Start headscale", Keys: []string{"S"},
			Mutating: true, Danger: module.DangerConfirm, Needs: host.NeedService,
			Consequence: "headscale's config is checked first; the start is refused if the check fails.",
			Enabled:     whenStopped, Targets: m.serviceTarget,
			Run: m.serviceOp("service.start", "start", "starting", true, true,
				func(s host.ServiceManager, c context.Context) error { return s.Start(c) }, "headscale started"),
		},
		{
			ID: "service.stop", Title: "Stop headscale", Keys: []string{"x"},
			Mutating: true, Danger: module.DangerCritical, Needs: host.NeedService,
			Consequence: "Every client loses the control plane: no new connections, key renewals or policy updates until headscale is started again.",
			Enabled:     whenRunning, Targets: m.serviceTarget,
			Run: m.serviceOp("service.stop", "stop", "stopping", false, false,
				func(s host.ServiceManager, c context.Context) error { return s.Stop(c) }, "headscale stopped"),
		},
		{
			ID: "service.restart", Title: "Restart headscale", Keys: []string{"X"},
			Mutating: true, Danger: module.DangerConfirm, Needs: host.NeedService,
			Consequence: "Connected clients will reconnect. The config is checked first; the restart is refused if the check fails.",
			Enabled:     m.idle(func(bool) (bool, string) { return true, "" }), Targets: m.serviceTarget,
			Run: m.serviceOp("service.restart", "restart", "restarting", true, true,
				func(s host.ServiceManager, c context.Context) error { return s.Restart(c) }, "headscale restarted"),
		},
		{
			ID: "service.reload", Title: "Reload policy", Keys: []string{"H"},
			Mutating: true, Danger: module.DangerConfirm, Needs: host.NeedService,
			Consequence: "Sends SIGHUP: reloads the policy only. Config changes need a restart. Clients stay connected.",
			Enabled:     whenRunning, Targets: m.serviceTarget,
			Run: m.serviceOp("service.reload", "reload", "reloading", false, false,
				func(s host.ServiceManager, c context.Context) error { return s.Reload(c) }, "Policy reloaded"),
		},
	}
}

// --- view -----------------------------------------------------------------------

func memory(b uint64) string {
	switch {
	case b == 0:
		return "-"
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(b)/(1<<20))
	}
	return fmt.Sprintf("%d KiB", b>>10)
}

// statusLines is the body of the status pane.
func (m *Model) statusLines(ctx module.Context) []string {
	s, ic := ctx.Styles(), ctx.Icons()
	label := func(k string) string { return s.Label.Render(k) + " " }
	var l1 string
	switch {
	case !m.haveStatus && m.statusErr == nil:
		l1 = s.Muted.Render("Checking service state" + ic.Ellipsis)
	case m.statusErr != nil && !m.haveStatus:
		l1 = s.Danger.Render("Service state unavailable: " + m.statusErr.Error())
	default:
		st := m.status
		word := st.State
		if m.busy != "" {
			word = m.busy + ic.Ellipsis
		}
		l1 = badge.Service(s, ic, m.phase(), word)
		if st.Running && !st.Since.IsZero() {
			l1 += "  " + label("since") + st.Since.In(ctx.Now().Location()).Format("2006-01-02 15:04") +
				s.Muted.Render(" ("+humanize.Ago(st.Since, ctx.Now())+")")
		}
		if st.PID > 0 {
			l1 += "  " + label("PID") + fmt.Sprint(st.PID)
		}
		if st.Memory > 0 {
			l1 += "  " + label("memory") + memory(st.Memory)
		}
	}
	env := host.Env{}
	if h := ctx.Host(); h != nil {
		env = h.Env()
	}
	ver := m.status.Version
	if ver == "" {
		ver = env.Version
	}
	if ver == "" {
		ver = "-"
	}
	l2 := label("version") + ver + "  " + label("manager") + env.Service.String() + "  " + label("logs") + env.LogSource.String()
	if env.Hostname != "" {
		l2 += "  " + label("host") + env.Hostname
	}
	lines := []string{l1, l2}
	switch {
	case m.refused != nil:
		lines = append(lines, s.Danger.Render(ic.Cross+" "+m.refused.Error()+". The service was not touched."))
		for _, i := range m.refused.Issues {
			if i.Severity != host.SevError {
				continue
			}
			loc := ""
			if i.Line > 0 {
				loc = fmt.Sprintf("line %d: ", i.Line)
			}
			lines = append(lines, "  "+s.Muted.Render(ic.Bullet)+" "+loc+i.Message)
		}
	case m.unhealthy != nil:
		lines = append(lines, s.Danger.Render(ic.Warn+" "+m.unhealthy.Error()+"."))
	case m.lastOK != "":
		lines = append(lines, s.OK.Render(ic.Check+" ")+s.Muted.Render(m.lastOK))
	}
	return lines
}

func (m *Model) phase() badge.ServicePhase {
	if !m.haveStatus && m.busy == "" {
		return badge.ServiceUnknown
	}
	return badge.ServicePhaseOf(m.status.State, m.status.Running, m.busy)
}

// onResult updates the panel after one of this module's actions finished.
func (m *Model) onResult(ctx module.Context, r module.ActionResult) tea.Cmd {
	switch r.ActionID {
	case "service.start", "service.stop", "service.restart", "service.reload":
	default:
		return nil
	}
	m.busy, m.refused, m.unhealthy, m.lastOK = "", nil, nil, ""
	var re *RefusedError
	var ue *UnhealthyError
	switch {
	case errors.As(r.Err, &re):
		m.refused = re
	case errors.As(r.Err, &ue):
		m.unhealthy = ue
		// Show what went wrong: jump to the most recent error line.
		m.logs.SetFollow(true)
		m.logs.NextError(-1)
	case r.Err == nil:
		m.lastOK = strings.TrimSuffix(r.Message, ".") + " at " + ctx.Now().Format("15:04:05")
		if r.ActionID == "service.restart" || r.ActionID == "service.start" {
			m.lastOK = "Config check passed. " + m.lastOK
		}
	}
	return fetchStatus(ctx.Host())
}
