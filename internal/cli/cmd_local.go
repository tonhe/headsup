package cli

import (
	"fmt"
	"sort"

	"github.com/tonhe/headsup/internal/brand"
	"github.com/tonhe/headsup/internal/config"
	"github.com/tonhe/headsup/internal/theme"
	"github.com/tonhe/headsup/internal/version"
)

// Verbs that need no server: version, themes, and the app's own config.

func cmdVersion(r *runner, args []string) error {
	v := r.newFlags("version", "")
	if _, err := v.args(args, 0, 0, "version"); err != nil {
		return err
	}
	if v.out == "" {
		fmt.Fprintln(r.stdout, brand.Name, version.String())
		return nil
	}
	type out struct {
		Name    string `json:"name" yaml:"name"`
		Version string `json:"version" yaml:"version"`
		Commit  string `json:"commit" yaml:"commit"`
		Build   string `json:"build" yaml:"build"`
	}
	o := out{Name: brand.Name, Version: version.Version, Commit: version.Commit, Build: version.Build}
	return emit(r.stdout, v.out, o, func(bool) table {
		return kvTable([2]string{"name", o.Name}, [2]string{"version", o.Version}, [2]string{"commit", o.Commit}, [2]string{"build", o.Build})
	})
}

type themeOut struct {
	Slug   string `json:"slug" yaml:"slug"`
	Name   string `json:"name" yaml:"name"`
	Light  bool   `json:"light" yaml:"light"`
	Source string `json:"source" yaml:"source"`
}

func themesLs(r *runner, args []string) error {
	v := r.newFlags("themes ls", "")
	if _, err := v.args(args, 0, 0, "themes ls"); err != nil {
		return err
	}
	var out []themeOut
	for _, t := range theme.All() {
		out = append(out, themeOut{Slug: t.Slug, Name: t.Name, Light: t.Light, Source: t.Source})
	}
	if v.out == "" {
		// The original listing, kept for compatibility.
		for _, t := range out {
			kind := "dark"
			if t.Light {
				kind = "light"
			}
			src := ""
			if t.Source != "" {
				src = "  (" + t.Source + ")"
			}
			fmt.Fprintf(r.stdout, "%-28s %-5s %s%s\n", t.Slug, kind, t.Name, src)
		}
		return nil
	}
	return emit(r.stdout, v.out, out, func(bool) table {
		t := table{head: []string{"SLUG", "KIND", "NAME", "SOURCE"}}
		for _, x := range out {
			kind := "dark"
			if x.Light {
				kind = "light"
			}
			t.add(x.Slug, kind, x.Name, x.Source)
		}
		return t
	})
}

func themesPreview(r *runner, args []string) error {
	v := r.newFlags("themes preview", fmtTable)
	pos, err := v.args(args, 1, 1, "themes preview SLUG")
	if err != nil {
		return err
	}
	t := theme.ByName(pos[0])
	if t == nil {
		return errorf(ExitNotFound, "no theme %q (see `%s themes ls`)", pos[0], brand.Name)
	}
	slots := []struct {
		name string
		val  string
	}{
		{"base00", string(t.Base00)}, {"base01", string(t.Base01)}, {"base02", string(t.Base02)}, {"base03", string(t.Base03)},
		{"base04", string(t.Base04)}, {"base05", string(t.Base05)}, {"base06", string(t.Base06)}, {"base07", string(t.Base07)},
		{"base08", string(t.Base08)}, {"base09", string(t.Base09)}, {"base0a", string(t.Base0A)}, {"base0b", string(t.Base0B)},
		{"base0c", string(t.Base0C)}, {"base0d", string(t.Base0D)}, {"base0e", string(t.Base0E)}, {"base0f", string(t.Base0F)},
	}
	pal := map[string]string{}
	for _, s := range slots {
		pal[s.name] = s.val
	}
	type out struct {
		themeOut `yaml:",inline"`
		Palette  map[string]string `json:"palette" yaml:"palette"`
	}
	o := out{themeOut{t.Slug, t.Name, t.Light, t.Source}, pal}
	return emit(r.stdout, v.out, o, func(bool) table {
		tb := table{head: []string{"SLOT", "COLOUR"}}
		for _, s := range slots {
			tb.add(s.name, s.val)
		}
		return tb
	})
}

func themesReload(r *runner, args []string) error {
	v := r.newFlags("themes reload", "")
	if _, err := v.args(args, 0, 0, "themes reload"); err != nil {
		return err
	}
	dir := r.o.ThemesDir
	if dir == "" {
		dir = config.ThemesDir()
	}
	loaded, warns := theme.LoadUserThemes(dir)
	for _, w := range warns {
		r.logf("theme: %s", w)
	}
	fmt.Fprintf(r.stdout, "%d user theme(s) in %s\n", len(loaded), dir)
	if len(warns) > 0 {
		return errorf(ExitInvalid, "%d theme file(s) have problems", len(warns))
	}
	return nil
}

func (r *runner) configPath() string {
	if r.o.ConfigPath != "" {
		return r.o.ConfigPath
	}
	return config.ConfigFile()
}

func configPath(r *runner, args []string) error {
	v := r.newFlags("config path", "")
	if _, err := v.args(args, 0, 0, "config path"); err != nil {
		return err
	}
	fmt.Fprintln(r.stdout, r.configPath())
	return nil
}

func (r *runner) saveConfig(cfg config.Config) error {
	if err := cfg.Validate(); err != nil {
		return errorf(ExitInvalid, "%v", err)
	}
	if err := config.Save(r.configPath(), cfg); err != nil {
		return err
	}
	r.o.Config = cfg
	return nil
}

func configTheme(r *runner, args []string) error {
	v := r.newFlags("config theme", "")
	pos, err := v.args(args, 1, 1, "config theme SLUG")
	if err != nil {
		return err
	}
	if theme.ByName(pos[0]) == nil {
		return errorf(ExitNotFound, "no theme %q (see `%s themes ls`)", pos[0], brand.Name)
	}
	cfg := r.o.Config
	cfg.UI.Theme = pos[0]
	if err := r.saveConfig(cfg); err != nil {
		return err
	}
	r.logf("Theme set to %s.", pos[0])
	return nil
}

func configProfile(r *runner, args []string) error {
	return sub(r, "config profile", args, group{
		"ls": profileLs, "list": profileLs, "add": profileAdd, "rm": profileRm, "delete": profileRm, "default": profileDefault,
	})
}

type profileOut struct {
	Name     string `json:"name" yaml:"name"`
	URL      string `json:"url" yaml:"url"`
	Key      string `json:"key_source" yaml:"key_source"`
	ReadOnly bool   `json:"read_only" yaml:"read_only"`
	Insecure bool   `json:"insecure" yaml:"insecure"`
	Default  bool   `json:"default" yaml:"default"`
}

func keySourceOf(p config.Profile) string {
	switch {
	case p.APIKeyEnv != "":
		return "env " + p.APIKeyEnv
	case p.APIKeyFile != "":
		return "file " + p.APIKeyFile
	case p.APIKeyCmd != "":
		return "command"
	case p.Keyring != nil && *p.Keyring:
		return "keyring"
	}
	return ""
}

func boolOf(b *bool) bool { return b != nil && *b }

func profileLs(r *runner, args []string) error {
	v := r.newFlags("config profile ls", fmtTable)
	if _, err := v.args(args, 0, 0, "config profile ls"); err != nil {
		return err
	}
	cfg := r.o.Config
	out := []profileOut{}
	for _, p := range cfg.Profiles {
		out = append(out, profileOut{Name: p.Name, URL: p.URL, Key: keySourceOf(p), ReadOnly: boolOf(p.ReadOnly),
			Insecure: boolOf(p.Insecure), Default: p.Name == cfg.DefaultProfile})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return emit(r.stdout, v.out, out, func(bool) table {
		t := table{head: []string{"NAME", "URL", "KEY", "READ-ONLY", "DEFAULT"}}
		for _, p := range out {
			def := ""
			if p.Default {
				def = "*"
			}
			t.add(p.Name, p.URL, p.Key, yesNo(p.ReadOnly), def)
		}
		return t
	})
}

func profileAdd(r *runner, args []string) error {
	v := r.newFlags("config profile add", "")
	var p config.Profile
	v.fs.StringVar(&p.APIKeyEnv, "api-key-env", "", "environment variable holding the key")
	v.fs.StringVar(&p.APIKeyCmd, "api-key-cmd", "", "command that prints the key")
	v.fs.StringVar(&p.CAFile, "ca-file", "", "CA bundle for the server's certificate")
	keyring := v.fs.Bool("keyring", false, "read the key from the OS keyring")
	pos, err := v.args(args, 1, 1, "config profile add NAME --url URL (--api-key-env VAR | --api-key-file PATH | --api-key-cmd CMD | --keyring) [--read-only] [--insecure]")
	if err != nil {
		return err
	}
	p.Name = pos[0]
	p.URL = r.flags.URL
	p.APIKeyFile = r.flags.APIKeyFile
	if p.URL == "" {
		return usagef("config profile add needs --url")
	}
	if *keyring {
		p.Keyring = config.Bool(true)
	}
	if keySourceOf(p) == "" {
		return usagef("give a key source: --api-key-env, --api-key-file, --api-key-cmd or --keyring (keys are never stored in the config)")
	}
	if r.flags.ReadOnly {
		p.ReadOnly = config.Bool(true)
	}
	if r.flags.Insecure {
		p.Insecure = config.Bool(true)
	}
	cfg := r.o.Config
	cfg.Profiles = append([]config.Profile(nil), cfg.Profiles...)
	replaced := cfg.UpsertProfile(p)
	if cfg.DefaultProfile == "" {
		cfg.DefaultProfile = p.Name
	}
	if err := r.saveConfig(cfg); err != nil {
		return err
	}
	if replaced {
		r.logf("Updated profile %s.", p.Name)
	} else {
		r.logf("Added profile %s.", p.Name)
	}
	return nil
}

func profileRm(r *runner, args []string) error {
	v := r.newFlags("config profile rm", "")
	pos, err := v.args(args, 1, 1, "config profile rm NAME")
	if err != nil {
		return err
	}
	cfg := r.o.Config
	var keep []config.Profile
	for _, p := range cfg.Profiles {
		if p.Name != pos[0] {
			keep = append(keep, p)
		}
	}
	if len(keep) == len(cfg.Profiles) {
		return errorf(ExitNotFound, "no profile %q", pos[0])
	}
	cfg.Profiles = keep
	if cfg.DefaultProfile == pos[0] {
		cfg.DefaultProfile = ""
	}
	if err := r.saveConfig(cfg); err != nil {
		return err
	}
	r.logf("Removed profile %s.", pos[0])
	return nil
}

func profileDefault(r *runner, args []string) error {
	v := r.newFlags("config profile default", "")
	pos, err := v.args(args, 1, 1, "config profile default NAME")
	if err != nil {
		return err
	}
	cfg := r.o.Config
	if _, ok := cfg.FindProfile(pos[0]); !ok {
		return errorf(ExitNotFound, "no profile %q", pos[0])
	}
	cfg.DefaultProfile = pos[0]
	if err := r.saveConfig(cfg); err != nil {
		return err
	}
	r.logf("Default profile is now %s.", pos[0])
	return nil
}
