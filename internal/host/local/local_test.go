package local

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tonhe/fathom/internal/host"
	"github.com/tonhe/fathom/internal/host/service"
)

const cfg = "server_url: https://hs.example.com\nlog:\n  level: info\n"

func setup(t *testing.T, env host.Env, rf service.RunFunc) (*Host, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "etc/headscale"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "etc/headscale/config.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	h := New(env, Options{Root: root, BackupDir: filepath.Join(t.TempDir(), "b"), Run: rf})
	return h, root
}

func TestConfigPipeline(t *testing.T) {
	var ran []string
	fail := false
	rf := func(ctx context.Context, name string, args ...string) (service.Result, error) {
		ran = append(ran, name+" "+strings.Join(args, " "))
		if fail {
			return service.Result{Stderr: []byte("Fatal config error: listen_addr is invalid\n"), Exit: 1}, nil
		}
		return service.Result{}, nil
	}
	env := host.Env{ConfigPath: "/etc/headscale/config.yaml", Version: "0.30.0", Service: host.ServiceSystemd, Transport: host.TransportUnixHTTP}
	h, root := setup(t, env, rf)
	ctx := context.Background()

	f, err := h.Open(host.FileConfig, "")
	if err != nil {
		t.Fatal(err)
	}
	c, err := h.Plan(ctx, f, []byte(strings.Replace(cfg, "info", "debug", 1)))
	if err != nil || !c.Valid() || c.Danger != host.DangerConfirm || c.Effect != host.EffectRestart {
		t.Fatalf("plan %+v %v", c, err)
	}
	if len(ran) != 1 || !strings.HasPrefix(ran[0], "headscale configtest -c ") || strings.HasSuffix(ran[0], "/config.yaml") {
		t.Fatalf("configtest ran %v", ran)
	}
	if _, err := h.Apply(ctx, c); err != nil {
		t.Fatal(err)
	}

	f, _ = h.Open(host.FileConfig, "")
	crit, _ := h.Plan(ctx, f, []byte(strings.Replace(string(f.Raw), "hs.example.com", "hs2.example.com", 1)))
	if crit.Danger != host.DangerCritical {
		t.Errorf("server_url change danger %v", crit.Danger)
	}

	fail = true
	bad, _ := h.Plan(ctx, f, []byte(string(f.Raw)+"listen_addr: nope\n"))
	if bad.Valid() {
		t.Fatal("invalid config accepted")
	}
	before, _ := os.ReadFile(filepath.Join(root, "etc/headscale/config.yaml"))
	var ei *host.ErrInvalid
	if _, err := h.Apply(ctx, bad); !errors.As(err, &ei) {
		t.Fatalf("err = %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(root, "etc/headscale/config.yaml"))
	if string(before) != string(after) {
		t.Fatal("file changed")
	}

	yamlBad, _ := h.Plan(ctx, f, []byte("a: [\n"))
	if yamlBad.Valid() {
		t.Fatal("broken YAML accepted")
	}
}

func TestWiring(t *testing.T) {
	tests := []struct {
		env  host.Env
		kind host.ServiceKind
	}{
		{host.Env{Service: host.ServiceSystemd}, host.ServiceSystemd},
		{host.Env{Service: host.ServiceOpenRC}, host.ServiceOpenRC},
		{host.Env{Service: host.ServiceContainer, Container: "hs", Notes: []string{"container runtime: podman"}}, host.ServiceContainer},
		{host.Env{Service: host.ServiceProcess}, host.ServiceProcess},
		{host.Env{}, host.ServiceNone},
	}
	for _, tt := range tests {
		h := New(tt.env, Options{Run: func(context.Context, string, ...string) (service.Result, error) { return service.Result{}, nil }})
		if h.Service().Kind() != tt.kind {
			t.Errorf("service %v want %v", h.Service().Kind(), tt.kind)
		}
	}
	if ContainerRuntime(host.Env{Notes: []string{"x", "container runtime: podman"}}) != "podman" {
		t.Error("runtime from notes")
	}

	h := New(host.Env{}, Options{})
	ctx := context.Background()
	if _, err := h.Logs(ctx, host.LogQuery{}); !errors.Is(err, host.ErrNoHost) {
		t.Errorf("logs err %v", err)
	}
	if _, err := h.NodeExtras(ctx, []uint64{1}); !errors.Is(err, host.ErrNoHost) {
		t.Errorf("extras err %v", err)
	}
	if _, err := h.Metrics(ctx); !errors.Is(err, host.ErrNoHost) {
		t.Errorf("metrics err %v", err)
	}
	if h.Debug().Available() {
		t.Error("debug available without address")
	}
	if _, err := h.Open(host.FilePolicy, ""); !errors.Is(err, host.ErrNoHost) {
		t.Errorf("open policy err %v", err)
	}
	if err := h.Service().Restart(ctx); !errors.Is(err, host.ErrNoHost) {
		t.Errorf("restart err %v", err)
	}

	jh := New(host.Env{LogSource: host.LogJournald, Perms: host.Perms{host.NeedLogs: {Reason: "no", FixCommand: "sudo usermod -aG systemd-journal $USER"}}}, Options{})
	var pe *host.ErrPermission
	if _, err := jh.Logs(ctx, host.LogQuery{}); !errors.As(err, &pe) || pe.FixCommand == "" {
		t.Errorf("journal perm err %v", err)
	}
}

func TestSchemas(t *testing.T) {
	tests := []struct {
		name   string
		fn     func([]byte) []host.Issue
		in     string
		errors int
	}{
		{"policy ok", PolicySchema, "// c\n{\"acls\": [{\"action\": \"accept\", \"src\": [\"*\"], \"dst\": [\"*:*\"]},],}", 0},
		{"policy syntax", PolicySchema, "{\"acls\": [", 1},
		{"policy not object", PolicySchema, "[1]", 1},
		{"records json ok", RecordsSchema, `[{"name":"a.ts.net","type":"A","value":"100.64.0.1"},{"name":"b","type":"AAAA","value":"fd7a::1"}]`, 0},
		{"records yaml ok", RecordsSchema, "- name: a\n  type: A\n  value: 100.64.0.1\n", 0},
		{"records bad type", RecordsSchema, `[{"name":"a","type":"CNAME","value":"b"}]`, 1},
		{"records family", RecordsSchema, `[{"name":"a","type":"A","value":"fd7a::1"}]`, 1},
		{"records no name", RecordsSchema, `[{"type":"A","value":"100.64.0.1"}]`, 1},
		{"derp ok", DERPMapSchema, "regions:\n  900:\n    regionid: 900\n    regioncode: lab\n    nodes:\n      - name: 900a\n        regionid: 900\n        hostname: derp.example.com\n", 0},
		{"derp json ok", DERPMapSchema, `{"Regions":{"901":{"RegionID":901,"Nodes":[{"Name":"a","HostName":"d.example.com"}]}}}`, 0},
		{"derp empty", DERPMapSchema, "regions: {}\n", 1},
		{"derp no host", DERPMapSchema, "regions:\n  1:\n    regionid: 1\n    nodes:\n      - name: x\n", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := 0
			for _, i := range tt.fn([]byte(tt.in)) {
				if i.Severity == host.SevError {
					n++
				}
			}
			if n != tt.errors {
				t.Fatalf("errors = %d, want %d: %+v", n, tt.errors, tt.fn([]byte(tt.in)))
			}
		})
	}
}
