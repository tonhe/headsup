// Package host is the seam between the UI and the machine headscale runs on:
// environment detection, managed files, the service manager, logs, the local
// debug/metrics endpoints, and read-only database enrichment.
//
// Nothing in this package tree may run sudo/su/doas/pkexec or write to the
// headscale database; both rules are enforced by lint tests.
package host

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"path"
	"strings"
	"time"
)

// Host is implemented by the real host adapter and by host/fake.
type Host interface {
	Env() Env

	// Managed files.
	Open(kind FileKind, path string) (*File, error)
	Plan(ctx context.Context, f *File, next []byte) (*Change, error)
	Apply(ctx context.Context, c *Change) (Applied, error)
	Rollback(ctx context.Context, a Applied) error

	Service() ServiceManager
	Logs(ctx context.Context, q LogQuery) (<-chan LogEntry, error)
	Debug() DebugClient
	Metrics(ctx context.Context) (MetricsSample, error)
	NodeExtras(ctx context.Context, ids []uint64) (map[uint64]NodeExtras, error)
}

// TransportKind is how the app reaches the local headscale API.
type TransportKind int

const (
	TransportNone TransportKind = iota
	TransportUnixHTTP
	TransportExecCLI
	TransportContainerExec
)

func (t TransportKind) String() string {
	return [...]string{"none", "unix socket (HTTP)", "headscale CLI", "container exec"}[t]
}

// ServiceKind is the detected service manager.
type ServiceKind int

const (
	ServiceNone ServiceKind = iota
	ServiceSystemd
	ServiceOpenRC
	ServiceContainer
	ServiceProcess
)

func (s ServiceKind) String() string {
	return [...]string{"none", "systemd", "OpenRC", "container", "process"}[s]
}

// LogSourceKind is where headscale's logs can be read.
type LogSourceKind int

const (
	LogNone LogSourceKind = iota
	LogJournald
	LogContainer
	LogFile
)

func (l LogSourceKind) String() string {
	return [...]string{"none", "journald", "container", "file"}[l]
}

// DBKind is the headscale database type.
type DBKind int

const (
	DBUnknown DBKind = iota
	DBSQLite
	DBPostgres
)

func (d DBKind) String() string { return [...]string{"unknown", "sqlite", "postgres"}[d] }

// Need is a host permission an action requires.
type Need uint32

const (
	NeedNone     Need = 0
	NeedSocket   Need = 1 << iota // talk to the local API
	NeedConfigR                   // read config.yaml
	NeedConfigW                   // write config.yaml
	NeedPolicyW                   // write the policy file
	NeedRecordsW                  // write the extra-records file
	NeedService                   // control the service
	NeedLogs                      // read logs
	NeedDebug                     // reach metrics_listen_addr
	NeedDBRead                    // read the database
)

// Perm is the outcome of probing one Need.
type Perm struct {
	OK         bool
	Reason     string // why not, in plain words
	FixCommand string // exact command to run, e.g. "sudo usermod -aG headscale $USER"
	Denied     bool   // the failure is missing access, so root would get past it
}

// Perms maps each probed Need to its outcome. Missing entries mean "not probed / not applicable".
type Perms map[Need]Perm

// Has reports whether every bit in n is granted.
func (p Perms) Has(n Need) bool {
	if n == NeedNone {
		return true
	}
	for bit := Need(1); bit != 0 && bit <= n; bit <<= 1 {
		if n&bit != 0 && !p[bit].OK {
			return false
		}
	}
	return true
}

// Missing returns the first missing Need's Perm in n, for explaining a disabled action.
func (p Perms) Missing(n Need) (Perm, bool) {
	for bit := Need(1); bit != 0 && bit <= n; bit <<= 1 {
		if n&bit != 0 && !p[bit].OK {
			pm := p[bit]
			if pm.Reason == "" {
				pm.Reason = "not available on this host"
			}
			return pm, true
		}
	}
	return Perm{}, false
}

// Env is what Detect found on this machine.
type Env struct {
	Hostname         string
	ConfigPath       string
	Version          string
	Transport        TransportKind
	SocketPath       string
	Container        string // container name for TransportContainerExec / ServiceContainer
	ContainerRuntime string // docker or podman, when Container is set
	Service          ServiceKind
	LogSource        LogSourceKind
	LogPath          string
	DB               DBKind
	DBPath           string
	DebugAddr        string // metrics_listen_addr
	ServerURL        string
	PolicyMode       string // "file" or "database"
	PolicyPath       string
	RecordsPath      string
	// Mounts maps paths inside the container (as config.yaml names them)
	// to where they are on this machine, for a containerised headscale.
	Mounts []Mount
	Perms  Perms
	Notes  []string // human-readable detection remarks
}

// Mount is one bind mount of a containerised headscale.
type Mount struct{ Source, Destination string }

// HostPath maps a path inside the container to this machine through the
// bind mounts. Outside container mode, or for a path that is not inside any
// mount, it returns p unchanged and false.
func (e Env) HostPath(p string) (string, bool) {
	best := Mount{}
	for _, m := range e.Mounts {
		d := strings.TrimSuffix(m.Destination, "/")
		if (p == m.Destination || strings.HasPrefix(p, d+"/")) && len(m.Destination) > len(best.Destination) {
			best = m
		}
	}
	if best.Destination == "" {
		return p, false
	}
	return path.Join(best.Source, strings.TrimPrefix(p, best.Destination)), true
}

// Usable reports whether host mode can talk to headscale at all.
func (e Env) Usable() bool { return e.Transport != TransportNone && e.Perms.Has(NeedSocket) }

// FileKind identifies a managed file.
type FileKind int

const (
	FileConfig FileKind = iota
	FilePolicy
	FileExtraRecords
	FileDERPMap
)

func (k FileKind) String() string {
	return [...]string{"config", "policy", "extra records", "DERP map"}[k]
}

// Effect is when a written change takes effect.
type Effect int

const (
	EffectLive Effect = iota
	EffectReload
	EffectRestart
)

func (e Effect) String() string { return [...]string{"live", "on reload", "needs restart"}[e] }

// Danger mirrors module.Danger without importing it.
type Danger int

const (
	DangerNone Danger = iota
	DangerConfirm
	DangerCritical
)

// File is an opened managed file.
type File struct {
	Kind   FileKind
	Path   string
	UID    int
	GID    int
	Mode   fs.FileMode
	SHA256 string
	Raw    []byte
	Effect Effect
	// New marks a file that does not exist yet: Apply creates it with Mode,
	// UID and GID (refusing if it appeared meanwhile) and Rollback removes it.
	New bool
}

// Severity of a validation issue.
type Severity int

const (
	SevInfo Severity = iota
	SevWarn
	SevError
)

// Issue is one validation finding.
type Issue struct {
	Severity Severity
	Message  string
	Line     int
	Col      int
	Source   string // "schema", "configtest", "policy check"
}

// DiffLine is one line of a unified diff.
type DiffLine struct {
	Op   byte // ' ', '+', '-'
	Text string
	Old  int // 1-based line in old, 0 if added
	New  int // 1-based line in new, 0 if removed
}

// Change is a validated, diffed proposal to write a file.
type Change struct {
	File   *File
	Next   []byte
	Diff   []DiffLine
	Issues []Issue
	Effect Effect
	Danger Danger
}

// Valid reports whether the change has no error-severity issues.
func (c *Change) Valid() bool {
	for _, i := range c.Issues {
		if i.Severity == SevError {
			return false
		}
	}
	return true
}

// Applied records a completed write for rollback.
type Applied struct {
	File       *File // state before the write
	BackupPath string
	NewSHA256  string
	At         time.Time
}

// ServiceStatus is the service's current state.
type ServiceStatus struct {
	State   string // active, inactive, failed, activating, running, exited, unknown
	Running bool
	Since   time.Time
	PID     int
	Memory  uint64
	Version string
}

// ServiceManager controls the headscale service.
type ServiceManager interface {
	Kind() ServiceKind
	Status(ctx context.Context) (ServiceStatus, error)
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Restart(ctx context.Context) error
	Reload(ctx context.Context) error
	Configtest(ctx context.Context, path string) ([]Issue, error)
}

// LogQuery selects log lines.
type LogQuery struct {
	Follow bool
	Tail   int
	Since  time.Time
}

// LogLevel is a parsed log level.
type LogLevel int

const (
	LevelTrace LogLevel = iota
	LevelDebug
	LevelInfo
	LevelWarn
	LevelError
	LevelFatal
)

func (l LogLevel) String() string {
	return [...]string{"TRC", "DBG", "INF", "WRN", "ERR", "FTL"}[l]
}

// LogEntry is one parsed log line.
type LogEntry struct {
	Time     time.Time
	Level    LogLevel
	Msg      string
	Fields   map[string]string
	NodeRefs []uint64
	Raw      string
}

// RegistrationEntry is a pending web-auth registration from the server cache.
type RegistrationEntry struct {
	AuthID      string
	Hostname    string
	RequestedAt time.Time
}

// RouteState is the server's primary/HA view of one prefix.
type RouteState struct {
	Prefix     netip.Prefix
	Primary    uint64
	Candidates []uint64
}

// DebugClient reads headscale's local debug endpoints.
type DebugClient interface {
	Available() bool
	RegistrationCache(ctx context.Context) ([]RegistrationEntry, error)
	Routes(ctx context.Context) ([]RouteState, error)
	Ping(ctx context.Context, nodeID uint64) (time.Duration, error)
}

// MetricsSample is one scrape of /metrics.
type MetricsSample struct {
	At                time.Time
	Nodes             float64
	MapResponsesTotal float64
	HeapBytes         float64
	Goroutines        float64
}

// NodeExtras is read-only enrichment from the database.
type NodeExtras struct {
	OS            string
	OSVersion     string
	ClientVersion string
	Hostname      string
	PreferredDERP int
	DERPLatency   map[string]float64 // region key -> ms
	Endpoints     []netip.AddrPort
	RequestTags   []string
}

// Errors.
var (
	ErrUnsupportedShape = errors.New("unrecognised response format from headscale")
	ErrNoHost           = errors.New("host mode is not available")
)

// ErrChangedOnDisk means the file changed since it was opened.
type ErrChangedOnDisk struct {
	Path    string
	Current []byte
}

func (e *ErrChangedOnDisk) Error() string {
	return fmt.Sprintf("%s changed on disk since it was opened", e.Path)
}

// ErrInvalid means validation failed; nothing was written.
type ErrInvalid struct{ Issues []Issue }

func (e *ErrInvalid) Error() string {
	for _, i := range e.Issues {
		if i.Severity == SevError {
			if i.Line > 0 {
				return fmt.Sprintf("invalid: line %d: %s", i.Line, i.Message)
			}
			return "invalid: " + i.Message
		}
	}
	return "invalid"
}

// ErrPermission means the process lacks a right; FixCommand says how to get it.
type ErrPermission struct {
	Need       Need
	Op         string
	FixCommand string
}

func (e *ErrPermission) Error() string {
	if e.FixCommand != "" {
		return fmt.Sprintf("permission denied: %s (try: %s)", e.Op, e.FixCommand)
	}
	return "permission denied: " + e.Op
}

// ErrInsecureBackupDir means the backup root is not private to this user.
type ErrInsecureBackupDir struct {
	Path       string
	Mode       fs.FileMode
	Owner      int
	FixCommand string
}

func (e *ErrInsecureBackupDir) Error() string {
	return fmt.Sprintf("backup directory %s is not private (mode %v, owner %d); try: %s", e.Path, e.Mode, e.Owner, e.FixCommand)
}
