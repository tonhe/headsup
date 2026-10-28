// Package detect inspects the machine headscale runs on: config location,
// version, local transport, service manager, log source, database and the
// permissions this process has for each host action. Every OS access is
// injectable so detection is tested with fixture trees and fakes.
package detect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tonhe/fathom/internal/brand"
	"github.com/tonhe/fathom/internal/host"
	"github.com/tonhe/fathom/internal/host/files"
	"github.com/tonhe/fathom/internal/host/hsconfig"
	"github.com/tonhe/fathom/internal/host/service"
)

// FileInfo is the subset of stat(2) detection needs.
type FileInfo struct {
	Mode fs.FileMode
	UID  int
	GID  int
}

// Options are detection inputs. Use Defaults() for the real machine.
type Options struct {
	Root          string                           // filesystem root; "" means "/"
	FS            fs.FS                            // file reads; default os.DirFS(Root)
	Stat          func(p string) (FileInfo, error) // owner and mode; default os.Stat under Root
	Run           service.RunFunc                  // command runner; default service.Exec
	Getenv        func(string) string              // default os.Getenv
	Uid           int                              // effective uid
	Gids          []int                            // primary and supplementary gids
	Hostname      string                           // default os.Hostname()
	ConfigFlag    string                           // --hs-config
	ContainerHint string                           // container name to prefer
	Dial          func(network, addr string) error // reachability probe; default net.DialTimeout
	SocketHTTP    func(socket string) bool         // does the socket speak HTTP? nil = probe via Dial only for >= 0.30
	Cwd           string                           // for ./config.yaml; default os.Getwd()
	Timeout       time.Duration                    // per command; default 3s
	Context       context.Context                  // default Background

	extraNotes []string
}

// Defaults returns Options for the current process.
func Defaults() Options {
	g, _ := os.Getgroups()
	cwd, _ := os.Getwd()
	h, _ := os.Hostname()
	return Options{
		Uid:      os.Getuid(),
		Gids:     append([]int{os.Getgid()}, g...),
		Hostname: h,
		Cwd:      cwd,
		SocketHTTP: func(sock string) bool {
			return unixSpeaksHTTP(sock)
		},
	}
}

func (o *Options) fill() {
	if o.Root == "" {
		o.Root = "/"
	}
	if o.FS == nil {
		o.FS = os.DirFS(o.Root)
	}
	if o.Stat == nil {
		root := o.Root
		o.Stat = func(p string) (FileInfo, error) {
			fi, err := os.Stat(filepath.Join(root, p))
			if err != nil {
				return FileInfo{}, err
			}
			uid, gid, _ := files.Owner(fi)
			return FileInfo{Mode: fi.Mode(), UID: uid, GID: gid}, nil
		}
	}
	if o.Run == nil {
		o.Run = service.Exec
	}
	if o.Getenv == nil {
		o.Getenv = os.Getenv
	}
	if o.Dial == nil {
		o.Dial = func(network, addr string) error {
			c, err := net.DialTimeout(network, addr, time.Second)
			if err == nil {
				c.Close()
			}
			return err
		}
	}
	if o.Timeout <= 0 {
		o.Timeout = 3 * time.Second
	}
	if o.Context == nil {
		o.Context = context.Background()
	}
}

func (o *Options) run(name string, args ...string) (service.Result, error) {
	ctx, cancel := context.WithTimeout(o.Context, o.Timeout)
	defer cancel()
	return o.Run(ctx, name, args...)
}

func (o *Options) read(p string) ([]byte, error) {
	return fs.ReadFile(o.FS, strings.TrimPrefix(path.Clean(p), "/"))
}

func (o *Options) exists(p string) bool {
	_, err := o.Stat(p)
	return err == nil
}

func (o *Options) ident() files.Ident { return files.Ident{UID: o.Uid, GIDs: o.Gids} }

// Config search order (FR-050, R15).
func (o *Options) configCandidates() []string {
	var c []string
	if o.ConfigFlag != "" {
		return []string{o.ConfigFlag}
	}
	if e := o.Getenv("HEADSCALE_CONFIG"); e != "" {
		return []string{e}
	}
	c = append(c, "/etc/headscale/config.yaml", "/etc/headscale/config.yml")
	if home := o.Getenv("HOME"); home != "" {
		c = append(c, path.Join(home, ".headscale", "config.yaml"), path.Join(home, ".headscale", "config.yml"))
	}
	if o.Cwd != "" {
		c = append(c, path.Join(o.Cwd, "config.yaml"), path.Join(o.Cwd, "config.yml"))
	}
	return c
}

// Config is the part of config.yaml detection reads.
type Config struct {
	UnixSocket   string
	ServerURL    string
	MetricsAddr  string
	MetricsSet   bool
	DBType       string
	SQLitePath   string
	PostgresDSN  string
	PolicyMode   string
	PolicyPath   string
	RecordsPath  string
	DERPPaths    []string
	LogFormat    string
	LogLevel     string
	TLSFlatForm  bool
	GRPCListen   string
	ParseProblem string
}

// ParseConfig extracts what detection needs from config.yaml.
func ParseConfig(b []byte) Config {
	c := Config{UnixSocket: "/var/run/headscale/headscale.sock", MetricsAddr: "127.0.0.1:9090", DBType: "sqlite", PolicyMode: "file"}
	d, err := hsconfig.Parse(b)
	if err != nil {
		c.ParseProblem = err.Error()
		return c
	}
	get := func(k string, dst *string) {
		if v, ok := d.Get(k); ok && v != "" {
			*dst = v
		}
	}
	get("unix_socket", &c.UnixSocket)
	get("server_url", &c.ServerURL)
	if v, ok := d.Get("metrics_listen_addr"); ok {
		c.MetricsAddr, c.MetricsSet = v, true
	}
	get("database.type", &c.DBType)
	if c.DBType == "sqlite3" {
		c.DBType = "sqlite"
	}
	get("database.sqlite.path", &c.SQLitePath)
	if c.DBType == "sqlite" && c.SQLitePath == "" {
		c.SQLitePath = "/var/lib/headscale/db.sqlite"
	}
	if c.DBType == "postgres" {
		h, _ := d.Get("database.postgres.host")
		p, _ := d.Get("database.postgres.port")
		n, _ := d.Get("database.postgres.name")
		u, _ := d.Get("database.postgres.user")
		ssl, _ := d.Get("database.postgres.ssl")
		parts := []string{}
		for _, kv := range [][2]string{{"host", h}, {"port", p}, {"dbname", n}, {"user", u}} {
			if kv[1] != "" {
				parts = append(parts, kv[0]+"="+kv[1])
			}
		}
		switch ssl {
		case "", "false":
			parts = append(parts, "sslmode=disable")
		case "true":
			parts = append(parts, "sslmode=require")
		default:
			parts = append(parts, "sslmode="+ssl)
		}
		c.PostgresDSN = strings.Join(parts, " ") // the password is deliberately omitted
	}
	get("policy.mode", &c.PolicyMode)
	get("policy.path", &c.PolicyPath)
	get("dns.extra_records_path", &c.RecordsPath)
	c.DERPPaths, _ = d.List("derp.paths")
	get("log.format", &c.LogFormat)
	get("log.level", &c.LogLevel)
	get("grpc_listen_addr", &c.GRPCListen)
	c.TLSFlatForm = d.Has("tls_cert_path") || d.Has("tls_letsencrypt_hostname") || d.Has("acme_url")
	return c
}

var versionRE = regexp.MustCompile(`v?(\d+)\.(\d+)\.(\d+)(-[0-9A-Za-z.\-]+)?`)

// ParseVersion extracts "0.29.6" (or "0.30.0-beta.1") from `headscale version` output.
func ParseVersion(out string) string {
	m := versionRE.FindStringSubmatch(out)
	if m == nil {
		return ""
	}
	return m[1] + "." + m[2] + "." + m[3] + m[4]
}

// AtLeast reports whether version v >= major.minor. Unknown versions are false.
func AtLeast(v string, major, minor int) bool {
	m := versionRE.FindStringSubmatch(v)
	if m == nil {
		return false
	}
	ma, _ := strconv.Atoi(m[1])
	mi, _ := strconv.Atoi(m[2])
	return ma > major || (ma == major && mi >= minor)
}

type groupDB struct {
	byName map[string]int
	byGID  map[int]string
}

func (o *Options) groups() groupDB {
	g := groupDB{byName: map[string]int{}, byGID: map[int]string{}}
	b, err := o.read("/etc/group")
	if err != nil {
		return g
	}
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Split(l, ":")
		if len(f) < 3 || strings.HasPrefix(l, "#") {
			continue
		}
		if id, err := strconv.Atoi(f[2]); err == nil {
			g.byName[f[0]] = id
			g.byGID[id] = f[0]
		}
	}
	return g
}

func groupFix(g string) string { return "sudo usermod -aG " + g + " $USER && newgrp " + g }

func rootFix() string { return "sudo " + brand.Name }

type container struct {
	runtime, name string
	mounts        []mount
}

type mount struct{ Source, Destination string }

// toHost maps an in-container path to the host via bind mounts.
func (c *container) toHost(p string) (string, bool) {
	best := mount{}
	for _, m := range c.mounts {
		if (p == m.Destination || strings.HasPrefix(p, strings.TrimSuffix(m.Destination, "/")+"/")) && len(m.Destination) > len(best.Destination) {
			best = m
		}
	}
	if best.Destination == "" {
		return "", false
	}
	return path.Join(best.Source, strings.TrimPrefix(p, best.Destination)), true
}

func (o *Options) findContainer() (*container, []string) {
	var notes []string
	for _, rt := range []string{"docker", "podman"} {
		res, err := o.run(rt, "ps", "--format", "{{.Names}}\t{{.Image}}\t{{.Labels}}")
		if err != nil || res.Exit != 0 {
			if err == nil && strings.Contains(strings.ToLower(res.Output()), "permission denied") {
				notes = append(notes, rt+" is installed but its socket is not accessible")
			}
			continue
		}
		var pick string
		for _, l := range strings.Split(strings.TrimSpace(string(res.Stdout)), "\n") {
			f := strings.SplitN(l, "\t", 3)
			if len(f) == 0 || f[0] == "" {
				continue
			}
			name := f[0]
			if o.ContainerHint != "" {
				if name == o.ContainerHint {
					pick = name
					break
				}
				continue
			}
			rest := strings.ToLower(strings.Join(f[1:], " "))
			if strings.Contains(strings.ToLower(name), "headscale") || strings.Contains(rest, "headscale") {
				if pick == "" {
					pick = name
				}
			}
		}
		if pick == "" {
			continue
		}
		c := &container{runtime: rt, name: pick}
		if res, err := o.run(rt, "inspect", "--format", "{{json .Mounts}}", pick); err == nil && res.Exit == 0 {
			_ = json.Unmarshal(res.Stdout, &c.mounts)
		}
		return c, notes
	}
	return nil, notes
}

// Detect inspects the machine.
func Detect(opts Options) host.Env {
	o := &opts
	o.fill()
	env := host.Env{Hostname: o.Hostname, Perms: host.Perms{}}
	if env.Hostname == "" {
		env.Hostname, _ = os.Hostname()
	}
	note := func(f string, a ...any) { env.Notes = append(env.Notes, fmt.Sprintf(f, a...)) }

	// Host binary and config.
	hostVersion := ""
	if res, err := o.run("headscale", "version"); err == nil && res.Exit == 0 {
		hostVersion = ParseVersion(res.Output())
	}
	cfgPath := ""
	for _, c := range o.configCandidates() {
		if o.exists(c) {
			cfgPath = c
			break
		}
	}
	if (o.ConfigFlag != "" || o.Getenv("HEADSCALE_CONFIG") != "") && cfgPath == "" {
		note("config %s (from %s) does not exist", o.configCandidates()[0], map[bool]string{true: "--hs-config", false: "HEADSCALE_CONFIG"}[o.ConfigFlag != ""])
	}

	// Containers win when hinted, or when there is no host install.
	var ctr *container
	if o.ContainerHint != "" || hostVersion == "" || cfgPath == "" {
		var n []string
		ctr, n = o.findContainer()
		env.Notes = append(env.Notes, n...)
		if ctr != nil && hostVersion != "" && o.ContainerHint == "" {
			note("both a host headscale binary and container %q exist; using the host install", ctr.name)
			ctr = nil
		}
	}

	var cfg Config
	if ctr != nil {
		env.Container = ctr.name
		env.ContainerRuntime = ctr.runtime
		env.Transport = host.TransportContainerExec
		env.Service = host.ServiceContainer
		env.LogSource = host.LogContainer
		note("container runtime: %s", ctr.runtime)
		if res, err := o.run(ctr.runtime, "exec", ctr.name, "headscale", "version"); err == nil && res.Exit == 0 {
			env.Version = ParseVersion(res.Output())
		}
		if cfgPath == "" {
			for _, in := range []string{"/etc/headscale/config.yaml", "/etc/headscale/config.yml"} {
				if hp, ok := ctr.toHost(in); ok && o.exists(hp) {
					cfgPath = hp
					break
				}
			}
			if cfgPath == "" {
				note("container %s has no bind-mounted /etc/headscale; config editing is unavailable", ctr.name)
			}
		}
	} else {
		env.Version = hostVersion
	}
	env.ConfigPath = cfgPath
	if cfgPath != "" {
		if b, err := o.read(cfgPath); err == nil {
			cfg = ParseConfig(b)
			if cfg.ParseProblem != "" {
				note("config %s does not parse: %s", cfgPath, cfg.ParseProblem)
			}
		} else {
			cfg = ParseConfig(nil)
			if errors.Is(err, fs.ErrPermission) {
				note("config %s is not readable; using defaults for socket and metrics address", cfgPath)
			}
		}
	} else {
		cfg = ParseConfig(nil)
		note("no headscale config found (searched %s)", strings.Join(o.configCandidates(), ", "))
	}

	mapPath := func(p string) string {
		if ctr == nil || p == "" {
			return p
		}
		if hp, ok := ctr.toHost(p); ok {
			return hp
		}
		note("%s is inside container %s and not bind-mounted", p, ctr.name)
		return ""
	}
	env.SocketPath = cfg.UnixSocket
	env.ServerURL = cfg.ServerURL
	env.DebugAddr = cfg.MetricsAddr
	if cfg.MetricsSet && cfg.MetricsAddr == "" {
		note("metrics_listen_addr is empty: live server data (debug, metrics) is disabled")
	}
	env.PolicyMode = cfg.PolicyMode
	env.PolicyPath = mapPath(cfg.PolicyPath)
	env.RecordsPath = mapPath(cfg.RecordsPath)
	switch cfg.DBType {
	case "sqlite":
		env.DB = host.DBSQLite
		env.DBPath = mapPath(cfg.SQLitePath)
	case "postgres":
		env.DB = host.DBPostgres
		env.DBPath = cfg.PostgresDSN
		note("postgres password is not read from config.yaml; use PGPASSWORD or ~/.pgpass for read-only enrichment")
	}
	if env.Version != "" && AtLeast(env.Version, 0, 30) && cfg.TLSFlatForm {
		note("config uses the 0.29 flat TLS keys; 0.30 expects tls.*")
	}
	if env.Version != "" && AtLeast(env.Version, 0, 30) && cfg.GRPCListen != "" {
		note("grpc_listen_addr is set but 0.30 no longer serves gRPC")
	}

	g := o.groups()

	// Transport (host install).
	socketDialable := false
	if ctr == nil {
		socketDialable = o.Dial("unix", env.SocketPath) == nil
		switch {
		case env.Version != "" && AtLeast(env.Version, 0, 30) && socketDialable && (o.SocketHTTP == nil || o.SocketHTTP(env.SocketPath)):
			env.Transport = host.TransportUnixHTTP
		case env.Version != "":
			env.Transport = host.TransportExecCLI
			if socketDialable && AtLeast(env.Version, 0, 30) {
				note("socket %s is reachable but does not speak HTTP; using the headscale CLI", env.SocketPath)
			}
		case socketDialable && o.SocketHTTP != nil && o.SocketHTTP(env.SocketPath):
			env.Transport = host.TransportUnixHTTP
			note("headscale binary not found; using the HTTP socket at %s", env.SocketPath)
		default:
			env.Transport = host.TransportNone
		}
	}

	// Service manager and log source.
	if ctr == nil {
		env.Service = o.serviceKind()
		switch env.Service {
		case host.ServiceSystemd:
			env.LogSource = host.LogJournald
		default:
			for _, p := range []string{"/var/log/headscale/headscale.log", "/var/log/headscale.log"} {
				if o.exists(p) {
					env.LogSource, env.LogPath = host.LogFile, p
					break
				}
			}
		}
	}

	o.perms(&env, g, ctr, socketDialable)
	env.Notes = append(env.Notes, o.extraNotes...)
	return env
}

func (o *Options) serviceKind() host.ServiceKind {
	if res, err := o.run("systemctl", "show", "headscale.service", "-p", "ActiveState,LoadState"); err == nil && res.Exit == 0 {
		kv := map[string]string{}
		for _, l := range strings.Split(string(res.Stdout), "\n") {
			if k, v, ok := strings.Cut(strings.TrimSpace(l), "="); ok {
				kv[k] = v
			}
		}
		if kv["LoadState"] == "loaded" {
			return host.ServiceSystemd
		}
	}
	if res, err := o.run("rc-service", "headscale", "status"); err == nil && (res.Exit == 0 || res.Exit == 3) &&
		!strings.Contains(strings.ToLower(res.Output()), "does not exist") {
		return host.ServiceOpenRC
	}
	if res, err := o.run("pgrep", "-x", "headscale"); err == nil && res.Exit == 0 && strings.TrimSpace(string(res.Stdout)) != "" {
		return host.ServiceProcess
	}
	return host.ServiceNone
}

func (o *Options) inGroup(g groupDB, names ...string) bool {
	for _, n := range names {
		if id, ok := g.byName[n]; ok && slices.Contains(o.Gids, id) {
			return true
		}
	}
	return false
}

// filePerm probes read or write access to p via its mode and owner.
func (o *Options) filePerm(p string, write bool, g groupDB) host.Perm {
	if p == "" {
		return host.Perm{Reason: "no path configured"}
	}
	fi, err := o.Stat(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			if write {
				dir, derr := o.Stat(path.Dir(p))
				if derr == nil && files.Allowed(o.ident(), dir.Mode, dir.UID, dir.GID, files.AccessWrite|files.AccessExec) {
					return host.Perm{OK: true}
				}
				if derr == nil {
					// The file can be created, but not by this user.
					return host.Perm{Reason: p + " does not exist and " + path.Dir(p) + " is not writable", FixCommand: rootFix()}
				}
			}
			// Elevating would not make a missing file appear, so offer no fix command.
			return host.Perm{Reason: p + " does not exist"}
		}
		return host.Perm{Reason: fmt.Sprintf("cannot stat %s: %v", p, err), FixCommand: rootFix()}
	}
	want := files.AccessRead
	verb := "read"
	if write {
		want, verb = files.AccessWrite, "write"
	}
	id := o.ident()
	if files.Allowed(id, fi.Mode, fi.UID, fi.GID, want) {
		if write {
			if dir, err := o.Stat(path.Dir(p)); err == nil && !files.Allowed(id, dir.Mode, dir.UID, dir.GID, files.AccessWrite|files.AccessExec) {
				o.extraNotes = append(o.extraNotes, fmt.Sprintf("%s is writable but its directory is not; writes fall back to in-place (not atomic)", p))
			}
		}
		return host.Perm{OK: true}
	}
	gname := g.byGID[fi.GID]
	if gname == "" {
		gname = strconv.Itoa(fi.GID)
	}
	reason := fmt.Sprintf("cannot %s %s (mode %#o, owner %d, group %s)", verb, p, uint32(fi.Mode.Perm()), fi.UID, gname)
	groupBit := fs.FileMode(0o040)
	if write {
		groupBit = 0o020
	}
	switch {
	case fi.Mode.Perm()&groupBit != 0 && fi.UID != o.Uid && g.byGID[fi.GID] != "" && !slices.Contains(o.Gids, fi.GID):
		return host.Perm{Reason: reason, FixCommand: groupFix(g.byGID[fi.GID])}
	case fi.Mode.Perm()&groupBit == 0 && slices.Contains(o.Gids, fi.GID) && fi.GID != 0:
		bit := "g+r"
		if write {
			bit = "g+w"
		}
		return host.Perm{Reason: reason, FixCommand: "sudo chmod " + bit + " " + p}
	}
	return host.Perm{Reason: reason, FixCommand: rootFix()}
}

func (o *Options) perms(env *host.Env, g groupDB, ctr *container, socketOK bool) {
	p := env.Perms
	svcStart := map[host.ServiceKind]string{
		host.ServiceSystemd: "sudo systemctl start headscale",
		host.ServiceOpenRC:  "sudo rc-service headscale start",
	}[env.Service]

	// Socket.
	switch {
	case ctr != nil:
		p[host.NeedSocket] = host.Perm{OK: true}
	case env.Transport == host.TransportExecCLI:
		// The CLI talks to the socket itself (0.29: gRPC).
		p[host.NeedSocket] = o.socketPerm(env, g, socketOK, svcStart)
		if !p[host.NeedSocket].OK && env.Version != "" {
			pm := p[host.NeedSocket]
			pm.Reason = "headscale CLI cannot reach the socket: " + pm.Reason
			p[host.NeedSocket] = pm
		}
	default:
		p[host.NeedSocket] = o.socketPerm(env, g, socketOK, svcStart)
	}

	// Files.
	if env.ConfigPath != "" {
		p[host.NeedConfigR] = o.filePerm(env.ConfigPath, false, g)
		p[host.NeedConfigW] = o.filePerm(env.ConfigPath, true, g)
	} else {
		p[host.NeedConfigR] = host.Perm{Reason: "no headscale config found"}
		p[host.NeedConfigW] = host.Perm{Reason: "no headscale config found"}
	}
	if env.PolicyMode == "file" && env.PolicyPath != "" {
		p[host.NeedPolicyW] = o.filePerm(env.PolicyPath, true, g)
	} else if env.PolicyMode == "database" {
		p[host.NeedPolicyW] = host.Perm{Reason: "policy.mode is database; edit the policy through the API"}
	}
	if env.RecordsPath != "" {
		p[host.NeedRecordsW] = o.filePerm(env.RecordsPath, true, g)
	}

	// Service.
	switch env.Service {
	case host.ServiceSystemd, host.ServiceOpenRC:
		switch {
		case o.Uid == 0:
			p[host.NeedService] = host.Perm{OK: true}
		case env.Service == host.ServiceSystemd && o.polkitAllows():
			p[host.NeedService] = host.Perm{OK: true}
			o.extraNotes = append(o.extraNotes, "a polkit rule mentions headscale.service; assuming service control is allowed")
		default:
			p[host.NeedService] = host.Perm{Reason: "controlling the headscale service needs root (or a polkit rule)", FixCommand: rootFix()}
		}
	case host.ServiceContainer:
		p[host.NeedService] = host.Perm{OK: true}
	case host.ServiceProcess:
		if o.Uid == 0 {
			p[host.NeedService] = host.Perm{OK: true}
		} else {
			p[host.NeedService] = host.Perm{Reason: "signalling a headscale process owned by another user needs root", FixCommand: rootFix()}
			if res, err := o.run("pgrep", "-x", "-u", strconv.Itoa(o.Uid), "headscale"); err == nil && res.Exit == 0 {
				p[host.NeedService] = host.Perm{OK: true}
			}
		}
	default:
		p[host.NeedService] = host.Perm{Reason: "no headscale service manager detected"}
	}

	// Logs.
	switch env.LogSource {
	case host.LogJournald:
		if o.Uid == 0 || o.inGroup(g, "systemd-journal", "adm", "wheel") {
			p[host.NeedLogs] = host.Perm{OK: true}
		} else {
			p[host.NeedLogs] = host.Perm{Reason: "reading the system journal needs the systemd-journal group", FixCommand: groupFix("systemd-journal")}
		}
	case host.LogContainer:
		p[host.NeedLogs] = host.Perm{OK: true}
	case host.LogFile:
		p[host.NeedLogs] = o.filePerm(env.LogPath, false, g)
	default:
		p[host.NeedLogs] = host.Perm{Reason: "no headscale log source detected"}
	}

	// Debug listener.
	if env.DebugAddr == "" {
		p[host.NeedDebug] = host.Perm{Reason: "metrics_listen_addr is disabled in config.yaml"}
	} else if err := o.Dial("tcp", loopback(env.DebugAddr)); err == nil {
		p[host.NeedDebug] = host.Perm{OK: true}
	} else {
		fix := svcStart
		if fix == "" && env.Service == host.ServiceContainer {
			fix = "docker port " + env.Container
		}
		p[host.NeedDebug] = host.Perm{Reason: fmt.Sprintf("metrics listener %s is not reachable (headscale stopped, or the address is not local)", env.DebugAddr), FixCommand: fix}
	}

	// Database.
	switch env.DB {
	case host.DBSQLite:
		if env.DBPath == "" {
			p[host.NeedDBRead] = host.Perm{Reason: "database file is not reachable from the host"}
			break
		}
		pm := o.filePerm(env.DBPath, false, g)
		if pm.OK {
			if dir, err := o.Stat(path.Dir(env.DBPath)); err == nil && !files.Allowed(o.ident(), dir.Mode, dir.UID, dir.GID, files.AccessExec) {
				pm = host.Perm{Reason: "cannot enter " + path.Dir(env.DBPath), FixCommand: groupFix(nonEmpty(g.byGID[dir.GID], "headscale"))}
			}
		}
		p[host.NeedDBRead] = pm
	case host.DBPostgres:
		// Not probed: needs credentials. NodeExtras reports its own error.
	}
}

func nonEmpty(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func (o *Options) socketPerm(env *host.Env, g groupDB, socketOK bool, svcStart string) host.Perm {
	if socketOK {
		return host.Perm{OK: true}
	}
	fi, err := o.Stat(env.SocketPath)
	if err != nil {
		return host.Perm{Reason: "socket " + env.SocketPath + " does not exist (is headscale running?)", FixCommand: svcStart}
	}
	if !files.Allowed(o.ident(), fi.Mode, fi.UID, fi.GID, files.AccessRead|files.AccessWrite) {
		gname := nonEmpty(g.byGID[fi.GID], "headscale")
		return host.Perm{Reason: fmt.Sprintf("no access to %s (group %s, mode %#o)", env.SocketPath, gname, uint32(fi.Mode.Perm())), FixCommand: groupFix(gname)}
	}
	return host.Perm{Reason: "socket " + env.SocketPath + " refused the connection (is headscale running?)", FixCommand: svcStart}
}

func (o *Options) polkitAllows() bool {
	for _, dir := range []string{"/etc/polkit-1/rules.d", "/usr/share/polkit-1/rules.d"} {
		ents, err := fs.ReadDir(o.FS, strings.TrimPrefix(dir, "/"))
		if err != nil {
			continue
		}
		for _, e := range ents {
			b, err := o.read(path.Join(dir, e.Name()))
			if err == nil && strings.Contains(string(b), "headscale.service") {
				return true
			}
		}
	}
	return false
}

// loopback maps a wildcard listen address to a dialable loopback one.
func loopback(addr string) string {
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	switch h {
	case "", "0.0.0.0":
		h = "127.0.0.1"
	case "::":
		h = "::1"
	}
	return net.JoinHostPort(h, p)
}

// unixSpeaksHTTP sends a minimal HTTP/1.0 request over the socket and
// checks for an HTTP status line (a gRPC-only server answers with HTTP/2
// framing or closes the connection).
func unixSpeaksHTTP(sock string) bool {
	c, err := net.DialTimeout("unix", sock, time.Second)
	if err != nil {
		return false
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := c.Write([]byte("GET /api/v1/apikey HTTP/1.0\r\nHost: localhost\r\n\r\n")); err != nil {
		return false
	}
	buf := make([]byte, 16)
	n, _ := c.Read(buf)
	return strings.HasPrefix(string(buf[:n]), "HTTP/1.")
}
