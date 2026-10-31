package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/tonhe/headsup/internal/api"
	"github.com/tonhe/headsup/internal/api/fake"
	"github.com/tonhe/headsup/internal/api/httpapi"
	"github.com/tonhe/headsup/internal/config"
	"github.com/tonhe/headsup/internal/host"
)

// connection is what the UI runs against.
type connection struct {
	backend   api.Backend
	host      host.Host
	label     string
	serverURL string
	readOnly  bool
}

// connect picks the mode: demo, host (on the headscale server), or remote API.
// Host mode is tried first unless the user named a remote connection or
// disabled it; a failed host probe falls back to the profile, if any.
func connect(res config.Resolved, f config.Flags, stderr io.Writer) (connection, error) {
	if res.Demo {
		c := connection{backend: fake.Demo(), label: "demo", serverURL: "https://headscale.example.com"}
		if f.Host {
			c.host = demoHost()
			c.label = "host:demo"
		}
		return c, nil
	}

	wantHost := res.Host != config.HostNever && !f.Remote && (!res.Explicit || f.Host)
	if wantHost {
		c, err := hostConnect(res, f)
		if err != nil {
			if eerr := elevate(err); eerr != nil {
				return connection{}, eerr
			}
		}
		switch {
		case err == nil:
			return c, nil
		case f.Host || res.Host == config.HostAlways:
			return connection{}, err
		case res.Profile.URL == "":
			return connection{}, fmt.Errorf("%w\n\nTo manage a remote server instead, pass --url and set HEADSCALE_API_KEY", err)
		default:
			fmt.Fprintf(stderr, "host mode unavailable (%v); using profile %q\n", firstLine(err), res.Profile.Name)
		}
	}
	return remoteConnect(res)
}

func remoteConnect(res config.Resolved) (connection, error) {
	p := res.Profile
	if p.URL == "" {
		return connection{}, errors.New("no server configured: pass --url (and HEADSCALE_API_KEY), add a profile to the config, or run on the headscale server")
	}
	key, err := config.ResolveKey(p, os.Getenv)
	if err != nil {
		return connection{}, err
	}
	var opts []httpapi.Option
	if p.Insecure {
		opts = append(opts, httpapi.WithInsecureSkipVerify())
	}
	if p.CAFile != "" {
		opts = append(opts, httpapi.WithCAFile(config.ExpandHome(p.CAFile)))
	}
	b, err := httpapi.New(p.URL, key, opts...)
	if err != nil {
		return connection{}, err
	}
	label := p.Name
	if label == "" {
		label = p.URL
	}
	return connection{backend: b, label: label, serverURL: p.URL}, nil
}

func firstLine(err error) string {
	s := err.Error()
	for i, r := range s {
		if r == '\n' {
			return s[:i]
		}
	}
	return s
}
