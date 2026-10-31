package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/tonhe/headsup/internal/activity"
	"github.com/tonhe/headsup/internal/api"
	"github.com/tonhe/headsup/internal/brand"
	"github.com/tonhe/headsup/internal/config"
	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/store"
)

// Exit codes (contracts/cli.md).
const (
	ExitOK           = 0
	ExitError        = 1
	ExitUsage        = 2
	ExitUnauthorized = 3
	ExitNotFound     = 4
	ExitUnsupported  = 5
	ExitInvalid      = 6
	ExitAborted      = 7
	ExitNoHost       = 8
	ExitPermission   = 9
)

// Conn is a live connection the verbs run against.
type Conn struct {
	Backend   api.Backend
	Host      host.Host // nil in remote mode
	Label     string
	ServerURL string
	ReadOnly  bool
}

// Options wires the CLI to the process. Everything that touches the outside
// world is injected so tests run against fakes with a fixed clock.
type Options struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	// Interactive is true when stdin (and stderr) are a terminal, so a
	// destructive verb may prompt instead of failing with exit code 7.
	Interactive bool
	// Now is the clock; nil means time.Now.
	Now func() time.Time
	// Flags are the global flags parsed before the verb. Verbs accept the
	// connection flags after the verb too.
	Flags      config.Flags
	Config     config.Config
	ConfigPath string
	// Connect opens the connection for flags. It is only called by verbs
	// that need the server.
	Connect func(config.Flags) (Conn, error)
	// DetectEnv runs host detection without connecting (`host info`). nil
	// means host info needs a connection.
	DetectEnv func(config.Flags) host.Env
	// Activity records every mutation; nil means no log.
	Activity activity.Logger
	// PolicyHistoryDir is where policy revisions are kept; "" disables history.
	PolicyHistoryDir string
	// ThemesDir is the user themes directory (`themes reload`).
	ThemesDir string
}

// Error is a failure carrying its exit code and, for host problems, the
// command that fixes it (printed to stderr).
type Error struct {
	Code int
	Msg  string
	Fix  string
}

func (e *Error) Error() string { return e.Msg }

func errorf(code int, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

func usagef(format string, args ...any) *Error { return errorf(ExitUsage, format, args...) }

// codeOf maps any error to an exit code and the fix command, if any.
func codeOf(err error) (int, string) {
	var ce *Error
	if errors.As(err, &ce) {
		return ce.Code, ce.Fix
	}
	var pe *host.ErrPermission
	if errors.As(err, &pe) {
		return ExitPermission, pe.FixCommand
	}
	var inv *host.ErrInvalid
	if errors.As(err, &inv) {
		return ExitInvalid, ""
	}
	if errors.Is(err, host.ErrNoHost) {
		return ExitNoHost, ""
	}
	var ae *api.Error
	if errors.As(err, &ae) {
		switch ae.Kind {
		case api.KindUnauthorized:
			return ExitUnauthorized, ""
		case api.KindNotFound:
			return ExitNotFound, ""
		case api.KindUnsupported:
			return ExitUnsupported, ""
		case api.KindInvalid:
			return ExitInvalid, ""
		}
	}
	return ExitError, ""
}

// Run executes one verb (args excludes the program name and global flags)
// and returns the process exit code.
func Run(args []string, o Options) int {
	r := newRunner(o)
	defer r.close()
	err := r.dispatch(args)
	if err == nil {
		return ExitOK
	}
	code, fix := codeOf(err)
	msg := err.Error()
	if code == ExitOK {
		return ExitOK
	}
	if ae := (*api.Error)(nil); errors.As(err, &ae) && ae.Hint != "" && !strings.Contains(msg, ae.Hint) {
		msg += "\n" + ae.Hint
	}
	fmt.Fprintf(r.stderr, "%s: %s\n", brand.Name, msg)
	if fix != "" && !strings.Contains(msg, fix) {
		fmt.Fprintf(r.stderr, "Fix: %s\n", fix)
	}
	return code
}

// runner holds the per-invocation state.
type runner struct {
	o      Options
	stdout io.Writer
	stderr io.Writer
	now    func() time.Time
	flags  config.Flags

	conn    *Conn
	rec     *recorder
	st      *store.Store
	loaded  store.Slice
	cancels []context.CancelFunc
	in      *bufio.Reader
}

func newRunner(o Options) *runner {
	if o.Stdout == nil {
		o.Stdout = io.Discard
	}
	if o.Stderr == nil {
		o.Stderr = io.Discard
	}
	if o.Stdin == nil {
		o.Stdin = strings.NewReader("")
	}
	now := o.Now
	if now == nil {
		now = time.Now
	}
	if o.Activity == nil {
		o.Activity = activity.Nop{}
	}
	return &runner{o: o, stdout: o.Stdout, stderr: o.Stderr, now: now, flags: o.Flags}
}

func (r *runner) close() {
	for _, c := range r.cancels {
		c()
	}
}

// connect opens the connection once and learns the server's capabilities.
func (r *runner) connect() (*Conn, error) {
	if r.conn != nil {
		return r.conn, nil
	}
	if r.o.Connect == nil {
		return nil, errorf(ExitError, "no server connection is configured")
	}
	c, err := r.o.Connect(r.flags)
	if err != nil {
		return nil, err
	}
	if r.flags.ReadOnly {
		c.ReadOnly = true
	}
	r.rec = &recorder{Backend: c.Backend}
	c.Backend = r.rec
	r.conn = &c
	r.st = store.New()
	ctx, cancel := api.WithTimeout(context.Background())
	defer cancel()
	info, err := c.Backend.Connect(ctx)
	if err != nil {
		return nil, err
	}
	r.st.Info = info
	return r.conn, nil
}

// hostConn connects and requires host mode (exit code 8 otherwise).
func (r *runner) hostConn() (*Conn, error) {
	if r.conn == nil && r.flags.Demo {
		r.flags.Host = true // the demo has a simulated host
	}
	c, err := r.connect()
	if err != nil {
		return nil, err
	}
	if c.Host == nil {
		return nil, &Error{Code: ExitNoHost,
			Msg: "this verb needs host mode: run " + brand.Name + " on the headscale server (or pass --host)",
			Fix: brand.Name + " host info"}
	}
	return c, nil
}

// need fails with exit code 9 and the fix when the host lacks a permission.
func (r *runner) need(n host.Need) error {
	c, err := r.hostConn()
	if err != nil {
		return err
	}
	if p, missing := c.Host.Env().Perms.Missing(n); missing {
		return &Error{Code: ExitPermission, Msg: "permission denied: " + p.Reason, Fix: p.FixCommand}
	}
	return nil
}

// load fetches the store slices verbs and modules read.
func (r *runner) load(sl store.Slice) (*store.Store, error) {
	c, err := r.connect()
	if err != nil {
		return nil, err
	}
	want := sl &^ r.loaded
	if want == 0 {
		return r.st, nil
	}
	if c.Host == nil {
		want &^= store.SliceHost
	}
	sn := store.Fetch(context.Background(), c.Backend, c.Host, want)
	for _, s := range []store.Slice{store.SliceNodes, store.SliceUsers, store.SlicePreAuthKeys, store.SliceAPIKeys, store.SlicePolicy} {
		if e := sn.Errs[s]; e != nil {
			return nil, e
		}
	}
	r.st.Apply(sn)
	r.loaded |= want
	return r.st, nil
}

// reload forces the slices to be fetched again (after a change).
func (r *runner) reload(sl store.Slice) error {
	r.loaded &^= sl
	_, err := r.load(sl)
	return err
}

func (r *runner) logf(format string, args ...any) {
	fmt.Fprintf(r.stderr, format+"\n", args...)
}
