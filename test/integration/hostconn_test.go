//go:build integration

package integration

import (
	"testing"

	"github.com/tonhe/headsup/internal/api"
	"github.com/tonhe/headsup/internal/api/execcli"
	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/host/detect"
	"github.com/tonhe/headsup/internal/host/local"
)

// hostConn detects the containerised headscale the way cmd/headsup does
// with `[host].container` set, and builds the same backend and host adapter.
func hostConn(t *testing.T) (api.Backend, host.Host, host.Env) {
	t.Helper()
	opts := detect.Defaults()
	opts.ConfigFlag = S.configPath()
	opts.ContainerHint = S.name
	env := detect.Detect(opts)
	if !env.Usable() || env.Transport != host.TransportContainerExec {
		t.Fatalf("host mode should be usable through the container: transport %v, perms %+v, notes %v", env.Transport, env.Perms, env.Notes)
	}
	b := execcli.New(execcli.WithContainer(local.ContainerRuntime(env), env.Container), execcli.WithVersion(env.Version))
	return b, local.New(env, local.Options{}), env
}

// hostTUI starts the app in host mode.
func hostTUI(t *testing.T, w, h int) (*tui, host.Env) {
	t.Helper()
	b, hh, env := hostConn(t)
	d := startTUI(t, S.newApp(t, appOpts{backend: b, host: hh, label: "host:" + env.Hostname, serverURL: env.ServerURL}), w, h)
	d.Wait(tuiWait, "headscale v", "online")
	return d, env
}
