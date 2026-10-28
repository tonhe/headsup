package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tonhe/fathom/internal/config"
	"github.com/tonhe/fathom/internal/host"
)

func TestHostUnavailableExplains(t *testing.T) {
	none := hostUnavailable(host.Env{})
	if !strings.Contains(none.Error(), "headscale was not found") {
		t.Errorf("no install: %v", none)
	}
	outsider := hostUnavailable(host.Env{
		ConfigPath: "/etc/headscale/config.yaml", Transport: host.TransportUnixHTTP,
		Perms: host.Perms{host.NeedSocket: {Reason: "permission denied on the socket", FixCommand: "sudo usermod -aG headscale $USER"}},
	})
	if !strings.Contains(outsider.Error(), "permission denied") || !strings.Contains(outsider.Error(), "usermod") {
		t.Errorf("outsider: %v", outsider)
	}
}

func TestDemoAndRemoteSelection(t *testing.T) {
	var stderr bytes.Buffer
	c, err := connect(config.Resolved{Demo: true}, config.Flags{Host: true}, &stderr)
	if err != nil || c.host == nil || c.label != "host:demo" {
		t.Fatalf("demo --host: %+v %v", c, err)
	}
	// --remote with no URL must not try host mode and must explain what to pass.
	_, err = connect(config.Resolved{}, config.Flags{Remote: true}, &stderr)
	if err == nil || !strings.Contains(err.Error(), "--url") {
		t.Fatalf("remote without URL: %v", err)
	}
}

func TestSubcommands(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var out, errb bytes.Buffer
	if code := run([]string{"version"}, &out, &errb); code != 0 || !strings.Contains(out.String(), "fathom") {
		t.Fatalf("version: %d %q %q", code, out.String(), errb.String())
	}
	out.Reset()
	if code := run([]string{"themes"}, &out, &errb); code != 0 || strings.Count(out.String(), "\n") < 61 {
		t.Fatalf("themes: %d, %d lines", code, strings.Count(out.String(), "\n"))
	}
	if code := run([]string{"bogus"}, &out, &errb); code != 2 {
		t.Fatalf("unknown command exit = %d, want 2", code)
	}
}
