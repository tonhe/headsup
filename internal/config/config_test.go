package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"

	"github.com/tonhe/fathom/internal/api"
	"github.com/tonhe/fathom/internal/brand"
)

func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

const sampleTOML = `
default_profile = "homelab"

[defaults]
theme = "nord"
read_only = true

[ui]
theme = "dracula"
refresh = "10s"

[host]
enabled = "never"
headscale_config = "/etc/headscale/config.yaml"

[host.doctor]
cert_warn = "72h"

[[profile]]
name = "homelab"
url = "https://hs.example.com"
keyring = true
read_only = false

[[profile]]
name = "work"
url = "https://headscale.corp.internal"
api_key_cmd = "op read x"
theme = "gruvbox-dark"
insecure = true
`

func loadSample(t *testing.T) Config {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte(sampleTOML), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestLoadMissingFileReturnsDefault(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil {
		t.Fatal(err)
	}
	d := Default()
	if cfg.UI.Theme != d.UI.Theme || cfg.Host.BackupKeep != 50 || !cfg.UI.Mouse {
		t.Fatalf("not default: %+v", cfg.UI)
	}
	if cfg.UI.RefreshInterval() != 5*time.Second || cfg.Host.Doctor.CertWarnDuration() != 504*time.Hour {
		t.Fatal("default durations wrong")
	}
}

func TestLoadOverlaysDefaults(t *testing.T) {
	cfg := loadSample(t)
	if cfg.UI.Theme != "dracula" || cfg.UI.Icons != "unicode" || !cfg.UI.Mouse {
		t.Fatalf("ui overlay: %+v", cfg.UI)
	}
	if cfg.UI.RefreshInterval() != 10*time.Second {
		t.Fatal("refresh")
	}
	if cfg.Host.Doctor.CertWarnDuration() != 72*time.Hour || cfg.Host.Doctor.WALWarnMB != 64 {
		t.Fatalf("doctor: %+v", cfg.Host.Doctor)
	}
	if len(cfg.Profiles) != 2 {
		t.Fatalf("profiles: %d", len(cfg.Profiles))
	}
}

func TestLoadInvalid(t *testing.T) {
	cases := map[string]string{
		"bad enum":     "[ui]\nicons = \"emoji\"\n",
		"bad duration": "[ui]\nrefresh = \"soon\"\n",
		"dup profile":  "[[profile]]\nname=\"a\"\n[[profile]]\nname=\"a\"\n",
		"bad toml":     "[ui\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "c.toml")
			os.WriteFile(p, []byte(body), 0o600)
			if _, err := Load(p); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestResolvePrecedence(t *testing.T) {
	cfg := loadSample(t)
	tests := []struct {
		name     string
		flags    Flags
		env      map[string]string
		wantProf string
		wantURL  string
		wantKey  KeySourceKind
		wantTh   string
		wantRO   bool
		wantIns  bool
		wantHost HostPreference
		wantHS   string
		explicit bool
		wantErr  bool
	}{
		{name: "default profile", wantProf: "homelab", wantURL: "https://hs.example.com",
			wantKey: KeyKeyring, wantTh: "nord", wantRO: false, wantHost: HostNever,
			wantHS: "/etc/headscale/config.yaml"},
		{name: "env profile", env: map[string]string{brand.EnvPrefix + "_PROFILE": "work"},
			wantProf: "work", wantURL: "https://headscale.corp.internal", wantKey: KeyCmd,
			wantTh: "gruvbox-dark", wantRO: true, wantIns: true, wantHost: HostNever,
			wantHS: "/etc/headscale/config.yaml", explicit: true},
		{name: "flag beats env profile", flags: Flags{Profile: "homelab"},
			env:      map[string]string{brand.EnvPrefix + "_PROFILE": "work"},
			wantProf: "homelab", wantURL: "https://hs.example.com", wantKey: KeyKeyring,
			wantTh: "nord", wantHost: HostNever, wantHS: "/etc/headscale/config.yaml", explicit: true},
		{name: "flag theme beats env theme beats profile",
			flags:    Flags{Profile: "work", Theme: "flagtheme"},
			env:      map[string]string{brand.EnvPrefix + "_THEME": "envtheme"},
			wantProf: "work", wantURL: "https://headscale.corp.internal", wantKey: KeyCmd,
			wantTh: "flagtheme", wantRO: true, wantIns: true, wantHost: HostNever,
			wantHS: "/etc/headscale/config.yaml", explicit: true},
		{name: "env theme", env: map[string]string{brand.EnvPrefix + "_THEME": "envtheme"},
			wantProf: "homelab", wantURL: "https://hs.example.com", wantKey: KeyKeyring,
			wantTh: "envtheme", wantHost: HostNever, wantHS: "/etc/headscale/config.yaml"},
		{name: "ad-hoc url skips default profile",
			env:      map[string]string{"HEADSCALE_URL": "https://other", "HEADSCALE_API_KEY": "x"},
			wantProf: "", wantURL: "https://other", wantKey: KeyEnv, wantTh: "nord",
			wantRO: true, wantHost: HostNever, wantHS: "/etc/headscale/config.yaml", explicit: true},
		{name: "flag url overrides profile url", flags: Flags{Profile: "work", URL: "https://flag"},
			env:      map[string]string{"HEADSCALE_URL": "https://env"},
			wantProf: "work", wantURL: "https://flag", wantKey: KeyCmd, wantTh: "gruvbox-dark",
			wantRO: true, wantIns: true, wantHost: HostNever, wantHS: "/etc/headscale/config.yaml", explicit: true},
		{name: "flag key file beats env key", flags: Flags{Profile: "homelab", APIKeyFile: "/k"},
			env:      map[string]string{"HEADSCALE_API_KEY": "x"},
			wantProf: "homelab", wantURL: "https://hs.example.com", wantKey: KeyFile, wantTh: "nord",
			wantHost: HostNever, wantHS: "/etc/headscale/config.yaml", explicit: true},
		{name: "flags force ro, insecure, host, hs-config",
			flags:    Flags{ReadOnly: true, Insecure: true, Host: true, HSConfig: "/x.yaml"},
			env:      map[string]string{"HEADSCALE_CONFIG": "/env.yaml"},
			wantProf: "homelab", wantURL: "https://hs.example.com", wantKey: KeyKeyring, wantTh: "nord",
			wantRO: true, wantIns: true, wantHost: HostAlways, wantHS: "/x.yaml"},
		{name: "env hs config", env: map[string]string{"HEADSCALE_CONFIG": "/env.yaml"}, flags: Flags{Remote: true},
			wantProf: "homelab", wantURL: "https://hs.example.com", wantKey: KeyKeyring, wantTh: "nord",
			wantHost: HostNever, wantHS: "/env.yaml"},
		{name: "unknown profile", flags: Flags{Profile: "nope"}, wantErr: true},
		{name: "host and remote", flags: Flags{Host: true, Remote: true}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := Resolve(cfg, tt.flags, envMap(tt.env))
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			p := r.Profile
			if p.Name != tt.wantProf || p.URL != tt.wantURL || p.Key.Kind != tt.wantKey ||
				p.Theme != tt.wantTh || p.ReadOnly != tt.wantRO || p.Insecure != tt.wantIns ||
				r.Host != tt.wantHost || r.HSConfig != tt.wantHS || r.Explicit != tt.explicit {
				t.Fatalf("got %+v host=%v hs=%q explicit=%v", p, r.Host, r.HSConfig, r.Explicit)
			}
			if r.UI.Theme != tt.wantTh {
				t.Fatalf("ui theme %q", r.UI.Theme)
			}
		})
	}
}

func TestResolveBuiltinWhenEmpty(t *testing.T) {
	r, err := Resolve(Default(), Flags{Icons: "ascii"}, envMap(nil))
	if err != nil {
		t.Fatal(err)
	}
	if r.Profile.Name != "" || r.Profile.Key.Kind != KeyNone || r.Host != HostAuto || r.Explicit {
		t.Fatalf("%+v", r)
	}
	if r.UI.Theme != Default().UI.Theme || r.UI.Icons != "ascii" {
		t.Fatalf("ui %+v", r.UI)
	}
}

func TestEnvAliases(t *testing.T) {
	for _, tc := range []struct {
		env     map[string]string
		url, kv string
	}{
		{map[string]string{brand.EnvPrefix + "_URL": "a", "HEADSCALE_URL": "b", "HEADSCALE_CLI_ADDRESS": "c"}, "a", ""},
		{map[string]string{"HEADSCALE_URL": "b", "HEADSCALE_CLI_ADDRESS": "c"}, "b", ""},
		{map[string]string{"HEADSCALE_CLI_ADDRESS": "c"}, "c", ""},
		{map[string]string{"HEADSCALE_URL": "u", brand.EnvPrefix + "_API_KEY": "1", "HEADSCALE_API_KEY": "2"}, "u", brand.EnvPrefix + "_API_KEY"},
		{map[string]string{"HEADSCALE_URL": "u", "HEADSCALE_API_KEY": "2", "HEADSCALE_CLI_API_KEY": "3"}, "u", "HEADSCALE_API_KEY"},
		{map[string]string{"HEADSCALE_URL": "u", "HEADSCALE_CLI_API_KEY": "3"}, "u", "HEADSCALE_CLI_API_KEY"},
	} {
		r, err := Resolve(Default(), Flags{}, envMap(tc.env))
		if err != nil {
			t.Fatal(err)
		}
		if r.Profile.URL != tc.url || r.Profile.Key.Env != tc.kv {
			t.Errorf("env %v: url=%q keyenv=%q", tc.env, r.Profile.URL, r.Profile.Key.Env)
		}
	}
	r, _ := Resolve(Default(), Flags{}, envMap(map[string]string{"NO_COLOR": "1"}))
	if !r.NoColor {
		t.Error("NO_COLOR not honoured")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sub")
	path := filepath.Join(dir, "config.toml")
	cfg := Default()
	cfg.DefaultProfile = "work"
	cfg.Keys["machines.expire"] = []string{"e"}
	cfg.UpsertProfile(Profile{Name: "work", URL: "https://a", Keyring: Bool(true), ReadOnly: Bool(true)})
	if replaced := cfg.UpsertProfile(Profile{Name: "work", URL: "https://b", Keyring: Bool(true)}); !replaced {
		t.Fatal("upsert should replace")
	}
	cfg.UpsertProfile(Profile{Name: "lab", URL: "https://lab"})
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.DefaultProfile != "work" || len(got.Profiles) != 2 || got.Profiles[0].URL != "https://b" ||
		got.Profiles[0].ReadOnly != nil || got.Keys["machines.expire"][0] != "e" || got.UI.Theme != cfg.UI.Theme {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(path)
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("file mode %v", fi.Mode().Perm())
		}
		di, _ := os.Stat(dir)
		if di.Mode().Perm() != 0o700 {
			t.Fatalf("dir mode %v", di.Mode().Perm())
		}
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Fatalf("temp files left behind: %v", ents)
	}
}

func TestSecretsNeverFormatted(t *testing.T) {
	const raw = "hskey-api-SUPERSECRETVALUE123456"
	cfg := Default()
	cfg.UpsertProfile(Profile{Name: "p", URL: "https://x", APIKeyEnv: "MY_KEY"})
	env := envMap(map[string]string{"MY_KEY": raw, "HEADSCALE_API_KEY": raw})
	for _, f := range []Flags{{Profile: "p"}, {}} {
		r, err := Resolve(cfg, f, env)
		if err != nil {
			t.Fatal(err)
		}
		out := fmt.Sprintf("%v %+v %#v", r, r, r)
		if strings.Contains(out, raw) || strings.Contains(out, "SUPERSECRET") {
			t.Fatalf("secret leaked: %s", out)
		}
		s, err := ResolveKey(r.Profile, env)
		if err != nil {
			t.Fatal(err)
		}
		if s.Reveal() != raw {
			t.Fatal("wrong key")
		}
		if out := fmt.Sprintf("%v %+v %#v %s", s, s, s, s); strings.Contains(out, "SUPERSECRET") {
			t.Fatalf("secret leaked: %s", out)
		}
	}
}

func TestResolveKeySources(t *testing.T) {
	keyring.MockInit()
	dir := t.TempDir()
	good := filepath.Join(dir, "good")
	os.WriteFile(good, []byte("hskey-file-abc\n"), 0o600)
	loose := filepath.Join(dir, "loose")
	os.WriteFile(loose, []byte("hskey-file-abc\n"), 0o600)
	os.Chmod(loose, 0o644)

	if err := StoreKeyring("lab", api.NewSecret("hskey-ring-xyz")); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		p       ResolvedProfile
		want    string
		wantErr string
	}{
		{"env", ResolvedProfile{Key: KeySource{Kind: KeyEnv, Env: "K"}}, "envval", ""},
		{"env unset", ResolvedProfile{Key: KeySource{Kind: KeyEnv, Env: "UNSET"}}, "", "UNSET"},
		{"file", ResolvedProfile{Key: KeySource{Kind: KeyFile, File: good}}, "hskey-file-abc", ""},
		{"cmd", ResolvedProfile{Key: KeySource{Kind: KeyCmd, Cmd: "printf '  hskey-cmd-1\\n'"}}, "hskey-cmd-1", ""},
		{"cmd fails", ResolvedProfile{Key: KeySource{Kind: KeyCmd, Cmd: "echo nope >&2; exit 3"}}, "", "nope"},
		{"keyring", ResolvedProfile{Name: "lab", Key: KeySource{Kind: KeyKeyring}}, "hskey-ring-xyz", ""},
		{"keyring missing", ResolvedProfile{Name: "other", Key: KeySource{Kind: KeyKeyring}}, "", "keyring"},
		{"none", ResolvedProfile{}, "", "no API key"},
	}
	if runtime.GOOS != "windows" {
		tests = append(tests, struct {
			name    string
			p       ResolvedProfile
			want    string
			wantErr string
		}{"loose file", ResolvedProfile{Key: KeySource{Kind: KeyFile, File: loose}}, "", "chmod 600 " + loose})
	}
	env := envMap(map[string]string{"K": "envval"})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := ResolveKey(tt.p, env)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
				}
				if strings.Contains(err.Error(), "hskey-file-abc") {
					t.Fatal("error leaks secret")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if s.Reveal() != tt.want {
				t.Fatalf("got %q", s.Masked())
			}
		})
	}
}

func TestKeyCmdTimeout(t *testing.T) {
	old := KeyCmdTimeout
	KeyCmdTimeout = 100 * time.Millisecond
	defer func() { KeyCmdTimeout = old }()
	_, err := ResolveKey(ResolvedProfile{Key: KeySource{Kind: KeyCmd, Cmd: "sleep 5"}}, envMap(nil))
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v", err)
	}
}

func TestPaths(t *testing.T) {
	oldEnv, oldHome, oldOS := getenv, userHomeDir, goos
	defer func() { getenv, userHomeDir, goos = oldEnv, oldHome, oldOS }()
	userHomeDir = func() (string, error) { return "/home/u", nil }
	tests := []struct {
		name          string
		os            string
		env           map[string]string
		config, state string
	}{
		{"xdg", "linux", map[string]string{"XDG_CONFIG_HOME": "/c", "XDG_STATE_HOME": "/s"},
			filepath.Join("/c", brand.Name), filepath.Join("/s", brand.Name)},
		{"defaults", "linux", nil,
			filepath.Join("/home/u", ".config", brand.Name), filepath.Join("/home/u", ".local", "state", brand.Name)},
		{"windows", "windows", map[string]string{"APPDATA": "/ad", "LOCALAPPDATA": "/lad"},
			filepath.Join("/ad", brand.Name), filepath.Join("/lad", brand.Name, "state")},
		{"windows xdg wins", "windows", map[string]string{"APPDATA": "/ad", "XDG_CONFIG_HOME": "/c"},
			filepath.Join("/c", brand.Name), filepath.Join("/home/u", ".local", "state", brand.Name)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			goos = tt.os
			getenv = envMap(tt.env)
			if ConfigDir() != tt.config || StateDir() != tt.state {
				t.Fatalf("config=%q state=%q", ConfigDir(), StateDir())
			}
			if ThemesDir() != filepath.Join(tt.config, "themes") || ConfigFile() != filepath.Join(tt.config, "config.toml") {
				t.Fatal("derived paths")
			}
		})
	}
	if ExpandHome("~/x") != filepath.Join("/home/u", "x") || ExpandHome("/abs") != "/abs" {
		t.Fatal("ExpandHome")
	}
}
