package main

import (
	"context"
	"fmt"
	"net/netip"
	"strings"

	"github.com/tonhe/headsup/internal/api"
	"github.com/tonhe/headsup/internal/api/execcli"
	"github.com/tonhe/headsup/internal/api/fake"
	"github.com/tonhe/headsup/internal/api/httpapi"
	"github.com/tonhe/headsup/internal/config"
	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/host/detect"
	hostfake "github.com/tonhe/headsup/internal/host/fake"
	"github.com/tonhe/headsup/internal/host/local"
)

// detectEnv runs host detection with the --hs-config flag and the [host]
// hints from the config file.
func detectEnv(f config.Flags, hc config.Host) host.Env {
	opts := detect.Defaults()
	if f.HSConfig != "" {
		opts.ConfigFlag = f.HSConfig
	}
	opts.ContainerHint = hc.Container
	opts.DebugAddr = hc.DebugAddr
	return detect.Detect(opts)
}

// hostConnect builds a connection from what is on this machine: no API key,
// the local socket or CLI, and the host adapter for files, service and logs.
func hostConnect(res config.Resolved, f config.Flags) (connection, error) {
	if res.HSConfig != "" && f.HSConfig == "" {
		f.HSConfig = res.HSConfig
	}
	env := detectEnv(f, res.HostConfig)
	if !env.Usable() {
		return connection{}, hostUnavailable(env)
	}
	var b api.Backend
	switch env.Transport {
	case host.TransportUnixHTTP:
		c, err := httpapi.New("http://localhost", api.Secret{},
			httpapi.WithUnixSocket(env.SocketPath), httpapi.WithVersion(env.Version))
		if err != nil {
			return connection{}, err
		}
		b = c
	case host.TransportExecCLI:
		opts := []execcli.Option{execcli.WithVersion(env.Version)}
		if env.ConfigPath != "" {
			opts = append(opts, execcli.WithConfig(env.ConfigPath))
		}
		b = execcli.New(opts...)
	case host.TransportContainerExec:
		b = execcli.New(execcli.WithContainer(local.ContainerRuntime(env), env.Container), execcli.WithVersion(env.Version))
	default:
		return connection{}, hostUnavailable(env)
	}
	serverURL := env.ServerURL
	if serverURL == "" {
		serverURL = "https://" + env.Hostname
	}
	return connection{
		backend:   b,
		host:      local.New(env, hostOptions(res.HostConfig)),
		label:     "host:" + env.Hostname,
		serverURL: serverURL,
	}, nil
}

// hostOptions passes [host] settings to the host adapter: where per-write
// backups go and how many to keep per file. Zero values keep its defaults.
func hostOptions(hc config.Host) local.Options {
	o := local.Options{Keep: hc.BackupKeep}
	if hc.BackupDir != "" {
		o.BackupDir = config.ExpandHome(hc.BackupDir)
	}
	return o
}

// hostUnavailable explains why host mode can't be used and how to fix it.
func hostUnavailable(env host.Env) error {
	var b strings.Builder
	if env.ConfigPath == "" && env.Transport == host.TransportNone {
		b.WriteString("headscale was not found on this machine (no config in /etc/headscale, $HOME/.headscale or ./, and no headscale container)")
		// A running service means it is here but hidden from this user.
		return &hostErr{msg: b.String(), denied: env.Service != host.ServiceNone}
	}
	b.WriteString("host mode is not available")
	p, missing := env.Perms.Missing(host.NeedSocket)
	if missing {
		fmt.Fprintf(&b, ": %s", p.Reason)
		if p.FixCommand != "" {
			fmt.Fprintf(&b, "\n\nFix: %s", p.FixCommand)
		}
	}
	return &hostErr{msg: b.String(), denied: missing && p.Denied}
}

// demoHost is a fake host whose node IDs line up with the demo backend.
func demoHost() host.Host {
	nodes, _ := fake.Demo().ListNodes(context.Background(), api.NodeFilter{})
	ids := make([]uint64, len(nodes))
	var ha []uint64
	ha24 := netip.MustParsePrefix("10.0.0.0/24")
	for i, n := range nodes {
		ids[i] = n.ID
		for _, p := range n.AvailableRoutes {
			if p == ha24 {
				ha = append(ha, n.ID)
			}
		}
	}
	opts := []hostfake.Option{hostfake.WithNodes(ids)}
	if len(ha) >= 2 {
		opts = append(opts, hostfake.WithHAPair(ha[0], ha[1]))
	}
	h := hostfake.Demo()
	for _, o := range opts {
		o(h)
	}
	return h
}
