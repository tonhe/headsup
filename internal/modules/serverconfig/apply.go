package serverconfig

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/host/files"
	"github.com/tonhe/headsup/internal/host/hsconfig"
	"github.com/tonhe/headsup/internal/icons"
	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/ui/styles"
	"github.com/tonhe/headsup/internal/ui/table"
)

// RestartConsequence is what every restart offer says it interrupts.
const RestartConsequence = "headscale restarts: every connected client loses the control plane for a few seconds and reconnects. Existing tunnels keep working."

// opTimeout bounds one host operation (plan with configtest, write, restart).
const opTimeout = 60 * time.Second

// LogTail is how many log lines an unhealthy restart shows.
const LogTail = 15

// --- messages ---------------------------------------------------------------

type loadedMsg struct {
	file *host.File
	err  error
}

type plannedMsg struct {
	change *host.Change
	err    error
}

type writtenMsg struct {
	applied host.Applied
	file    *host.File // reopened after the write
	change  *host.Change
	err     error
}

// healthReport is the outcome of a restart and its health check.
type healthReport struct {
	healthy bool
	status  host.ServiceStatus
	logs    []string
	logErr  error
}

type restartedMsg struct {
	action string // action ID that asked for the restart
	report healthReport
	file   *host.File // reopened after a rollback
	err    error
}

// --- commands ---------------------------------------------------------------

func loadCmd(h host.Host) tea.Cmd {
	return func() tea.Msg {
		f, err := h.Open(host.FileConfig, "")
		return loadedMsg{file: f, err: err}
	}
}

// planCmd validates next (schema plus headscale's configtest on a temporary
// copy, done by the host) and diffs it. Nothing is written.
func planCmd(h host.Host, f *host.File, next []byte) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
		defer cancel()
		c, err := h.Plan(ctx, f, next)
		return plannedMsg{change: c, err: err}
	}
}

// writeCmd applies a validated change: the host re-checks the hash, backs
// the file up, and writes atomically keeping owner and mode.
func writeCmd(h host.Host, c *host.Change) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
		defer cancel()
		a, err := h.Apply(ctx, c)
		if err != nil {
			return writtenMsg{change: c, err: err}
		}
		f, oerr := h.Open(c.File.Kind, c.File.Path)
		if oerr != nil {
			// The write succeeded; describe the new content ourselves.
			nf := *c.File
			nf.Raw, nf.SHA256 = append([]byte(nil), c.Next...), a.NewSHA256
			f = &nf
		}
		return writtenMsg{applied: a, file: f, change: c}
	}
}

// restartCmd validates the live config with headscale's checker, restarts
// the service, and checks it came back. A restart is never attempted after
// a failed check; prevalidated lets a just-planned write stand in when the
// checker cannot run at all.
func restartCmd(h host.Host, path, action string, prevalidated bool, settle time.Duration) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
		defer cancel()
		rep, err := restartChecked(ctx, h, path, prevalidated, settle)
		return restartedMsg{action: action, report: rep, err: err}
	}
}

// rollbackCmd restores the backup taken before a, then restarts when the
// service had already been restarted onto the bad config.
func rollbackCmd(h host.Host, a host.Applied, restart bool, settle time.Duration) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
		defer cancel()
		const id = "serverconfig.rollback"
		if err := h.Rollback(ctx, a); err != nil {
			return restartedMsg{action: id, err: err}
		}
		f, _ := h.Open(a.File.Kind, a.File.Path)
		if !restart {
			return restartedMsg{action: id, file: f, report: healthReport{healthy: true}}
		}
		rep, err := restartChecked(ctx, h, a.File.Path, false, settle)
		return restartedMsg{action: id, file: f, report: rep, err: err}
	}
}

func restartChecked(ctx context.Context, h host.Host, path string, prevalidated bool, settle time.Duration) (healthReport, error) {
	svc := h.Service()
	issues, err := svc.Configtest(ctx, path)
	switch {
	case err != nil && !prevalidated:
		return healthReport{}, fmt.Errorf("headscale's config check could not run (%v); not restarting", err)
	case hasErrors(issues):
		return healthReport{}, fmt.Errorf("config check failed, not restarting: %s", firstError(issues))
	}
	if err := svc.Restart(ctx); err != nil {
		return healthReport{}, err
	}
	return checkHealth(ctx, h, settle), nil
}

// checkHealth waits settle, then requires the service to be running on two
// consecutive looks, so a crash right after start is caught.
func checkHealth(ctx context.Context, h host.Host, settle time.Duration) healthReport {
	var rep healthReport
	for i := 0; i < 2; i++ {
		if settle > 0 {
			select {
			case <-time.After(settle):
			case <-ctx.Done():
			}
		}
		st, err := h.Service().Status(ctx)
		rep.status = st
		if err != nil || !st.Running {
			rep.logs, rep.logErr = tailLogs(ctx, h, LogTail)
			return rep
		}
	}
	rep.healthy = true
	return rep
}

func tailLogs(ctx context.Context, h host.Host, n int) ([]string, error) {
	lctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	ch, err := h.Logs(lctx, host.LogQuery{Tail: n})
	if err != nil {
		return nil, err
	}
	var out []string
	for {
		select {
		case e, ok := <-ch:
			if !ok {
				return lastN(out, n), nil
			}
			line := e.Raw
			if line == "" {
				line = e.Time.Format(time.TimeOnly) + " " + e.Level.String() + " " + e.Msg
			}
			out = append(out, line)
		case <-lctx.Done():
			return lastN(out, n), nil
		}
	}
}

func lastN(s []string, n int) []string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}

func hasErrors(is []host.Issue) bool {
	for _, i := range is {
		if i.Severity == host.SevError {
			return true
		}
	}
	return false
}

func firstError(is []host.Issue) string {
	for _, i := range is {
		if i.Severity == host.SevError {
			if i.Line > 0 {
				return fmt.Sprintf("line %d: %s", i.Line, i.Message)
			}
			return i.Message
		}
	}
	return ""
}

// --- confirm text -----------------------------------------------------------

// changedKeys lists the keys a change touches, for the confirm dialog.
func changedKeys(c *host.Change) []string {
	a, err1 := hsconfig.Parse(c.File.Raw)
	b, err2 := hsconfig.Parse(c.Next)
	if err1 != nil || err2 != nil {
		return nil
	}
	return hsconfig.ChangedKeys(a, b)
}

func writeConsequence(c *host.Change) string {
	added, removed := files.Changed(c.Diff)
	keys := changedKeys(c)
	what := fmt.Sprintf("%d line(s) added, %d removed", added, removed)
	if n := len(keys); n > 0 && n <= 4 {
		what = strings.Join(keys, ", ")
	} else if n > 4 {
		what = fmt.Sprintf("%s and %d more keys", strings.Join(keys[:3], ", "), n-3)
	}
	s := fmt.Sprintf("Writes %s (%s). A backup is taken first; u restores it.", c.File.Path, what)
	if c.Effect == host.EffectRestart {
		s += " Takes effect after a restart."
	}
	return s
}

// errText explains a failed host operation in plain words.
func errText(err error) string {
	var pe *host.ErrPermission
	var inv *host.ErrInvalid
	switch {
	case errors.As(err, &pe):
		if pe.FixCommand != "" {
			return fmt.Sprintf("Permission denied: %s. Run: %s", pe.Op, pe.FixCommand)
		}
		return "Permission denied: " + pe.Op
	case errors.As(err, &inv):
		return "Nothing was written: " + inv.Error()
	}
	return err.Error()
}

// --- diff rendering ---------------------------------------------------------

// diffContext is how many unchanged lines frame each hunk.
const diffContext = 2

// diffLines renders d as numbered lines with context, hunks separated by a
// gap marker. Secret values are masked.
func diffLines(s *styles.Styles, ic icons.Set, d []host.DiffLine, w int) []string {
	keep := make([]bool, len(d))
	for i, l := range d {
		if l.Op == ' ' {
			continue
		}
		for j := max(0, i-diffContext); j <= min(len(d)-1, i+diffContext); j++ {
			keep[j] = true
		}
	}
	var out []string
	gap := false
	for i, l := range d {
		if !keep[i] {
			gap = true
			continue
		}
		if gap && len(out) > 0 {
			out = append(out, s.Muted.Render(ic.Ellipsis))
		}
		gap = false
		num := func(n int) string {
			if n == 0 {
				return "    "
			}
			return fmt.Sprintf("%4d", n)
		}
		text := maskLine(ic, l.Text)
		line := table.Fit(fmt.Sprintf("%s %s %c %s", num(l.Old), num(l.New), l.Op, text), w, lipgloss.Left, ic.Ellipsis)
		switch l.Op {
		case '+':
			line = s.OK.Render(line)
		case '-':
			line = s.Danger.Render(line)
		default:
			line = s.Muted.Render(line)
		}
		out = append(out, line)
	}
	return out
}

// issueLines renders validation findings, errors first.
func issueLines(s *styles.Styles, ic icons.Set, is []host.Issue, w int) []string {
	var out []string
	for _, sev := range []host.Severity{host.SevError, host.SevWarn, host.SevInfo} {
		for _, i := range is {
			if i.Severity != sev {
				continue
			}
			st, mark := s.Muted, ic.Bullet
			switch sev {
			case host.SevError:
				st, mark = s.Danger, ic.Cross
			case host.SevWarn:
				st, mark = s.Warn, ic.Warn
			}
			loc := ""
			if i.Line > 0 {
				loc = fmt.Sprintf("line %d: ", i.Line)
			}
			src := ""
			if i.Source != "" {
				src = " (" + i.Source + ")"
			}
			out = append(out, st.Render(table.Fit(mark+" "+loc+i.Message+src, w, lipgloss.Left, ic.Ellipsis)))
		}
	}
	return out
}

// window returns at most h lines of ls starting at off (clamped).
func window(ls []string, off, h int) ([]string, int) {
	if h <= 0 {
		return nil, 0
	}
	off = max(0, min(off, len(ls)-h))
	end := min(len(ls), off+h)
	return ls[off:end], off
}

// asDanger maps the host's rating onto the module's (same scale) and never
// lets a config write go through without at least a y/N.
func asDanger(d host.Danger) module.Danger {
	return max(module.Danger(d), module.DangerConfirm)
}
