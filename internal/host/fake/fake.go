// Package fake is an in-memory host.Host for tests and `--demo --host`.
// Files, service, logs, debug data, metrics and database enrichment are all
// simulated deterministically, with injectable failures: permission errors,
// changed-on-disk conflicts, configtest failures and an unhealthy restart.
package fake

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/tonhe/fathom/internal/brand"
	"github.com/tonhe/fathom/internal/host"
	"github.com/tonhe/fathom/internal/host/files"
	"github.com/tonhe/fathom/internal/host/hsconfig"
	"github.com/tonhe/fathom/internal/host/local"
	"github.com/tonhe/fathom/internal/host/logs"
)

var (
	//go:embed seed/config.yaml
	SeedConfig []byte
	//go:embed seed/policy.hujson
	SeedPolicy []byte
	//go:embed seed/extra-records.json
	SeedRecords []byte
)

// Seed paths.
const (
	ConfigPath  = "/etc/headscale/config.yaml"
	PolicyPath  = "/etc/headscale/policy.hujson"
	RecordsPath = "/var/lib/headscale/extra-records.json"
)

// HAPrefix is the prefix the seed's HA router pair advertises.
var HAPrefix = netip.MustParsePrefix("10.0.0.0/24")

type memFile struct {
	data     []byte
	mode     fs.FileMode
	uid, gid int
}

// Host is the fake.
type Host struct {
	mu        sync.Mutex
	env       host.Env
	files     map[string]*memFile
	backups   map[string][]byte
	nodes     []uint64
	haPrimary uint64
	haStandby uint64
	now       func() time.Time
	logEvery  time.Duration

	permFail       map[host.Need]string // need -> fix command
	configtestFail []host.Issue
	unhealthy      bool
	metricsTick    int

	svc *Service
	dbg *Debug
}

var _ host.Host = (*Host)(nil)

// Option configures New.
type Option func(*Host)

// WithEnv replaces the environment (Perms included).
func WithEnv(e host.Env) Option { return func(h *Host) { h.env = e } }

// WithPerms replaces Env.Perms.
func WithPerms(p host.Perms) Option { return func(h *Host) { h.env.Perms = p } }

// WithNodes sets the node IDs debug data, pings and NodeExtras refer to, so
// the fake host agrees with a fake backend. The first two form the HA pair
// for 10.0.0.0/24 unless WithHAPair is given.
func WithNodes(ids []uint64) Option {
	return func(h *Host) {
		h.nodes = slices.Clone(ids)
		if len(ids) >= 2 {
			h.haPrimary, h.haStandby = ids[0], ids[1]
		} else if len(ids) == 1 {
			h.haPrimary, h.haStandby = ids[0], 0
		}
	}
}

// WithHAPair sets the primary and standby router for 10.0.0.0/24.
func WithHAPair(primary, standby uint64) Option {
	return func(h *Host) { h.haPrimary, h.haStandby = primary, standby }
}

// WithFile adds or replaces an in-memory file (mode 0644, root-owned).
func WithFile(path string, data []byte) Option {
	return func(h *Host) { h.files[path] = &memFile{data: slices.Clone(data), mode: 0o644} }
}

// WithClock sets the time source.
func WithClock(now func() time.Time) Option { return func(h *Host) { h.now = now } }

// WithLogInterval sets how often a following log stream emits (default 1s).
func WithLogInterval(d time.Duration) Option { return func(h *Host) { h.logEvery = d } }

// DefaultEnv is a healthy systemd deb install of 0.30 with every permission.
func DefaultEnv() host.Env {
	ok := host.Perm{OK: true}
	return host.Env{
		Hostname: "hs-demo", ConfigPath: ConfigPath, Version: "0.30.0",
		Transport: host.TransportUnixHTTP, SocketPath: "/var/run/headscale/headscale.sock",
		Service: host.ServiceSystemd, LogSource: host.LogJournald,
		DB: host.DBSQLite, DBPath: "/var/lib/headscale/db.sqlite",
		DebugAddr: "127.0.0.1:9090", ServerURL: "https://hs.demo.example.com",
		PolicyMode: "file", PolicyPath: PolicyPath, RecordsPath: RecordsPath,
		Perms: host.Perms{
			host.NeedSocket: ok, host.NeedConfigR: ok, host.NeedConfigW: ok, host.NeedPolicyW: ok,
			host.NeedRecordsW: ok, host.NeedService: ok, host.NeedLogs: ok, host.NeedDebug: ok, host.NeedDBRead: ok,
		},
		Notes: []string{"simulated host (" + brand.Name + " --demo --host)"},
	}
}

// New returns a fake host with the seed files and nodes 1-5.
func New(opts ...Option) *Host {
	h := &Host{
		env:      DefaultEnv(),
		files:    map[string]*memFile{},
		backups:  map[string][]byte{},
		now:      time.Now,
		logEvery: time.Second,
		permFail: map[host.Need]string{},
	}
	h.files[ConfigPath] = &memFile{data: slices.Clone(SeedConfig), mode: 0o644}
	h.files[PolicyPath] = &memFile{data: slices.Clone(SeedPolicy), mode: 0o640, gid: 998}
	h.files[RecordsPath] = &memFile{data: slices.Clone(SeedRecords), mode: 0o640, uid: 998, gid: 998}
	WithNodes([]uint64{1, 2, 3, 4, 5})(h)
	for _, o := range opts {
		o(h)
	}
	h.svc = &Service{h: h, state: "active", since: h.now().Add(-26 * time.Hour), pid: 812}
	h.dbg = &Debug{h: h}
	return h
}

// Demo is the host for `--demo --host`.
func Demo() *Host { return New(WithLogInterval(700 * time.Millisecond)) }

// --- injection --------------------------------------------------------------

// FailPermission makes operations needing n return *host.ErrPermission with fix.
// An empty fix uses a sensible default.
func (h *Host) FailPermission(n host.Need, fix string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if fix == "" {
		fix = "sudo " + brand.Name
		if n == host.NeedService {
			fix = "sudo systemctl restart headscale"
		}
	}
	h.permFail[n] = fix
	p := h.env.Perms[n]
	p.OK, p.Reason, p.FixCommand = false, "simulated permission failure", fix
	if h.env.Perms == nil {
		h.env.Perms = host.Perms{}
	}
	h.env.Perms[n] = p
}

// ClearFailures removes every injected failure.
func (h *Host) ClearFailures() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for n := range h.permFail {
		h.env.Perms[n] = host.Perm{OK: true}
	}
	h.permFail = map[host.Need]string{}
	h.configtestFail = nil
	h.unhealthy = false
}

// ChangeOnDisk edits a file behind the caller's back, so an Apply of a
// change planned earlier fails with ErrChangedOnDisk.
func (h *Host) ChangeOnDisk(path string, data []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if f, ok := h.files[path]; ok {
		f.data = slices.Clone(data)
		return
	}
	h.files[path] = &memFile{data: slices.Clone(data), mode: 0o644}
}

// SetConfigtestFailure makes Configtest and config Plans report issues
// (nil clears). Issues default to Error severity from "configtest".
func (h *Host) SetConfigtestFailure(issues ...host.Issue) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.configtestFail = nil
	for _, i := range issues {
		if i.Source == "" {
			i.Source = "configtest"
		}
		h.configtestFail = append(h.configtestFail, i)
	}
}

// SetUnhealthyRestart makes the next restarts leave the service failed.
func (h *Host) SetUnhealthyRestart(on bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.unhealthy = on
}

// File returns a copy of an in-memory file.
func (h *Host) File(path string) ([]byte, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	f, ok := h.files[path]
	if !ok {
		return nil, false
	}
	return slices.Clone(f.data), true
}

// Backups returns how many backups were taken.
func (h *Host) Backups() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.backups)
}

// Nodes returns the node IDs in use.
func (h *Host) Nodes() []uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.nodes)
}

func (h *Host) permErr(n host.Need, op string) error {
	if fix, ok := h.permFail[n]; ok {
		return &host.ErrPermission{Need: n, Op: op, FixCommand: fix}
	}
	return nil
}

// --- host.Host ----------------------------------------------------------------

func (h *Host) Env() host.Env {
	h.mu.Lock()
	defer h.mu.Unlock()
	e := h.env
	e.Perms = host.Perms{}
	for k, v := range h.env.Perms {
		e.Perms[k] = v
	}
	e.Notes = slices.Clone(h.env.Notes)
	return e
}

func needW(k host.FileKind) host.Need {
	switch k {
	case host.FilePolicy:
		return host.NeedPolicyW
	case host.FileExtraRecords:
		return host.NeedRecordsW
	}
	return host.NeedConfigW
}

func (h *Host) defaultPath(k host.FileKind) string {
	switch k {
	case host.FileConfig:
		return h.env.ConfigPath
	case host.FilePolicy:
		if h.env.PolicyMode == "database" {
			return ""
		}
		return h.env.PolicyPath
	case host.FileExtraRecords:
		return h.env.RecordsPath
	}
	return ""
}

func (h *Host) Open(kind host.FileKind, path string) (*host.File, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if path == "" {
		path = h.defaultPath(kind)
	}
	if path == "" {
		return nil, fmt.Errorf("%w: no %s file configured", host.ErrNoHost, kind)
	}
	if err := h.permErr(host.NeedConfigR, "read "+path); err != nil && kind == host.FileConfig {
		return nil, err
	}
	f, ok := h.files[path]
	if !ok {
		return nil, fmt.Errorf("open %s: %w", path, fs.ErrNotExist)
	}
	return &host.File{Kind: kind, Path: path, UID: f.uid, GID: f.gid, Mode: f.mode,
		SHA256: files.Sum(f.data), Raw: slices.Clone(f.data), Effect: files.DefaultEffect(kind)}, nil
}

func (h *Host) Plan(ctx context.Context, f *host.File, next []byte) (*host.Change, error) {
	if f == nil {
		return nil, errors.New("plan: nil file")
	}
	c := &host.Change{File: f, Next: slices.Clone(next), Effect: f.Effect, Diff: files.Diff(f.Raw, next), Danger: host.DangerConfirm}
	switch f.Kind {
	case host.FileConfig:
		h.mu.Lock()
		ver := h.env.Version
		h.mu.Unlock()
		c.Issues = local.ConfigSchema(next, ver)
		c.Danger = max(hsconfig.Danger(f.Raw, next), host.DangerConfirm)
		h.mu.Lock()
		c.Issues = append(c.Issues, h.configtestFail...)
		h.mu.Unlock()
	case host.FilePolicy:
		c.Issues = local.PolicySchema(next)
	case host.FileExtraRecords:
		c.Issues = local.RecordsSchema(next)
		c.Danger = host.DangerNone
	case host.FileDERPMap:
		c.Issues = local.DERPMapSchema(next)
	}
	return c, nil
}

func (h *Host) Apply(ctx context.Context, c *host.Change) (host.Applied, error) {
	if c == nil || c.File == nil {
		return host.Applied{}, errors.New("apply: nil change")
	}
	if !c.Valid() {
		return host.Applied{}, &host.ErrInvalid{Issues: c.Issues}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	f := c.File
	if err := h.permErr(needW(f.Kind), "write "+f.Path); err != nil {
		return host.Applied{}, err
	}
	cur, ok := h.files[f.Path]
	if !ok {
		return host.Applied{}, fmt.Errorf("apply %s: %w", f.Path, fs.ErrNotExist)
	}
	if files.Sum(cur.data) != f.SHA256 {
		return host.Applied{}, &host.ErrChangedOnDisk{Path: f.Path, Current: slices.Clone(cur.data)}
	}
	at := h.now()
	bp := fmt.Sprintf("mem://backups/%s-%d%s", at.UTC().Format("20060102T150405Z"), len(h.backups), f.Path)
	h.backups[bp] = slices.Clone(cur.data)
	cur.data = slices.Clone(c.Next)
	return host.Applied{File: f, BackupPath: bp, NewSHA256: files.Sum(c.Next), At: at}, nil
}

func (h *Host) Rollback(ctx context.Context, a host.Applied) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if a.File == nil {
		return errors.New("rollback: nothing recorded")
	}
	if err := h.permErr(needW(a.File.Kind), "write "+a.File.Path); err != nil {
		return err
	}
	old, ok := h.backups[a.BackupPath]
	if !ok {
		return fmt.Errorf("rollback: backup %s not found", a.BackupPath)
	}
	cur := h.files[a.File.Path]
	if cur == nil {
		return fs.ErrNotExist
	}
	if files.Sum(cur.data) != a.NewSHA256 {
		return &host.ErrChangedOnDisk{Path: a.File.Path, Current: slices.Clone(cur.data)}
	}
	h.backups[a.BackupPath+".rollback"] = slices.Clone(cur.data)
	cur.data = slices.Clone(old)
	return nil
}

func (h *Host) Service() host.ServiceManager { return h.svc }

func (h *Host) Debug() host.DebugClient { return h.dbg }

func (h *Host) Metrics(ctx context.Context) (host.MetricsSample, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.permErr(host.NeedDebug, "scrape /metrics"); err != nil {
		return host.MetricsSample{}, err
	}
	if !h.svc.runningLocked() {
		return host.MetricsSample{}, errors.New("dial tcp " + h.env.DebugAddr + ": connection refused")
	}
	h.metricsTick++
	t := float64(h.metricsTick)
	n := float64(len(h.nodes))
	return host.MetricsSample{
		At:                h.now(),
		Nodes:             n,
		MapResponsesTotal: 1000 + t*n*3,
		HeapBytes:         24e6 + float64(h.metricsTick%10)*1.5e5,
		Goroutines:        80 + float64(h.metricsTick%7),
	}, nil
}

var (
	oses     = []struct{ os, ver string }{{"linux", "Debian 13"}, {"macOS", "15.4"}, {"windows", "11 24H2"}, {"iOS", "18.5"}, {"android", "15"}, {"linux", "Ubuntu 24.04"}}
	versions = []string{"1.90.4", "1.88.2", "1.90.1", "1.86.0"}
)

// NodeExtras is deterministic by ID; any ID gets data.
func (h *Host) NodeExtras(ctx context.Context, ids []uint64) (map[uint64]host.NodeExtras, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.permErr(host.NeedDBRead, "read the headscale database"); err != nil {
		return nil, err
	}
	out := make(map[uint64]host.NodeExtras, len(ids))
	for _, id := range ids {
		o := oses[id%uint64(len(oses))]
		region := int(id%3) + 1
		out[id] = host.NodeExtras{
			OS: o.os, OSVersion: o.ver,
			ClientVersion: versions[id%uint64(len(versions))],
			Hostname:      fmt.Sprintf("node-%d", id),
			PreferredDERP: region,
			DERPLatency: map[string]float64{
				fmt.Sprintf("%d-v4", region):         float64(8 + id%5),
				fmt.Sprintf("%d-v4", region%3+1):     float64(40 + id%17),
				fmt.Sprintf("%d-v4", (region+1)%3+1): float64(95 + id%23),
			},
			Endpoints: []netip.AddrPort{
				netip.AddrPortFrom(netip.AddrFrom4([4]byte{203, 0, 113, byte(id)}), 41641),
				netip.AddrPortFrom(netip.AddrFrom4([4]byte{192, 168, 1, byte(10 + id)}), 41641),
			},
		}
		if id == h.haPrimary || id == h.haStandby {
			x := out[id]
			x.RequestTags = []string{"tag:router"}
			out[id] = x
		}
	}
	return out, nil
}

// --- logs -------------------------------------------------------------------

var logTemplates = []struct {
	lvl string
	msg string
	kv  string
}{
	{"INF", "Node connected", "node.id=%d"},
	{"DBG", "Map response sent", "node.id=%d type=patch"},
	{"INF", "Sending keepalive", "node.id=%d"},
	{"DBG", "Map request received", "node.id=%d omitPeers=false stream=true"},
	{"WRN", "Node did not respond to HA probe", "node.id=%d prefix=10.0.0.0/24"},
	{"INF", "Node disconnected", "node.id=%d"},
	{"INF", "Policy reloaded", "path=/etc/headscale/policy.hujson"},
	{"ERR", "Failed to send map response", "node.id=%d error=\"write: broken pipe\""},
}

func (h *Host) line(seq int, at time.Time) string {
	h.mu.Lock()
	nodes := h.nodes
	h.mu.Unlock()
	t := logTemplates[seq%len(logTemplates)]
	if seq%len(logTemplates) == 7 && seq%3 != 0 {
		t = logTemplates[1] // keep errors rare
	}
	id := uint64(seq + 1)
	if len(nodes) > 0 {
		id = nodes[seq%len(nodes)]
	}
	kv := t.kv
	if strings.Contains(kv, "%d") {
		kv = fmt.Sprintf(kv, id)
	}
	return at.UTC().Format(time.RFC3339) + " " + t.lvl + " " + t.msg + " " + kv
}

func (h *Host) Logs(ctx context.Context, q host.LogQuery) (<-chan host.LogEntry, error) {
	h.mu.Lock()
	err := h.permErr(host.NeedLogs, "read the headscale journal")
	every := h.logEvery
	now := h.now()
	h.mu.Unlock()
	if err != nil {
		return nil, err
	}
	n := q.Tail
	if n <= 0 {
		n = logs.DefaultTail
	}
	n = min(n, 200)
	ch := make(chan host.LogEntry, 64)
	go func() {
		defer close(ch)
		send := func(s string) bool {
			e := logs.ParseLine(s)
			if !q.Since.IsZero() && e.Time.Before(q.Since) {
				return true
			}
			select {
			case ch <- e:
				return true
			case <-ctx.Done():
				return false
			}
		}
		for i := 0; i < n; i++ {
			if !send(h.line(i, now.Add(-time.Duration(n-i)*7*time.Second))) {
				return
			}
		}
		if !q.Follow {
			return
		}
		tk := time.NewTicker(every)
		defer tk.Stop()
		for seq := n; ; seq++ {
			select {
			case <-ctx.Done():
				return
			case <-tk.C:
				h.mu.Lock()
				t := h.now()
				h.mu.Unlock()
				if !send(h.line(seq, t)) {
					return
				}
			}
		}
	}()
	return ch, nil
}

// --- service ----------------------------------------------------------------

// Service is the fake service state machine:
// active <-> inactive, restart -> active | failed (when unhealthy).
type Service struct {
	h     *Host
	state string
	since time.Time
	pid   int
}

func (s *Service) runningLocked() bool { return s.state == "active" }

func (s *Service) Kind() host.ServiceKind {
	s.h.mu.Lock()
	defer s.h.mu.Unlock()
	return s.h.env.Service
}

func (s *Service) Status(ctx context.Context) (host.ServiceStatus, error) {
	s.h.mu.Lock()
	defer s.h.mu.Unlock()
	st := host.ServiceStatus{State: s.state, Running: s.state == "active", Version: s.h.env.Version}
	if st.Running {
		st.Since, st.PID, st.Memory = s.since, s.pid, 48<<20+uint64(s.h.metricsTick%10)<<18
	}
	return st, nil
}

func (s *Service) transition(verb string, fn func()) error {
	s.h.mu.Lock()
	defer s.h.mu.Unlock()
	if err := s.h.permErr(host.NeedService, "systemctl "+verb+" headscale.service"); err != nil {
		if pe, ok := err.(*host.ErrPermission); ok && pe.FixCommand == "sudo systemctl restart headscale" {
			pe.FixCommand = "sudo systemctl " + verb + " headscale"
		}
		return err
	}
	fn()
	return nil
}

func (s *Service) start() {
	if s.h.unhealthy {
		s.state = "failed"
		return
	}
	s.state, s.since, s.pid = "active", s.h.now(), s.pid+17
}

func (s *Service) Start(ctx context.Context) error {
	return s.transition("start", func() {
		if s.state != "active" {
			s.start()
		}
	})
}

func (s *Service) Stop(ctx context.Context) error {
	return s.transition("stop", func() { s.state = "inactive" })
}

func (s *Service) Restart(ctx context.Context) error {
	return s.transition("restart", s.start)
}

func (s *Service) Reload(ctx context.Context) error {
	var err error
	e := s.transition("reload", func() {
		if s.state != "active" {
			err = errors.New("headscale.service is not active, cannot reload")
		}
	})
	if e != nil {
		return e
	}
	return err
}

// Configtest checks the in-memory file at path plus any injected failure.
func (s *Service) Configtest(ctx context.Context, path string) ([]host.Issue, error) {
	s.h.mu.Lock()
	f, ok := s.h.files[path]
	inj := slices.Clone(s.h.configtestFail)
	ver := s.h.env.Version
	s.h.mu.Unlock()
	var issues []host.Issue
	if ok {
		for _, i := range local.ConfigSchema(f.data, ver) {
			i.Source = "configtest"
			issues = append(issues, i)
		}
	}
	return append(issues, inj...), nil
}

// --- debug ------------------------------------------------------------------

// Debug serves fake /debug data.
type Debug struct{ h *Host }

func (d *Debug) Available() bool {
	d.h.mu.Lock()
	defer d.h.mu.Unlock()
	return d.h.env.DebugAddr != "" && d.h.permFail[host.NeedDebug] == "" && d.h.svc.runningLocked()
}

func (d *Debug) RegistrationCache(ctx context.Context) ([]host.RegistrationEntry, error) {
	if !d.Available() {
		return nil, host.ErrNoHost
	}
	now := d.h.now()
	return []host.RegistrationEntry{
		{AuthID: "hskey-authreq-7Qm2xK9pLw4sVt1ZcY8bN3eR", Hostname: "new-laptop", RequestedAt: now.Add(-2 * time.Minute)},
		{AuthID: "hskey-authreq-Jd5fH2aGk8Lp0Qx6Wv3Ys9Tu", Hostname: "pi-garage", RequestedAt: now.Add(-35 * time.Second)},
	}, nil
}

func (d *Debug) Routes(ctx context.Context) ([]host.RouteState, error) {
	if !d.Available() {
		return nil, host.ErrNoHost
	}
	d.h.mu.Lock()
	defer d.h.mu.Unlock()
	if d.h.haPrimary == 0 {
		return []host.RouteState{}, nil
	}
	r := host.RouteState{Prefix: HAPrefix, Primary: d.h.haPrimary, Candidates: []uint64{d.h.haPrimary}}
	if d.h.haStandby != 0 {
		r.Candidates = append(r.Candidates, d.h.haStandby)
	}
	return []host.RouteState{r}, nil
}

func (d *Debug) Ping(ctx context.Context, id uint64) (time.Duration, error) {
	if !d.Available() {
		return 0, host.ErrNoHost
	}
	d.h.mu.Lock()
	known := slices.Contains(d.h.nodes, id)
	d.h.mu.Unlock()
	if !known {
		return 0, fmt.Errorf("ping failed: node %d is not connected", id)
	}
	return time.Duration(8+(id*7)%40) * time.Millisecond, nil
}
