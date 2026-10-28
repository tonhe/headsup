// Package config loads, validates, saves and resolves the application's
// config.toml (see specs/.../contracts/config.md).
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/tonhe/headsup/internal/brand"
	"github.com/tonhe/headsup/internal/theme"
)

// Config is the on-disk schema.
type Config struct {
	DefaultProfile string              `toml:"default_profile"`
	Defaults       Profile             `toml:"defaults,omitempty"`
	UI             UI                  `toml:"ui"`
	Modules        Modules             `toml:"modules"`
	Keys           map[string][]string `toml:"keys,omitempty"`
	Notify         Notify              `toml:"notify"`
	Host           Host                `toml:"host"`
	Profiles       []Profile           `toml:"profile,omitempty"`
}

// UI holds [ui].
type UI struct {
	Theme       string              `toml:"theme"`
	Icons       string              `toml:"icons"`       // unicode | nerd | ascii
	Density     string              `toml:"density"`     // compact | comfortable
	Sidebar     string              `toml:"sidebar"`     // auto | always | never
	Mouse       bool                `toml:"mouse"`       //
	TimeFormat  string              `toml:"time_format"` // relative | rfc3339 | local
	ExpiryWarn  string              `toml:"expiry_warn"`
	Refresh     string              `toml:"refresh"`
	RefreshIdle string              `toml:"refresh_idle"`
	Columns     map[string][]string `toml:"columns,omitempty"`
}

// Modules holds [modules].
type Modules struct {
	Disabled []string `toml:"disabled"`
}

// Notify holds [notify].
type Notify struct {
	Desktop bool     `toml:"desktop"`
	Bell    bool     `toml:"bell"`
	Events  []string `toml:"events"`
}

// Host holds [host].
type Host struct {
	Enabled         string `toml:"enabled"` // auto | always | never
	HeadscaleConfig string `toml:"headscale_config"`
	Container       string `toml:"container"`
	BackupDir       string `toml:"backup_dir"`
	BackupKeep      int    `toml:"backup_keep"`
	DBEnrich        bool   `toml:"db_enrich"`
	DebugAddr       string `toml:"debug_addr"`
	CheckUpdates    bool   `toml:"check_updates"`
	Doctor          Doctor `toml:"doctor"`
}

// Doctor holds [host.doctor] thresholds.
type Doctor struct {
	CertWarn    string `toml:"cert_warn"`
	CertError   string `toml:"cert_error"`
	WALWarnMB   int    `toml:"wal_warn_mb"`
	DiskWarnPct int    `toml:"disk_warn_pct"`
	SkewWarn    string `toml:"skew_warn"`
}

// Profile is one [[profile]] (and the shape of [defaults]). Exactly one key
// source should be set; the first of APIKeyEnv, APIKeyFile, APIKeyCmd,
// Keyring wins. Plaintext keys in config are deliberately not supported.
type Profile struct {
	Name       string `toml:"name,omitempty"`
	URL        string `toml:"url,omitempty"`
	APIKeyEnv  string `toml:"api_key_env,omitempty"`
	APIKeyFile string `toml:"api_key_file,omitempty"`
	APIKeyCmd  string `toml:"api_key_cmd,omitempty"`
	Keyring    *bool  `toml:"keyring,omitempty"`
	CAFile     string `toml:"ca_file,omitempty"`
	Insecure   *bool  `toml:"insecure,omitempty"`
	ReadOnly   *bool  `toml:"read_only,omitempty"`
	Theme      string `toml:"theme,omitempty"`
}

// Default returns the built-in configuration.
func Default() Config {
	return Config{
		UI: UI{
			Theme:       theme.Default,
			Icons:       "unicode",
			Density:     "comfortable",
			Sidebar:     "auto",
			Mouse:       true,
			TimeFormat:  "relative",
			ExpiryWarn:  "168h",
			Refresh:     "5s",
			RefreshIdle: "30s",
			Columns: map[string][]string{
				"machines": {"status", "name", "user", "ips", "tags", "last_seen", "expiry", "routes"},
			},
		},
		Modules: Modules{Disabled: []string{}},
		Keys:    map[string][]string{},
		Notify: Notify{
			Events: []string{"node_offline", "key_expiring", "route_advertised"},
		},
		Host: Host{
			Enabled:      "auto",
			BackupKeep:   50,
			DBEnrich:     true,
			CheckUpdates: true,
			Doctor: Doctor{
				CertWarn:    "504h",
				CertError:   "168h",
				WALWarnMB:   64,
				DiskWarnPct: 10,
				SkewWarn:    "2s",
			},
		},
	}
}

// Load reads path over Default(). A missing file returns Default() and no error.
func Load(path string) (Config, error) {
	cfg := Default()
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("read config: %w", err)
	}
	dec := toml.NewDecoder(bytes.NewReader(b))
	if err := dec.Decode(&cfg); err != nil {
		return Default(), fmt.Errorf("parse %s: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return cfg, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// Save writes cfg atomically (temp file + rename) with mode 0600, creating
// the parent directory with mode 0700.
func Save(path string, cfg Config) error {
	b, err := toml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"."+brand.Name+"-tmp-*")
	if err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("save config: %w", err)
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("save config: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("save config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("save config: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return fmt.Errorf("save config: %w", err)
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}

// Validate checks enum values and durations.
func (c Config) Validate() error {
	enums := []struct {
		field, v string
		ok       []string
	}{
		{"ui.icons", c.UI.Icons, []string{"unicode", "nerd", "ascii"}},
		{"ui.density", c.UI.Density, []string{"compact", "comfortable"}},
		{"ui.sidebar", c.UI.Sidebar, []string{"auto", "always", "never"}},
		{"ui.time_format", c.UI.TimeFormat, []string{"relative", "rfc3339", "local"}},
		{"host.enabled", c.Host.Enabled, []string{"auto", "always", "never"}},
	}
	for _, e := range enums {
		if e.v == "" {
			continue
		}
		if !contains(e.ok, e.v) {
			return fmt.Errorf("%s = %q: want one of %v", e.field, e.v, e.ok)
		}
	}
	durs := []struct{ field, v string }{
		{"ui.expiry_warn", c.UI.ExpiryWarn},
		{"ui.refresh", c.UI.Refresh},
		{"ui.refresh_idle", c.UI.RefreshIdle},
		{"host.doctor.cert_warn", c.Host.Doctor.CertWarn},
		{"host.doctor.cert_error", c.Host.Doctor.CertError},
		{"host.doctor.skew_warn", c.Host.Doctor.SkewWarn},
	}
	for _, d := range durs {
		if d.v == "" {
			continue
		}
		if _, err := time.ParseDuration(d.v); err != nil {
			return fmt.Errorf("%s = %q: not a duration (e.g. \"5s\", \"168h\")", d.field, d.v)
		}
	}
	seen := map[string]bool{}
	for i, p := range c.Profiles {
		if p.Name == "" {
			return fmt.Errorf("profile #%d: name is required", i+1)
		}
		if seen[p.Name] {
			return fmt.Errorf("profile %q: defined more than once", p.Name)
		}
		seen[p.Name] = true
	}
	return nil
}

// FindProfile returns the profile with the given name.
func (c Config) FindProfile(name string) (Profile, bool) {
	for _, p := range c.Profiles {
		if p.Name == name {
			return p, true
		}
	}
	return Profile{}, false
}

// UpsertProfile replaces the profile with p.Name or appends p. It reports
// whether an existing profile was replaced.
func (c *Config) UpsertProfile(p Profile) bool {
	for i := range c.Profiles {
		if c.Profiles[i].Name == p.Name {
			c.Profiles[i] = p
			return true
		}
	}
	c.Profiles = append(c.Profiles, p)
	return false
}

// ParseDuration parses s, returning def when s is empty or invalid.
func ParseDuration(s string, def time.Duration) time.Duration {
	if s == "" {
		return def
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return def
	}
	return d
}

// ExpiryWarnDuration is ui.expiry_warn (default 168h).
func (u UI) ExpiryWarnDuration() time.Duration { return ParseDuration(u.ExpiryWarn, 168*time.Hour) }

// RefreshInterval is ui.refresh (default 5s).
func (u UI) RefreshInterval() time.Duration { return ParseDuration(u.Refresh, 5*time.Second) }

// RefreshIdleInterval is ui.refresh_idle (default 30s).
func (u UI) RefreshIdleInterval() time.Duration {
	return ParseDuration(u.RefreshIdle, 30*time.Second)
}

// CertWarnDuration is host.doctor.cert_warn (default 21 days).
func (d Doctor) CertWarnDuration() time.Duration { return ParseDuration(d.CertWarn, 504*time.Hour) }

// CertErrorDuration is host.doctor.cert_error (default 7 days).
func (d Doctor) CertErrorDuration() time.Duration {
	return ParseDuration(d.CertError, 168*time.Hour)
}

// SkewWarnDuration is host.doctor.skew_warn (default 2s).
func (d Doctor) SkewWarnDuration() time.Duration { return ParseDuration(d.SkewWarn, 2*time.Second) }

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// Bool returns a pointer to v, for building Profile tri-state fields.
func Bool(v bool) *bool { return &v }
