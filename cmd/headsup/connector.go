package main

import (
	"errors"
	"os"

	"github.com/tonhe/headsup/internal/app"
	"github.com/tonhe/headsup/internal/config"
)

// newConnector lets the running app switch profiles and save new ones.
func newConnector(cfg *config.Config, cfgPath string, f config.Flags, hostUsable bool) app.Connector {
	open := func(target string) (app.Conn, error) {
		var c connection
		var err error
		if target == "host" {
			hf := f
			hf.Host = true
			c, err = hostConnect(config.Resolved{HostConfig: cfg.Host}, hf)
		} else {
			var res config.Resolved
			res, err = config.Resolve(*cfg, config.Flags{Profile: target, ConfigPath: cfgPath}, os.Getenv)
			if err == nil {
				c, err = remoteConnect(res)
				c.readOnly = res.Profile.ReadOnly
			}
		}
		if err != nil {
			return app.Conn{}, err
		}
		return app.Conn{Backend: c.backend, Host: c.host, Label: c.label, ServerURL: c.serverURL, ReadOnly: c.readOnly}, nil
	}
	return app.Connector{
		Profiles: func() []string {
			var out []string
			if hostUsable {
				out = append(out, "host")
			}
			for _, p := range cfg.Profiles {
				out = append(out, p.Name)
			}
			return out
		},
		Open: open,
		Save: func(in app.ProfileInput) (string, error) {
			if in.Name == "" || in.URL == "" {
				return "", errors.New("a profile needs a name and a server URL")
			}
			if err := config.StoreKeyring(in.Name, in.Key); err != nil {
				return "", errors.New("could not store the API key in the OS keyring (" + err.Error() +
					"); set api_key_env, api_key_file or api_key_cmd for this profile in the config instead")
			}
			cfg.UpsertProfile(config.Profile{
				Name: in.Name, URL: in.URL, Keyring: config.Bool(true),
				Insecure: config.Bool(in.Insecure), ReadOnly: config.Bool(in.ReadOnly),
			})
			if cfg.DefaultProfile == "" {
				cfg.DefaultProfile = in.Name
			}
			if err := config.Save(cfgPath, *cfg); err != nil {
				return "", err
			}
			return in.Name, nil
		},
	}
}
