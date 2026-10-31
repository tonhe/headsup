package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/tonhe/headsup/internal/host"
)

// ErrUnsupported means the service manager cannot perform the operation
// (e.g. starting a bare process).
var ErrUnsupported = errors.New("not supported by this service manager")

var permPatterns = []string{
	"access denied",
	"interactive authentication required",
	"permission denied",
	"operation not permitted",
	"authentication is required",
	"must be root",
	"superuser access required",
	"got permission denied while trying to connect to the docker daemon",
}

// isPermission reports whether a failed command failed for lack of rights.
func isPermission(r Result) bool {
	out := strings.ToLower(r.Output())
	for _, p := range permPatterns {
		if strings.Contains(out, p) {
			return true
		}
	}
	return false
}

// CmdError is a failed service command.
type CmdError struct {
	Cmd    string
	Exit   int
	Output string
}

func (e *CmdError) Error() string {
	if e.Output == "" {
		return fmt.Sprintf("%s: exit status %d", e.Cmd, e.Exit)
	}
	return fmt.Sprintf("%s: exit status %d: %s", e.Cmd, e.Exit, e.Output)
}

// run executes argv and maps failures to ErrPermission or CmdError. fix is
// the command the user can run themselves on a permission failure.
func run(ctx context.Context, rf RunFunc, fix string, argv ...string) (Result, error) {
	res, err := rf(ctx, argv[0], argv[1:]...)
	if err != nil {
		return res, err
	}
	if res.Exit != 0 {
		if isPermission(res) {
			return res, &host.ErrPermission{Need: host.NeedService, Op: strings.Join(argv, " "), FixCommand: fix}
		}
		return res, &CmdError{Cmd: strings.Join(argv, " "), Exit: res.Exit, Output: res.Output()}
	}
	return res, nil
}

// headscaleCmd is how to invoke the headscale binary (possibly via a container exec).
type headscaleCmd []string

func (h headscaleCmd) argv(args ...string) []string {
	if len(h) == 0 {
		h = headscaleCmd{"headscale"}
	}
	return append(append([]string{}, h...), args...)
}

func configtest(ctx context.Context, rf RunFunc, hs headscaleCmd, path string) ([]host.Issue, error) {
	argv := hs.argv("configtest")
	if path != "" {
		argv = append(argv, "-c", path)
	}
	res, err := rf(ctx, argv[0], argv[1:]...)
	if err != nil {
		return nil, err
	}
	issues := ParseConfigtest(res.Output())
	if res.Exit != 0 {
		hasErr := false
		for _, i := range issues {
			if i.Severity == host.SevError {
				hasErr = true
			}
		}
		if !hasErr {
			msg := lastLine(res.Output())
			if msg == "" {
				msg = fmt.Sprintf("headscale configtest exited with status %d", res.Exit)
			}
			if isPermission(res) {
				return issues, &host.ErrPermission{Need: host.NeedConfigR, Op: "headscale configtest", FixCommand: "sudo headscale configtest -c " + path}
			}
			issues = append(issues, host.Issue{Severity: host.SevError, Message: msg, Source: "configtest"})
		}
	}
	return issues, nil
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[i+1:])
	}
	return s
}

var (
	ansiRE      = regexp.MustCompile("\x1b\\[[0-9;]*[A-Za-z]")
	zerologRE   = regexp.MustCompile(`^\S+\s+(TRC|DBG|INF|WRN|ERR|FTL|PNC)\s+(.*)$`)
	lineRE      = regexp.MustCompile(`(?i)\bline (\d+)(?:[:,]? ?(?:col(?:umn)? )?(\d+))?`)
	errFieldRE  = regexp.MustCompile(`\berror="((?:[^"\\]|\\.)*)"|\berror=(\S+)`)
	jsonLevelRE = regexp.MustCompile(`"level":"(\w+)"`)
	jsonMsgRE   = regexp.MustCompile(`"(?:message|error)":"((?:[^"\\]|\\.)*)"`)
)

// ParseConfigtest turns `headscale configtest` output into issues. It
// understands 0.30's structured "Fatal config error:" blocks, zerolog
// console and JSON lines (0.29), and cobra's "Error: ..." line.
func ParseConfigtest(out string) []host.Issue {
	out = ansiRE.ReplaceAllString(out, "")
	var issues []host.Issue
	var cur *host.Issue
	flush := func() {
		if cur != nil {
			issues = append(issues, *cur)
			cur = nil
		}
	}
	for _, raw := range strings.Split(out, "\n") {
		ln := strings.TrimRight(raw, "\r ")
		t := strings.TrimSpace(ln)
		switch {
		case t == "":
			continue
		case strings.HasPrefix(t, "Fatal config error:"):
			flush()
			cur = &host.Issue{Severity: host.SevError, Message: strings.TrimSpace(strings.TrimPrefix(t, "Fatal config error:")), Source: "configtest"}
		case cur != nil && strings.HasPrefix(ln, "  ") && strings.Contains(t, ": "):
			cur.Message += "; " + t
		case strings.HasPrefix(t, "{") && jsonLevelRE.MatchString(t):
			flush()
			lvl := jsonLevelRE.FindStringSubmatch(t)[1]
			var msgs []string
			for _, m := range jsonMsgRE.FindAllStringSubmatch(t, -1) {
				if s, err := strconv.Unquote(`"` + m[1] + `"`); err == nil {
					msgs = append(msgs, s)
				}
			}
			if sev, ok := sevOf(lvl); ok {
				issues = append(issues, withLine(host.Issue{Severity: sev, Message: strings.Join(msgs, ": "), Source: "configtest"}))
			}
		case zerologRE.MatchString(t):
			flush()
			m := zerologRE.FindStringSubmatch(t)
			sev, ok := sevOf(m[1])
			if !ok {
				continue
			}
			msg := m[2]
			if e := errFieldRE.FindStringSubmatch(msg); e != nil {
				ev := e[2]
				if e[1] != "" {
					ev, _ = strconv.Unquote(`"` + e[1] + `"`)
				}
				msg = strings.TrimSpace(errFieldRE.ReplaceAllString(msg, "")) + ": " + ev
			}
			issues = append(issues, withLine(host.Issue{Severity: sev, Message: msg, Source: "configtest"}))
		case strings.HasPrefix(t, "Error:"):
			flush()
			msg := strings.TrimSpace(strings.TrimPrefix(t, "Error:"))
			if dupOf(issues, msg) {
				continue
			}
			issues = append(issues, withLine(host.Issue{Severity: host.SevError, Message: msg, Source: "configtest"}))
		}
	}
	flush()
	return issues
}

func dupOf(issues []host.Issue, msg string) bool {
	for _, i := range issues {
		if strings.Contains(msg, i.Message) || strings.Contains(i.Message, msg) {
			return true
		}
	}
	return false
}

func withLine(i host.Issue) host.Issue {
	if m := lineRE.FindStringSubmatch(i.Message); m != nil {
		i.Line, _ = strconv.Atoi(m[1])
		if m[2] != "" {
			i.Col, _ = strconv.Atoi(m[2])
		}
	}
	return i
}

func sevOf(l string) (host.Severity, bool) {
	switch strings.ToLower(l) {
	case "wrn", "warn", "warning":
		return host.SevWarn, true
	case "err", "error", "ftl", "fatal", "pnc", "panic":
		return host.SevError, true
	}
	return 0, false
}

// --- systemd ----------------------------------------------------------------

// Systemd manages a systemd unit with systemctl.
type Systemd struct {
	Run       RunFunc
	Unit      string // default "headscale.service"
	Headscale []string
}

// NewSystemd returns a systemd ServiceManager.
func NewSystemd(rf RunFunc, unit string) *Systemd {
	if unit == "" {
		unit = "headscale.service"
	}
	return &Systemd{Run: rf, Unit: unit}
}

func (s *Systemd) Kind() host.ServiceKind { return host.ServiceSystemd }

func (s *Systemd) short() string { return strings.TrimSuffix(s.Unit, ".service") }

func (s *Systemd) do(ctx context.Context, verb string) error {
	_, err := run(ctx, s.Run, "sudo systemctl "+verb+" "+s.short(), "systemctl", verb, s.Unit)
	return err
}

func (s *Systemd) Start(ctx context.Context) error   { return s.do(ctx, "start") }
func (s *Systemd) Stop(ctx context.Context) error    { return s.do(ctx, "stop") }
func (s *Systemd) Restart(ctx context.Context) error { return s.do(ctx, "restart") }
func (s *Systemd) Reload(ctx context.Context) error  { return s.do(ctx, "reload") }

func (s *Systemd) Configtest(ctx context.Context, path string) ([]host.Issue, error) {
	return configtest(ctx, s.Run, s.Headscale, path)
}

// Status runs `systemctl show` and parses its key=value output.
func (s *Systemd) Status(ctx context.Context) (host.ServiceStatus, error) {
	res, err := run(ctx, s.Run, "", "systemctl", "show", s.Unit,
		"-p", "ActiveState,SubState,LoadState,MainPID,ExecMainStartTimestamp,MemoryCurrent")
	if err != nil {
		return host.ServiceStatus{State: "unknown"}, err
	}
	return ParseSystemdShow(string(res.Stdout)), nil
}

// ParseSystemdShow parses `systemctl show -p ...` output.
func ParseSystemdShow(out string) host.ServiceStatus {
	kv := map[string]string{}
	for _, l := range strings.Split(out, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(l), "="); ok {
			kv[k] = v
		}
	}
	st := host.ServiceStatus{State: kv["ActiveState"]}
	if st.State == "" {
		st.State = "unknown"
	}
	if kv["LoadState"] == "not-found" {
		st.State = "unknown"
	}
	st.Running = st.State == "active" && (kv["SubState"] == "" || kv["SubState"] == "running")
	st.PID, _ = strconv.Atoi(kv["MainPID"])
	if m, err := strconv.ParseUint(kv["MemoryCurrent"], 10, 64); err == nil && m != 1<<64-1 {
		st.Memory = m
	}
	st.Since = parseSystemdTime(kv["ExecMainStartTimestamp"])
	return st
}

func parseSystemdTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" || s == "n/a" {
		return time.Time{}
	}
	for _, layout := range []string{"Mon 2006-01-02 15:04:05 MST", "Mon 2006-01-02 15:04:05 -0700", "2006-01-02 15:04:05 MST"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// --- OpenRC -----------------------------------------------------------------

// OpenRC manages an OpenRC service with rc-service.
type OpenRC struct {
	Run       RunFunc
	Name      string
	Headscale []string
}

// NewOpenRC returns an OpenRC ServiceManager.
func NewOpenRC(rf RunFunc, name string) *OpenRC {
	if name == "" {
		name = "headscale"
	}
	return &OpenRC{Run: rf, Name: name}
}

func (o *OpenRC) Kind() host.ServiceKind { return host.ServiceOpenRC }

func (o *OpenRC) do(ctx context.Context, verb string) error {
	_, err := run(ctx, o.Run, "sudo rc-service "+o.Name+" "+verb, "rc-service", o.Name, verb)
	return err
}

func (o *OpenRC) Start(ctx context.Context) error   { return o.do(ctx, "start") }
func (o *OpenRC) Stop(ctx context.Context) error    { return o.do(ctx, "stop") }
func (o *OpenRC) Restart(ctx context.Context) error { return o.do(ctx, "restart") }
func (o *OpenRC) Reload(ctx context.Context) error  { return o.do(ctx, "reload") }

func (o *OpenRC) Configtest(ctx context.Context, path string) ([]host.Issue, error) {
	return configtest(ctx, o.Run, o.Headscale, path)
}

// Status parses `rc-service <name> status` (" * status: started").
func (o *OpenRC) Status(ctx context.Context) (host.ServiceStatus, error) {
	res, err := o.Run(ctx, "rc-service", o.Name, "status")
	if err != nil {
		return host.ServiceStatus{State: "unknown"}, err
	}
	out := strings.ToLower(res.Output())
	st := host.ServiceStatus{State: "unknown"}
	if i := strings.Index(out, "status:"); i >= 0 {
		st.State = strings.Fields(out[i+len("status:"):] + " unknown")[0]
	} else if res.Exit == 3 {
		st.State = "stopped"
	}
	switch st.State {
	case "started":
		st.State, st.Running = "active", true
	case "stopped":
		st.State = "inactive"
	case "crashed":
		st.State = "failed"
	}
	return st, nil
}

// --- container --------------------------------------------------------------

// Container manages a docker or podman container.
type Container struct {
	Run     RunFunc
	Runtime string // docker or podman
	Name    string
}

// NewContainer returns a container ServiceManager.
func NewContainer(rf RunFunc, runtime, name string) *Container {
	if runtime == "" {
		runtime = "docker"
	}
	return &Container{Run: rf, Runtime: runtime, Name: name}
}

func (c *Container) Kind() host.ServiceKind { return host.ServiceContainer }

func (c *Container) do(ctx context.Context, argv ...string) error {
	fix := "sudo " + strings.Join(argv, " ")
	if c.Runtime == "docker" {
		fix = "sudo usermod -aG docker $USER && newgrp docker"
	}
	_, err := run(ctx, c.Run, fix, argv...)
	return err
}

func (c *Container) Start(ctx context.Context) error { return c.do(ctx, c.Runtime, "start", c.Name) }
func (c *Container) Stop(ctx context.Context) error  { return c.do(ctx, c.Runtime, "stop", c.Name) }
func (c *Container) Restart(ctx context.Context) error {
	return c.do(ctx, c.Runtime, "restart", c.Name)
}

// Reload sends SIGHUP to the container's main process.
func (c *Container) Reload(ctx context.Context) error {
	return c.do(ctx, c.Runtime, "kill", "--signal", "HUP", c.Name)
}

// Configtest runs configtest inside the container; path is the in-container path.
//
// path is where the config is on this machine (the bind-mount source), which
// means nothing inside the container, so headscale checks the config it was
// started with instead: the bind-mounted file itself.
func (c *Container) Configtest(ctx context.Context, path string) ([]host.Issue, error) {
	return configtest(ctx, c.Run, headscaleCmd{c.Runtime, "exec", c.Name, "headscale"}, "")
}

// Status parses `<runtime> inspect` output.
func (c *Container) Status(ctx context.Context) (host.ServiceStatus, error) {
	res, err := run(ctx, c.Run, "", c.Runtime, "inspect", "--format", "{{.State.Status}}|{{.State.Pid}}|{{.State.StartedAt}}", c.Name)
	if err != nil {
		return host.ServiceStatus{State: "unknown"}, err
	}
	parts := strings.Split(strings.TrimSpace(string(res.Stdout)), "|")
	st := host.ServiceStatus{State: "unknown"}
	if len(parts) >= 1 && parts[0] != "" {
		st.State = parts[0]
	}
	st.Running = st.State == "running"
	if len(parts) >= 2 {
		st.PID, _ = strconv.Atoi(parts[1])
	}
	if len(parts) >= 3 {
		st.Since, _ = time.Parse(time.RFC3339Nano, parts[2])
	}
	return st, nil
}

// --- bare process -----------------------------------------------------------

// Process signals a headscale process that no service manager owns. Only
// Reload (SIGHUP) is supported.
type Process struct {
	Run       RunFunc
	Headscale []string
}

// NewProcess returns a process ServiceManager.
func NewProcess(rf RunFunc) *Process { return &Process{Run: rf} }

func (p *Process) Kind() host.ServiceKind { return host.ServiceProcess }

func (p *Process) pid(ctx context.Context) (int, error) {
	res, err := p.Run(ctx, "pgrep", "-x", "headscale")
	if err != nil {
		return 0, err
	}
	f := strings.Fields(string(res.Stdout))
	if res.Exit != 0 || len(f) == 0 {
		return 0, errors.New("no headscale process found")
	}
	return strconv.Atoi(f[0])
}

func (p *Process) Status(ctx context.Context) (host.ServiceStatus, error) {
	pid, err := p.pid(ctx)
	if err != nil {
		return host.ServiceStatus{State: "inactive"}, nil
	}
	return host.ServiceStatus{State: "running", Running: true, PID: pid}, nil
}

func (p *Process) Start(context.Context) error   { return ErrUnsupported }
func (p *Process) Stop(context.Context) error    { return ErrUnsupported }
func (p *Process) Restart(context.Context) error { return ErrUnsupported }

// Reload sends SIGHUP with kill(1).
func (p *Process) Reload(ctx context.Context) error {
	pid, err := p.pid(ctx)
	if err != nil {
		return err
	}
	ps := strconv.Itoa(pid)
	_, err = run(ctx, p.Run, "sudo kill -HUP "+ps, "kill", "-HUP", ps)
	return err
}

func (p *Process) Configtest(ctx context.Context, path string) ([]host.Issue, error) {
	return configtest(ctx, p.Run, p.Headscale, path)
}

// --- none -------------------------------------------------------------------

// None is the ServiceManager when no service was detected. Configtest still
// works when a RunFunc is set and the headscale binary is present.
type None struct{ Run RunFunc }

func (None) Kind() host.ServiceKind { return host.ServiceNone }
func (None) Status(context.Context) (host.ServiceStatus, error) {
	return host.ServiceStatus{State: "unknown"}, host.ErrNoHost
}
func (None) Start(context.Context) error   { return host.ErrNoHost }
func (None) Stop(context.Context) error    { return host.ErrNoHost }
func (None) Restart(context.Context) error { return host.ErrNoHost }
func (None) Reload(context.Context) error  { return host.ErrNoHost }
func (n None) Configtest(ctx context.Context, path string) ([]host.Issue, error) {
	if n.Run == nil {
		return nil, host.ErrNoHost
	}
	return configtest(ctx, n.Run, nil, path)
}

var (
	_ host.ServiceManager = (*Systemd)(nil)
	_ host.ServiceManager = (*OpenRC)(nil)
	_ host.ServiceManager = (*Container)(nil)
	_ host.ServiceManager = (*Process)(nil)
	_ host.ServiceManager = None{}
)
