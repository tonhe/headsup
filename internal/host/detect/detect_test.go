package detect

import (
	"bufio"
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/tonhe/headsup/internal/brand"
	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/host/service"
)

func treeRoot(name string) string { return filepath.Join("..", "..", "..", "testdata", "hostfs", name) }

// fixtureStat combines the tree on disk with meta.txt's ownership overlay.
func fixtureStat(t *testing.T, name string) func(string) (FileInfo, error) {
	t.Helper()
	root := treeRoot(name)
	meta := map[string]FileInfo{}
	if f, err := os.Open(filepath.Join(root, "meta.txt")); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			l := strings.TrimSpace(sc.Text())
			if l == "" || strings.HasPrefix(l, "#") {
				continue
			}
			p := strings.Fields(l)
			mode, _ := strconv.ParseUint(p[1], 8, 32)
			uid, _ := strconv.Atoi(p[2])
			gid, _ := strconv.Atoi(p[3])
			meta[p[0]] = FileInfo{Mode: fs.FileMode(mode), UID: uid, GID: gid}
		}
		f.Close()
	}
	return func(p string) (FileInfo, error) {
		p = path.Clean(p)
		fi, err := os.Stat(filepath.Join(root, p))
		m, ok := meta[p]
		switch {
		case ok && err == nil:
			if fi.IsDir() {
				m.Mode |= fs.ModeDir
			}
			return m, nil
		case ok:
			return m, nil
		case err == nil:
			return FileInfo{Mode: fi.Mode().Perm() | (fi.Mode() & fs.ModeDir), UID: 0, GID: 0}, nil
		}
		return FileInfo{}, err
	}
}

type world struct {
	hostVersion string // "" = no headscale binary
	systemd     bool
	docker      string // container line for docker ps; "" = none
	mounts      string
	ctrVersion  string
	listening   map[string]bool // dialable addresses
	httpSocket  bool
}

func (w world) run(_ context.Context, name string, args ...string) (service.Result, error) {
	cmd := name + " " + strings.Join(args, " ")
	switch {
	case cmd == "headscale version":
		if w.hostVersion == "" {
			return service.Result{}, errors.New(`exec: "headscale": executable file not found in $PATH`)
		}
		return service.Result{Stdout: []byte(w.hostVersion + "\n")}, nil
	case strings.HasPrefix(cmd, "systemctl show headscale.service"):
		if w.systemd {
			return service.Result{Stdout: []byte("ActiveState=active\nLoadState=loaded\n")}, nil
		}
		return service.Result{Stdout: []byte("ActiveState=inactive\nLoadState=not-found\n")}, nil
	case strings.HasPrefix(cmd, "docker ps"):
		if w.docker == "" {
			return service.Result{}, errors.New("docker: not found")
		}
		return service.Result{Stdout: []byte(w.docker + "\n")}, nil
	case strings.HasPrefix(cmd, "docker inspect"):
		return service.Result{Stdout: []byte(w.mounts)}, nil
	case strings.HasPrefix(cmd, "docker exec") && strings.HasSuffix(cmd, "headscale version"):
		return service.Result{Stdout: []byte(w.ctrVersion)}, nil
	}
	return service.Result{}, errors.New(name + ": not found")
}

func (w world) dial(network, addr string) error {
	if w.listening[network+":"+addr] {
		return nil
	}
	return errors.New("connection refused")
}

func opts(t *testing.T, tree string, w world, uid int, gids ...int) Options {
	return Options{
		Root:       treeRoot(tree),
		Stat:       fixtureStat(t, tree),
		Run:        w.run,
		Getenv:     func(string) string { return "" },
		Uid:        uid,
		Gids:       gids,
		Hostname:   "hs1",
		Dial:       w.dial,
		SocketHTTP: func(string) bool { return w.httpSocket },
	}
}

var debInstall = world{
	hostVersion: "headscale version v0.30.0\ncommit: abc123",
	systemd:     true,
	listening:   map[string]bool{"unix:/var/run/headscale/headscale.sock": true, "tcp:127.0.0.1:9090": true},
	httpSocket:  true,
}

func TestDebInstall(t *testing.T) {
	member := debInstall
	outsider := debInstall
	outsider.hostVersion = "v0.29.6"
	outsider.listening = map[string]bool{"tcp:127.0.0.1:9090": true}
	rootFixCmd := "sudo " + brand.Name
	hsGroup := "sudo usermod -aG headscale $USER && newgrp headscale"

	tests := []struct {
		name      string
		w         world
		uid       int
		gids      []int
		transport host.TransportKind
		want      map[host.Need]host.Perm
	}{
		{name: "root", w: debInstall, uid: 0, gids: []int{0}, transport: host.TransportUnixHTTP, want: map[host.Need]host.Perm{
			host.NeedSocket: {OK: true}, host.NeedConfigR: {OK: true}, host.NeedConfigW: {OK: true}, host.NeedPolicyW: {OK: true},
			host.NeedRecordsW: {OK: true}, host.NeedService: {OK: true}, host.NeedLogs: {OK: true}, host.NeedDebug: {OK: true}, host.NeedDBRead: {OK: true},
		}},
		{name: "headscale group member", w: member, uid: 1000, gids: []int{1000, 998}, transport: host.TransportUnixHTTP, want: map[host.Need]host.Perm{
			host.NeedSocket:   {OK: true},
			host.NeedConfigR:  {OK: true},
			host.NeedConfigW:  {FixCommand: rootFixCmd},
			host.NeedPolicyW:  {OK: true},
			host.NeedRecordsW: {FixCommand: "sudo chmod g+w /etc/headscale/extra-records.json"},
			host.NeedService:  {FixCommand: rootFixCmd},
			host.NeedLogs:     {FixCommand: "sudo usermod -aG systemd-journal $USER && newgrp systemd-journal"},
			host.NeedDebug:    {OK: true},
			host.NeedDBRead:   {OK: true},
		}},
		{name: "outsider on 0.29", w: outsider, uid: 1001, gids: []int{1001}, transport: host.TransportExecCLI, want: map[host.Need]host.Perm{
			host.NeedSocket:   {FixCommand: hsGroup},
			host.NeedConfigR:  {OK: true},
			host.NeedConfigW:  {FixCommand: rootFixCmd},
			host.NeedPolicyW:  {FixCommand: hsGroup},
			host.NeedRecordsW: {FixCommand: rootFixCmd},
			host.NeedService:  {FixCommand: rootFixCmd},
			host.NeedDBRead:   {FixCommand: hsGroup},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := Detect(opts(t, "deb-install", tt.w, tt.uid, tt.gids...))
			if env.ConfigPath != "/etc/headscale/config.yaml" || env.Service != host.ServiceSystemd || env.LogSource != host.LogJournald {
				t.Fatalf("env = %+v", env)
			}
			if env.Transport != tt.transport {
				t.Errorf("transport = %v, want %v", env.Transport, tt.transport)
			}
			if env.SocketPath != "/var/run/headscale/headscale.sock" || env.DebugAddr != "127.0.0.1:9090" || env.ServerURL != "https://hs.example.com" {
				t.Errorf("config fields %+v", env)
			}
			if env.DB != host.DBSQLite || env.DBPath != "/var/lib/headscale/db.sqlite" || env.PolicyMode != "file" ||
				env.PolicyPath != "/etc/headscale/policy.hujson" || env.RecordsPath != "/etc/headscale/extra-records.json" {
				t.Errorf("paths %+v", env)
			}
			for need, want := range tt.want {
				got, ok := env.Perms[need]
				if !ok {
					t.Errorf("need %d not probed", need)
					continue
				}
				if got.OK != want.OK || got.FixCommand != want.FixCommand {
					t.Errorf("need %d = %+v, want OK=%v fix=%q", need, got, want.OK, want.FixCommand)
				}
				if !got.OK && got.Reason == "" {
					t.Errorf("need %d has no reason", need)
				}
			}
		})
	}
}

func TestDocker(t *testing.T) {
	w := world{
		docker:     "headscale\theadscale/headscale:0.30.0\tcom.docker.compose.project=hs",
		mounts:     `[{"Type":"bind","Source":"/srv/headscale/config","Destination":"/etc/headscale"},{"Type":"bind","Source":"/srv/headscale/data","Destination":"/var/lib/headscale"}]`,
		ctrVersion: "v0.30.0\n",
		listening:  map[string]bool{"tcp:127.0.0.1:9090": true},
	}
	env := Detect(opts(t, "docker", w, 1002, 1002, 999))
	if env.Transport != host.TransportContainerExec || env.Container != "headscale" || env.Service != host.ServiceContainer || env.LogSource != host.LogContainer {
		t.Fatalf("env = %+v", env)
	}
	if env.Version != "0.30.0" || env.ConfigPath != "/srv/headscale/config/config.yaml" || env.DBPath != "/srv/headscale/data/db.sqlite" {
		t.Fatalf("env = %+v", env)
	}
	if env.DebugAddr != "0.0.0.0:9090" || !env.Perms[host.NeedDebug].OK {
		t.Errorf("debug %q %+v", env.DebugAddr, env.Perms[host.NeedDebug])
	}
	if !env.Usable() || !env.Perms[host.NeedService].OK || !env.Perms[host.NeedDBRead].OK || env.Perms[host.NeedConfigW].OK {
		t.Errorf("perms %+v", env.Perms)
	}
	if !hasNote(env, "container runtime: docker") {
		t.Errorf("notes %v", env.Notes)
	}

	// A hint for a container that does not exist finds nothing.
	o := opts(t, "docker", w, 1002, 1002, 999)
	o.ContainerHint = "other"
	if env := Detect(o); env.Transport != host.TransportNone {
		t.Errorf("hinted missing container: %+v", env)
	}
}

func TestNone(t *testing.T) {
	env := Detect(opts(t, "none", world{}, 1001, 1001))
	if env.Transport != host.TransportNone || env.Service != host.ServiceNone || env.ConfigPath != "" || env.Usable() {
		t.Fatalf("env = %+v", env)
	}
	if !hasNote(env, "no headscale config found") {
		t.Errorf("notes %v", env.Notes)
	}
	if env.Perms[host.NeedConfigR].OK || env.Perms[host.NeedConfigR].Reason == "" {
		t.Errorf("config perm %+v", env.Perms[host.NeedConfigR])
	}
}

func hasNote(env host.Env, s string) bool {
	for _, n := range env.Notes {
		if strings.Contains(n, s) {
			return true
		}
	}
	return false
}

func TestConfigSearchOrder(t *testing.T) {
	all := fstest.MapFS{
		"flag.yaml":                     {Data: []byte("server_url: https://flag\n")},
		"env.yaml":                      {Data: []byte("server_url: https://env\n")},
		"etc/headscale/config.yml":      {Data: []byte("server_url: https://etc\n")},
		"home/u/.headscale/config.yaml": {Data: []byte("server_url: https://home\n")},
		"work/config.yaml":              {Data: []byte("server_url: https://cwd\n")},
	}
	tests := []struct {
		name   string
		flag   string
		env    string
		remove []string
		want   string
	}{
		{"flag", "/flag.yaml", "/env.yaml", nil, "https://flag"},
		{"env", "", "/env.yaml", nil, "https://env"},
		{"etc yml", "", "", nil, "https://etc"},
		{"home", "", "", []string{"etc/headscale/config.yml"}, "https://home"},
		{"cwd", "", "", []string{"etc/headscale/config.yml", "home/u/.headscale/config.yaml"}, "https://cwd"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := fstest.MapFS{}
			for k, v := range all {
				m[k] = v
			}
			for _, r := range tt.remove {
				delete(m, r)
			}
			o := Options{
				FS: m,
				Stat: func(p string) (FileInfo, error) {
					if _, err := fs.Stat(m, strings.TrimPrefix(p, "/")); err != nil {
						return FileInfo{}, err
					}
					return FileInfo{Mode: 0o644}, nil
				},
				Run:        world{}.run,
				Getenv:     func(k string) string { return map[string]string{"HEADSCALE_CONFIG": tt.env, "HOME": "/home/u"}[k] },
				ConfigFlag: tt.flag,
				Cwd:        "/work",
				Dial:       world{}.dial,
				Uid:        1000,
			}
			if env := Detect(o); env.ServerURL != tt.want {
				t.Fatalf("server_url = %q (config %q), want %q", env.ServerURL, env.ConfigPath, tt.want)
			}
		})
	}
}

func TestParseVersion(t *testing.T) {
	tests := map[string]string{
		"v0.29.6":                     "0.29.6",
		"headscale version v0.30.0\n": "0.30.0",
		"0.30.0-beta.2":               "0.30.0-beta.2",
		"dev":                         "",
	}
	for in, want := range tests {
		if got := ParseVersion(in); got != want {
			t.Errorf("ParseVersion(%q) = %q, want %q", in, got, want)
		}
	}
	if !AtLeast("0.30.0", 0, 30) || AtLeast("0.29.6", 0, 30) || !AtLeast("1.0.0", 0, 30) || AtLeast("", 0, 1) {
		t.Error("AtLeast")
	}
}

func TestParseConfigDefaults(t *testing.T) {
	c := ParseConfig([]byte("database:\n  type: postgres\n  postgres:\n    host: db\n    port: 5432\n    name: hs\n    user: hs\n    pass: secret\n"))
	if c.UnixSocket != "/var/run/headscale/headscale.sock" || c.MetricsAddr != "127.0.0.1:9090" || c.PolicyMode != "file" {
		t.Errorf("defaults %+v", c)
	}
	if strings.Contains(c.PostgresDSN, "secret") || !strings.Contains(c.PostgresDSN, "host=db") {
		t.Errorf("dsn %q", c.PostgresDSN)
	}
	if c := ParseConfig([]byte("metrics_listen_addr: \"\"\n")); c.MetricsAddr != "" || !c.MetricsSet {
		t.Errorf("disabled metrics %+v", c)
	}
}
