package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tonhe/headsup/internal/host"
)

type call struct {
	name string
	args []string
}

type fakeRunner struct {
	calls []call
	reply func(name string, args []string) Result
}

func (f *fakeRunner) run(_ context.Context, name string, args ...string) (Result, error) {
	f.calls = append(f.calls, call{name, args})
	if f.reply == nil {
		return Result{}, nil
	}
	return f.reply(name, args), nil
}

func (f *fakeRunner) last() string {
	if len(f.calls) == 0 {
		return ""
	}
	c := f.calls[len(f.calls)-1]
	return strings.TrimSpace(c.name + " " + strings.Join(c.args, " "))
}

func TestCommands(t *testing.T) {
	tests := []struct {
		name string
		mk   func(RunFunc) host.ServiceManager
		op   func(host.ServiceManager, context.Context) error
		want string
	}{
		{"systemd restart", func(r RunFunc) host.ServiceManager { return NewSystemd(r, "") }, host.ServiceManager.Restart, "systemctl restart headscale.service"},
		{"systemd reload", func(r RunFunc) host.ServiceManager { return NewSystemd(r, "") }, host.ServiceManager.Reload, "systemctl reload headscale.service"},
		{"systemd stop", func(r RunFunc) host.ServiceManager { return NewSystemd(r, "") }, host.ServiceManager.Stop, "systemctl stop headscale.service"},
		{"openrc start", func(r RunFunc) host.ServiceManager { return NewOpenRC(r, "") }, host.ServiceManager.Start, "rc-service headscale start"},
		{"docker restart", func(r RunFunc) host.ServiceManager { return NewContainer(r, "docker", "headscale") }, host.ServiceManager.Restart, "docker restart headscale"},
		{"podman reload", func(r RunFunc) host.ServiceManager { return NewContainer(r, "podman", "hs") }, host.ServiceManager.Reload, "podman kill --signal HUP hs"},
		{"process reload", func(r RunFunc) host.ServiceManager { return NewProcess(r) }, host.ServiceManager.Reload, "kill -HUP 4242"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fr := &fakeRunner{reply: func(name string, args []string) Result {
				if name == "pgrep" {
					return Result{Stdout: []byte("4242\n")}
				}
				return Result{}
			}}
			if err := tt.op(tt.mk(fr.run), context.Background()); err != nil {
				t.Fatal(err)
			}
			if fr.last() != tt.want {
				t.Fatalf("ran %q, want %q", fr.last(), tt.want)
			}
		})
	}
}

func TestProcessOnlyReloads(t *testing.T) {
	p := NewProcess((&fakeRunner{}).run)
	for _, op := range []func(context.Context) error{p.Start, p.Stop, p.Restart} {
		if !errors.Is(op(context.Background()), ErrUnsupported) {
			t.Error("expected ErrUnsupported")
		}
	}
	if !errors.Is(None{}.Restart(context.Background()), host.ErrNoHost) {
		t.Error("None should return ErrNoHost")
	}
}

func TestPermissionErrors(t *testing.T) {
	tests := []struct {
		name string
		mk   func(RunFunc) host.ServiceManager
		out  string
		fix  string
	}{
		{"polkit", func(r RunFunc) host.ServiceManager { return NewSystemd(r, "") }, "Failed to restart headscale.service: Access denied", "sudo systemctl restart headscale"},
		{"interactive", func(r RunFunc) host.ServiceManager { return NewSystemd(r, "") }, "Failed to restart headscale.service: Interactive authentication required.\nSee system logs and 'systemctl status headscale.service' for details.", "sudo systemctl restart headscale"},
		{"openrc", func(r RunFunc) host.ServiceManager { return NewOpenRC(r, "") }, " * rc-service: superuser access required", "sudo rc-service headscale restart"},
		{"docker", func(r RunFunc) host.ServiceManager { return NewContainer(r, "docker", "headscale") }, "permission denied while trying to connect to the Docker daemon socket", "sudo usermod -aG docker $USER && newgrp docker"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fr := &fakeRunner{reply: func(string, []string) Result { return Result{Stderr: []byte(tt.out), Exit: 1} }}
			err := tt.mk(fr.run).Restart(context.Background())
			var pe *host.ErrPermission
			if !errors.As(err, &pe) {
				t.Fatalf("err = %v", err)
			}
			if pe.FixCommand != tt.fix || pe.Need != host.NeedService {
				t.Fatalf("fix = %q need %v", pe.FixCommand, pe.Need)
			}
		})
	}
	fr := &fakeRunner{reply: func(string, []string) Result { return Result{Stderr: []byte("Job failed"), Exit: 1} }}
	var ce *CmdError
	if err := NewSystemd(fr.run, "").Start(context.Background()); !errors.As(err, &ce) {
		t.Fatalf("err = %v, want CmdError", err)
	}
}

func TestSystemdStatus(t *testing.T) {
	out := "ActiveState=active\nSubState=running\nLoadState=loaded\nMainPID=812\nExecMainStartTimestamp=Wed 2026-10-28 09:15:02 UTC\nMemoryCurrent=48361472\n"
	fr := &fakeRunner{reply: func(string, []string) Result { return Result{Stdout: []byte(out)} }}
	st, err := NewSystemd(fr.run, "").Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 10, 28, 9, 15, 2, 0, time.UTC)
	if !st.Running || st.State != "active" || st.PID != 812 || st.Memory != 48361472 || !st.Since.Equal(want) {
		t.Fatalf("status = %+v", st)
	}
	tests := []struct {
		in      string
		state   string
		running bool
	}{
		{"ActiveState=failed\nSubState=failed\nMemoryCurrent=[not set]\nMainPID=0\n", "failed", false},
		{"ActiveState=inactive\nLoadState=not-found\n", "unknown", false},
		{"ActiveState=activating\nSubState=start\n", "activating", false},
	}
	for _, tt := range tests {
		st := ParseSystemdShow(tt.in)
		if st.State != tt.state || st.Running != tt.running {
			t.Errorf("%q -> %+v", tt.in, st)
		}
	}
}

func TestOtherStatus(t *testing.T) {
	fr := &fakeRunner{reply: func(name string, args []string) Result {
		switch name {
		case "rc-service":
			return Result{Stdout: []byte(" * status: started\n")}
		case "docker":
			return Result{Stdout: []byte("running|3131|2026-10-28T09:00:00.123456789Z\n")}
		}
		return Result{Exit: 1}
	}}
	if st, _ := NewOpenRC(fr.run, "").Status(context.Background()); !st.Running {
		t.Errorf("openrc %+v", st)
	}
	if st, _ := NewContainer(fr.run, "docker", "hs").Status(context.Background()); !st.Running || st.PID != 3131 || st.Since.IsZero() {
		t.Errorf("docker %+v", st)
	}
	if st, _ := NewProcess(fr.run).Status(context.Background()); st.Running {
		t.Errorf("process %+v", st)
	}
}

func TestConfigtest(t *testing.T) {
	tests := []struct {
		name  string
		out   string
		exit  int
		want  []string
		sev   host.Severity
		lines []int
	}{
		{name: "ok", out: "", exit: 0},
		{name: "0.30 structured",
			out:  "Fatal config error: dns.base_domain must not be part of server_url\n  current: dns.base_domain=\"example.com\", server_url=\"https://hs.example.com\"\n  hint: pick a different base_domain\n",
			exit: 1, want: []string{"dns.base_domain must not be part of server_url; current: dns.base_domain=\"example.com\", server_url=\"https://hs.example.com\"; hint: pick a different base_domain"}, sev: host.SevError},
		{name: "0.29 zerolog console",
			out:  "2026-10-28T12:00:00Z WRN Warning: when using tags, all tags must be defined\n\x1b[31m2026-10-28T12:00:00Z FTL\x1b[0m cmd/headscale/cli/root.go:50 > error loading config file /tmp/x.yaml error=\"yaml: line 12: did not find expected key\"\n",
			exit: 1, want: []string{"Warning: when using tags, all tags must be defined", "cmd/headscale/cli/root.go:50 > error loading config file /tmp/x.yaml: yaml: line 12: did not find expected key"}, lines: []int{0, 12}},
		{name: "json log", out: `{"level":"fatal","error":"invalid prefix","time":"2026-10-28T12:00:00Z","message":"error loading config"}`,
			exit: 1, want: []string{"invalid prefix: error loading config"}},
		{name: "cobra error", out: "Error: configuration error: server_url must start with https:// or http://\n", exit: 1,
			want: []string{"configuration error: server_url must start with https:// or http://"}},
		{name: "unparsed failure", out: "segfault", exit: 2, want: []string{"segfault"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fr := &fakeRunner{reply: func(string, []string) Result { return Result{Stderr: []byte(tt.out), Exit: tt.exit} }}
			issues, err := NewSystemd(fr.run, "").Configtest(context.Background(), "/etc/headscale/.config.headsup-check.yaml")
			if err != nil {
				t.Fatal(err)
			}
			if fr.last() != "headscale configtest -c /etc/headscale/.config.headsup-check.yaml" {
				t.Errorf("ran %q", fr.last())
			}
			if len(issues) != len(tt.want) {
				t.Fatalf("issues = %+v", issues)
			}
			for i, w := range tt.want {
				if issues[i].Message != w {
					t.Errorf("issue %d = %q, want %q", i, issues[i].Message, w)
				}
				if tt.lines != nil && issues[i].Line != tt.lines[i] {
					t.Errorf("issue %d line %d, want %d", i, issues[i].Line, tt.lines[i])
				}
			}
			if tt.exit != 0 {
				last := issues[len(issues)-1]
				if last.Severity != host.SevError {
					t.Errorf("last issue severity %v", last.Severity)
				}
			}
		})
	}
	fr := &fakeRunner{}
	if _, err := NewContainer(fr.run, "podman", "hs").Configtest(context.Background(), "/etc/headscale/config.yaml"); err != nil {
		t.Fatal(err)
	}
	if fr.last() != "podman exec hs headscale configtest -c /etc/headscale/config.yaml" {
		t.Errorf("ran %q", fr.last())
	}
}
