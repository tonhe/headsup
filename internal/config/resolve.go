package config

import (
	"errors"
	"fmt"

	"github.com/tonhe/headsup/internal/brand"
)

// Flags are the command-line overrides. The API key is never a flag value.
type Flags struct {
	Profile    string
	URL        string
	APIKeyFile string
	Theme      string
	Icons      string
	ReadOnly   bool
	Insecure   bool
	Demo       bool
	Host       bool // force host mode
	Remote     bool // skip host mode
	HSConfig   string
	ConfigPath string
}

// HostPreference says whether host mode should be attempted.
type HostPreference int

const (
	HostAuto HostPreference = iota
	HostAlways
	HostNever
)

func (h HostPreference) String() string {
	switch h {
	case HostAlways:
		return "always"
	case HostNever:
		return "never"
	}
	return "auto"
}

// KeySourceKind names where the API key comes from.
type KeySourceKind int

const (
	KeyNone KeySourceKind = iota
	KeyEnv
	KeyFile
	KeyCmd
	KeyKeyring
)

func (k KeySourceKind) String() string {
	return [...]string{"none", "env", "file", "command", "keyring"}[k]
}

// KeySource describes where to fetch the key; it never holds the key itself.
type KeySource struct {
	Kind KeySourceKind
	Env  string // variable name for KeyEnv
	File string // path for KeyFile (home-expanded)
	Cmd  string // shell command for KeyCmd
}

func (k KeySource) String() string {
	switch k.Kind {
	case KeyEnv:
		return "env " + k.Env
	case KeyFile:
		return "file " + k.File
	case KeyCmd:
		return "command"
	case KeyKeyring:
		return "keyring"
	}
	return "none"
}

// ResolvedProfile is the effective connection after precedence.
type ResolvedProfile struct {
	Name     string // "" for an ad-hoc connection from flags/env
	URL      string
	Key      KeySource
	CAFile   string
	Insecure bool
	ReadOnly bool
	Theme    string
}

// Resolved is the effective runtime configuration.
type Resolved struct {
	Profile ResolvedProfile
	// Explicit is true when the connection was chosen by flag or env
	// (profile name, URL or key). When false the app may prefer host mode
	// over default_profile.
	Explicit   bool
	UI         UI
	Host       HostPreference
	HSConfig   string
	Demo       bool
	NoColor    bool
	ConfigPath string
}

// Env var names.
var (
	envURL     = []string{brand.EnvPrefix + "_URL", "HEADSCALE_URL", "HEADSCALE_CLI_ADDRESS"}
	envAPIKey  = []string{brand.EnvPrefix + "_API_KEY", "HEADSCALE_API_KEY", "HEADSCALE_CLI_API_KEY"}
	envProfile = brand.EnvPrefix + "_PROFILE"
	envTheme   = brand.EnvPrefix + "_THEME"
)

// Resolve applies precedence: flags > env > profile > [defaults] > built-in.
func Resolve(cfg Config, f Flags, env func(string) string) (Resolved, error) {
	if env == nil {
		env = getenv
	}
	r := Resolved{UI: cfg.UI, Demo: f.Demo, ConfigPath: f.ConfigPath}
	if r.ConfigPath == "" {
		r.ConfigPath = ConfigFile()
	}
	r.NoColor = env("NO_COLOR") != ""

	_, envURLVal := firstEnv(env, envURL)
	envKeyName, _ := firstEnv(env, envAPIKey)

	// Choose a profile.
	name, explicitName := f.Profile, f.Profile != ""
	if name == "" {
		if v := env(envProfile); v != "" {
			name, explicitName = v, true
		}
	}
	urlOverride := f.URL
	if urlOverride == "" {
		urlOverride = envURLVal
	}
	r.Explicit = explicitName || urlOverride != "" || envKeyName != "" || f.APIKeyFile != ""
	if name == "" && urlOverride == "" {
		// Only fall back to default_profile when no ad-hoc URL is given:
		// a profile's key must never be sent to a different server.
		name = cfg.DefaultProfile
	}

	var prof Profile
	if name != "" {
		p, ok := cfg.FindProfile(name)
		if !ok {
			src := "default_profile"
			if f.Profile != "" {
				src = "--profile"
			} else if explicitName {
				src = envProfile
			}
			return r, fmt.Errorf("profile %q (from %s) is not defined in %s", name, src, r.ConfigPath)
		}
		prof = p
	}

	rp := ResolvedProfile{Name: prof.Name}
	rp.URL = pick(urlOverride, prof.URL, cfg.Defaults.URL)
	rp.CAFile = ExpandHome(pick(prof.CAFile, cfg.Defaults.CAFile))
	rp.Insecure = f.Insecure || tri(prof.Insecure, cfg.Defaults.Insecure)
	rp.ReadOnly = f.ReadOnly || tri(prof.ReadOnly, cfg.Defaults.ReadOnly)
	rp.Theme = pick(f.Theme, env(envTheme), prof.Theme, cfg.Defaults.Theme, cfg.UI.Theme)

	switch {
	case f.APIKeyFile != "":
		rp.Key = KeySource{Kind: KeyFile, File: ExpandHome(f.APIKeyFile)}
	case envKeyName != "":
		rp.Key = KeySource{Kind: KeyEnv, Env: envKeyName}
	default:
		rp.Key = keySource(prof)
		if rp.Key.Kind == KeyNone {
			rp.Key = keySource(cfg.Defaults)
		}
		if rp.Key.Kind == KeyKeyring && rp.Name == "" {
			rp.Key = KeySource{} // keyring entries are per profile name
		}
	}
	r.Profile = rp

	r.UI.Theme = rp.Theme
	if f.Icons != "" {
		r.UI.Icons = f.Icons
	}

	switch {
	case f.Host && f.Remote:
		return r, errors.New("--host and --remote are mutually exclusive")
	case f.Host:
		r.Host = HostAlways
	case f.Remote:
		r.Host = HostNever
	default:
		switch cfg.Host.Enabled {
		case "always":
			r.Host = HostAlways
		case "never":
			r.Host = HostNever
		default:
			r.Host = HostAuto
		}
	}
	r.HSConfig = ExpandHome(pick(f.HSConfig, env("HEADSCALE_CONFIG"), cfg.Host.HeadscaleConfig))
	return r, nil
}

func keySource(p Profile) KeySource {
	switch {
	case p.APIKeyEnv != "":
		return KeySource{Kind: KeyEnv, Env: p.APIKeyEnv}
	case p.APIKeyFile != "":
		return KeySource{Kind: KeyFile, File: ExpandHome(p.APIKeyFile)}
	case p.APIKeyCmd != "":
		return KeySource{Kind: KeyCmd, Cmd: p.APIKeyCmd}
	case p.Keyring != nil && *p.Keyring:
		return KeySource{Kind: KeyKeyring}
	}
	return KeySource{}
}

func firstEnv(env func(string) string, names []string) (string, string) {
	for _, n := range names {
		if v := env(n); v != "" {
			return n, v
		}
	}
	return "", ""
}

func pick(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

func tri(vs ...*bool) bool {
	for _, v := range vs {
		if v != nil {
			return *v
		}
	}
	return false
}
