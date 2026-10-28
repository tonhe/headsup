package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"strings"

	"github.com/tonhe/fathom/internal/api"
	"github.com/tonhe/fathom/internal/api/execcli"
	"github.com/tonhe/fathom/internal/api/fake"
	"github.com/tonhe/fathom/internal/api/httpapi"
	"github.com/tonhe/fathom/internal/config"
	"github.com/tonhe/fathom/internal/host"
	"github.com/tonhe/fathom/internal/host/detect"
	hostfake "github.com/tonhe/fathom/internal/host/fake"
	"github.com/tonhe/fathom/internal/host/local"
)

func detectEnv(f config.Flags) host.Env {
	opts := detect.Defaults()
	if f.HSConfig != "" {
		opts.ConfigFlag = f.HSConfig
	}
	return detect.Detect(opts)
}

// hostConnect builds a connection from what is on this machine: no API key,
// the local socket or CLI, and the host adapter for files, service and logs.
func hostConnect(res config.Resolved, f config.Flags) (connection, error) {
	if res.HSConfig != "" && f.HSConfig == "" {
		f.HSConfig = res.HSConfig
	}
	env := detectEnv(f)
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
		host:      local.New(env, local.Options{}),
		label:     "host:" + env.Hostname,
		serverURL: serverURL,
	}, nil
}

// hostUnavailable explains why host mode can't be used and how to fix it.
func hostUnavailable(env host.Env) error {
	var b strings.Builder
	if env.ConfigPath == "" && env.Transport == host.TransportNone {
		b.WriteString("headscale was not found on this machine (no config in /etc/headscale, $HOME/.headscale or ./, and no headscale container)")
		return errors.New(b.String())
	}
	b.WriteString("host mode is not available")
	if p, missing := env.Perms.Missing(host.NeedSocket); missing {
		fmt.Fprintf(&b, ": %s", p.Reason)
		if p.FixCommand != "" {
			fmt.Fprintf(&b, "\n\nFix: %s", p.FixCommand)
		}
	}
	return errors.New(b.String())
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

// hostInfo prints what detection found, with fix commands (`fathom host info`).
func hostInfo(f config.Flags, w io.Writer) int {
	env := detectEnv(f)
	row := func(k, v string) {
		if v == "" {
			v = "-"
		}
		fmt.Fprintf(w, "%-14s %s\n", k, v)
	}
	row("hostname", env.Hostname)
	row("config", env.ConfigPath)
	row("version", env.Version)
	row("transport", env.Transport.String())
	row("socket", env.SocketPath)
	row("container", strings.TrimSpace(env.ContainerRuntime+" "+env.Container))
	row("service", env.Service.String())
	row("logs", env.LogSource.String())
	row("database", strings.TrimSpace(env.DB.String()+" "+env.DBPath))
	row("debug", env.DebugAddr)
	row("server_url", env.ServerURL)
	row("policy", strings.TrimSpace(env.PolicyMode+" "+env.PolicyPath))
	fmt.Fprintln(w)
	needs := []struct {
		n    host.Need
		name string
	}{
		{host.NeedSocket, "local API"}, {host.NeedConfigR, "read config"}, {host.NeedConfigW, "write config"},
		{host.NeedPolicyW, "write policy"}, {host.NeedRecordsW, "write DNS records"}, {host.NeedService, "control service"},
		{host.NeedLogs, "read logs"}, {host.NeedDebug, "debug/metrics"}, {host.NeedDBRead, "read database"},
	}
	for _, n := range needs {
		p, ok := env.Perms[n.n]
		switch {
		case !ok:
			fmt.Fprintf(w, "  -  %-18s not applicable\n", n.name)
		case p.OK:
			fmt.Fprintf(w, "  ok %-18s\n", n.name)
		default:
			fmt.Fprintf(w, "  no %-18s %s\n", n.name, p.Reason)
			if p.FixCommand != "" {
				fmt.Fprintf(w, "     %-18s fix: %s\n", "", p.FixCommand)
			}
		}
	}
	for _, n := range env.Notes {
		fmt.Fprintln(w, "note:", n)
	}
	if !env.Usable() {
		return 8
	}
	return 0
}
