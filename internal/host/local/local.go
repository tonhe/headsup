// Package local is the real host.Host: it wires the managed-file pipeline,
// service manager, log source, debug client and read-only database
// according to the detected host.Env. Pieces the host lacks return
// host.ErrNoHost (or are no-ops).
package local

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/tonhe/fathom/internal/host"
	"github.com/tonhe/fathom/internal/host/db"
	"github.com/tonhe/fathom/internal/host/debug"
	"github.com/tonhe/fathom/internal/host/files"
	"github.com/tonhe/fathom/internal/host/hsconfig"
	"github.com/tonhe/fathom/internal/host/logs"
	"github.com/tonhe/fathom/internal/host/service"
)

// Options tunes the adapter. Zero values use the real OS.
type Options struct {
	BackupDir        string
	Keep             int
	Run              service.RunFunc
	Stream           logs.StreamFunc
	ContainerRuntime string // docker or podman; default parsed from Env.Notes, else docker
	HTTPClient       *http.Client
	DB               db.Options
	Now              func() time.Time
	Root             string // filesystem prefix for managed files (tests)
}

// Host implements host.Host.
type Host struct {
	env   host.Env
	o     Options
	files *files.Manager
	svc   host.ServiceManager
	dbg   host.DebugClient
	met   *debug.Client
	logs  logs.Source
}

var _ host.Host = (*Host)(nil)

// ContainerRuntime returns the runtime detect found (falling back to the
// note older detection recorded in env.Notes).
func ContainerRuntime(env host.Env) string {
	if env.ContainerRuntime != "" {
		return env.ContainerRuntime
	}
	for _, n := range env.Notes {
		if rt, ok := strings.CutPrefix(n, "container runtime: "); ok {
			return strings.TrimSpace(rt)
		}
	}
	return ""
}

// New builds the adapter for env.
func New(env host.Env, o Options) *Host {
	if o.Run == nil {
		o.Run = service.Exec
	}
	if o.Stream == nil {
		o.Stream = logs.ExecStream
	}
	if o.ContainerRuntime == "" {
		o.ContainerRuntime = ContainerRuntime(env)
	}
	if o.ContainerRuntime == "" {
		o.ContainerRuntime = "docker"
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	o.DB.AllowImmutable = true // a stale read beats no enrichment; db.Open records a note
	h := &Host{env: env, o: o}

	switch env.Service {
	case host.ServiceSystemd:
		h.svc = service.NewSystemd(o.Run, "headscale.service")
	case host.ServiceOpenRC:
		h.svc = service.NewOpenRC(o.Run, "headscale")
	case host.ServiceContainer:
		h.svc = service.NewContainer(o.Run, o.ContainerRuntime, env.Container)
	case host.ServiceProcess:
		h.svc = service.NewProcess(o.Run)
	default:
		var rf service.RunFunc
		if env.Transport == host.TransportExecCLI || env.Transport == host.TransportUnixHTTP {
			rf = o.Run
		}
		h.svc = service.None{Run: rf}
	}

	schema := map[host.FileKind]files.SchemaFunc{
		host.FileConfig:       func(b []byte) []host.Issue { return ConfigSchema(b, env.Version) },
		host.FilePolicy:       PolicySchema,
		host.FileExtraRecords: RecordsSchema,
		host.FileDERPMap:      DERPMapSchema,
	}
	external := map[host.FileKind]files.ExternalFunc{}
	if env.Service != host.ServiceContainer && env.Version != "" {
		external[host.FileConfig] = func(ctx context.Context, tmp string) ([]host.Issue, error) {
			return h.svc.Configtest(ctx, tmp)
		}
		external[host.FilePolicy] = func(ctx context.Context, tmp string) ([]host.Issue, error) {
			return policyCheck(ctx, o.Run, tmp)
		}
	}
	h.files = files.New(files.Options{
		Root: o.Root, BackupDir: o.BackupDir, Keep: o.Keep, Now: o.Now,
		Schema: schema, External: external,
		Danger: func(k host.FileKind, old, next []byte) host.Danger {
			if k == host.FileConfig {
				return max(hsconfig.Danger(old, next), host.DangerConfirm)
			}
			if k == host.FileExtraRecords {
				return host.DangerNone
			}
			return host.DangerConfirm
		},
	})

	if env.DebugAddr != "" {
		var opts []debug.Option
		if o.HTTPClient != nil {
			opts = append(opts, debug.WithHTTPClient(o.HTTPClient))
		}
		h.met = debug.New(env.DebugAddr, opts...)
		h.dbg = h.met
	} else {
		h.dbg = NoDebug{}
	}

	switch env.LogSource {
	case host.LogJournald:
		h.logs = logs.Journald{Start: o.Stream, Unit: "headscale"}
	case host.LogContainer:
		h.logs = logs.Container{Start: o.Stream, Runtime: o.ContainerRuntime, Name: env.Container}
	case host.LogFile:
		h.logs = logs.File{Path: env.LogPath}
	}
	return h
}

func (h *Host) Env() host.Env { return h.env }

// Files exposes the file manager (backups listing, backup-root check).
func (h *Host) Files() *files.Manager { return h.files }

func (h *Host) defaultPath(kind host.FileKind) (string, error) {
	switch kind {
	case host.FileConfig:
		if h.env.ConfigPath != "" {
			return h.env.ConfigPath, nil
		}
		return "", fmt.Errorf("%w: no headscale config found", host.ErrNoHost)
	case host.FilePolicy:
		if h.env.PolicyMode == "database" {
			return "", fmt.Errorf("%w: policy.mode is database", host.ErrNoHost)
		}
		if h.env.PolicyPath != "" {
			return h.env.PolicyPath, nil
		}
		return "", fmt.Errorf("%w: policy.path is not set", host.ErrNoHost)
	case host.FileExtraRecords:
		if h.env.RecordsPath != "" {
			return h.env.RecordsPath, nil
		}
		return "", fmt.Errorf("%w: dns.extra_records_path is not set", host.ErrNoHost)
	}
	return "", fmt.Errorf("%w: a path is required for %s files", host.ErrNoHost, kind)
}

func (h *Host) Open(kind host.FileKind, path string) (*host.File, error) {
	if path == "" {
		p, err := h.defaultPath(kind)
		if err != nil {
			return nil, err
		}
		path = p
	}
	return h.files.Open(kind, path)
}

func (h *Host) Plan(ctx context.Context, f *host.File, next []byte) (*host.Change, error) {
	return h.files.Plan(ctx, f, next)
}

func (h *Host) Apply(ctx context.Context, c *host.Change) (host.Applied, error) {
	return h.files.Apply(ctx, c)
}

func (h *Host) Rollback(ctx context.Context, a host.Applied) error {
	return h.files.Rollback(ctx, a)
}

func (h *Host) Service() host.ServiceManager { return h.svc }

func (h *Host) Logs(ctx context.Context, q host.LogQuery) (<-chan host.LogEntry, error) {
	if h.logs == nil {
		return nil, host.ErrNoHost
	}
	if p, probed := h.env.Perms[host.NeedLogs]; probed && !p.OK && h.env.LogSource == host.LogJournald {
		return nil, &host.ErrPermission{Need: host.NeedLogs, Op: "read the headscale journal", FixCommand: p.FixCommand}
	}
	return h.logs.Stream(ctx, q)
}

func (h *Host) Debug() host.DebugClient { return h.dbg }

func (h *Host) Metrics(ctx context.Context) (host.MetricsSample, error) {
	if h.met == nil {
		return host.MetricsSample{}, host.ErrNoHost
	}
	return h.met.Metrics(ctx)
}

// NodeExtras opens the database read-only for the call.
func (h *Host) NodeExtras(ctx context.Context, ids []uint64) (map[uint64]host.NodeExtras, error) {
	if h.env.DB == host.DBUnknown || h.env.DBPath == "" {
		return nil, host.ErrNoHost
	}
	if p, probed := h.env.Perms[host.NeedDBRead]; probed && !p.OK {
		return nil, &host.ErrPermission{Need: host.NeedDBRead, Op: "read the headscale database", FixCommand: p.FixCommand}
	}
	d, err := db.Open(h.env.DB, h.env.DBPath, h.o.DB)
	if err != nil {
		return nil, err
	}
	defer d.Close()
	return d.NodeExtras(ctx, ids)
}

// NoDebug is the DebugClient when metrics_listen_addr is disabled.
type NoDebug struct{}

func (NoDebug) Available() bool { return false }
func (NoDebug) RegistrationCache(context.Context) ([]host.RegistrationEntry, error) {
	return nil, host.ErrNoHost
}
func (NoDebug) Routes(context.Context) ([]host.RouteState, error) { return nil, host.ErrNoHost }
func (NoDebug) Ping(context.Context, uint64) (time.Duration, error) {
	return 0, host.ErrNoHost
}

func policyCheck(ctx context.Context, rf service.RunFunc, tmp string) ([]host.Issue, error) {
	res, err := rf(ctx, "headscale", "policy", "check", "-f", tmp)
	if err != nil {
		return nil, err
	}
	issues := service.ParseConfigtest(res.Output())
	for i := range issues {
		issues[i].Source = "policy check"
	}
	if res.Exit != 0 && !hasError(issues) {
		msg := strings.TrimSpace(res.Output())
		if msg == "" {
			msg = fmt.Sprintf("headscale policy check exited with status %d", res.Exit)
		}
		issues = append(issues, host.Issue{Severity: host.SevError, Message: msg, Source: "policy check"})
	}
	return issues, nil
}

func hasError(is []host.Issue) bool {
	for _, i := range is {
		if i.Severity == host.SevError {
			return true
		}
	}
	return false
}
