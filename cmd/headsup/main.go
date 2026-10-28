// Command headsup is a terminal UI for administering a headscale server. On the
// headscale host it needs no flags; elsewhere it uses the API with a key.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tonhe/headsup/internal/activity"
	"github.com/tonhe/headsup/internal/app"
	"github.com/tonhe/headsup/internal/brand"
	"github.com/tonhe/headsup/internal/config"
	"github.com/tonhe/headsup/internal/modules/registry"
	"github.com/tonhe/headsup/internal/theme"
	"github.com/tonhe/headsup/internal/version"

	_ "github.com/tonhe/headsup/internal/modules/all"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(brand.Name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	var f config.Flags
	var showVersion bool
	fs.StringVar(&f.Profile, "profile", "", "connection profile to use")
	fs.StringVar(&f.Profile, "p", "", "connection profile to use (shorthand)")
	fs.StringVar(&f.URL, "url", "", "headscale server URL (remote mode)")
	fs.StringVar(&f.APIKeyFile, "api-key-file", "", "file holding the API key (must not be group/world readable)")
	fs.StringVar(&f.Theme, "theme", "", "theme slug for this session")
	fs.StringVar(&f.Icons, "icons", "", "icon set: unicode, nerd, ascii")
	fs.BoolVar(&f.ReadOnly, "read-only", false, "disable every change")
	fs.BoolVar(&f.Insecure, "insecure", false, "skip TLS certificate verification (unsafe)")
	fs.BoolVar(&f.Demo, "demo", false, "run against built-in demo data")
	fs.BoolVar(&f.Host, "host", false, "require host mode (run on the headscale server)")
	fs.BoolVar(&f.Remote, "remote", false, "never use host mode")
	fs.StringVar(&f.HSConfig, "hs-config", "", "path to headscale's config.yaml (host mode)")
	fs.StringVar(&f.ConfigPath, "config", "", "path to "+brand.Name+"'s own config file")
	fs.BoolVar(&showVersion, "version", false, "print version and exit")
	fs.Usage = func() {
		fmt.Fprintf(stderr, "%s - a terminal UI for headscale\n\nUsage:\n  %s [flags]\n  %s version | host info | themes | config path\n\nFlags:\n",
			brand.Display, brand.Name, brand.Name)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if showVersion {
		fmt.Fprintln(stdout, brand.Name, version.String())
		return 0
	}

	cfgPath := f.ConfigPath
	if cfgPath == "" {
		cfgPath = config.ConfigFile()
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(stderr, "%s: reading %s: %v\n", brand.Name, cfgPath, err)
		return 1
	}
	if n, warns := theme.RegisterUserThemes(config.ThemesDir()); n >= 0 {
		for _, w := range warns {
			fmt.Fprintln(stderr, "theme:", w)
		}
	}

	if rest := fs.Args(); len(rest) > 0 {
		return subcommand(rest, cfg, f, stdout, stderr)
	}

	res, err := config.Resolve(cfg, f, os.Getenv)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", brand.Name, err)
		return 2
	}
	theme.ApplyProfile(theme.Profile())

	conn, err := connect(res, f, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", brand.Name, err)
		return 1
	}

	logger := activity.Logger(activity.Nop{})
	if !res.Demo {
		if l, err := activity.OpenDefault(); err == nil {
			logger = l
		} else {
			fmt.Fprintf(stderr, "%s: activity log disabled: %v\n", brand.Name, err)
		}
	}

	themeSlug := res.UI.Theme
	if res.Profile.Theme != "" {
		themeSlug = res.Profile.Theme
	}
	m := app.New(app.Options{
		Backend:      conn.backend,
		Host:         conn.host,
		Modules:      registry.All(cfg.Modules.Disabled),
		Profile:      conn.label,
		ServerURL:    conn.serverURL,
		ReadOnly:     res.Profile.ReadOnly || conn.readOnly,
		Theme:        themeSlug,
		Icons:        res.UI.Icons,
		Sidebar:      res.UI.Sidebar,
		Refresh:      res.UI.RefreshInterval(),
		RefreshIdle:  res.UI.RefreshIdleInterval(),
		ExpiryWarn:   res.UI.ExpiryWarnDuration(),
		KeyOverrides: cfg.Keys,
		Activity:     logger,
		Connector:    newConnector(&cfg, cfgPath, f, conn.host != nil),
	})
	opts := []tea.ProgramOption{tea.WithAltScreen()}
	if res.UI.Mouse {
		opts = append(opts, tea.WithMouseCellMotion())
	}
	if _, err := tea.NewProgram(m, opts...).Run(); err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", brand.Name, err)
		return 1
	}
	return 0
}

func subcommand(args []string, cfg config.Config, f config.Flags, stdout, stderr io.Writer) int {
	switch strings.Join(args, " ") {
	case "version":
		fmt.Fprintln(stdout, brand.Name, version.String())
		return 0
	case "config path":
		fmt.Fprintln(stdout, config.ConfigFile())
		return 0
	case "themes", "themes ls":
		for _, t := range theme.All() {
			kind := "dark"
			if t.Light {
				kind = "light"
			}
			src := ""
			if t.Source != "" {
				src = "  (" + t.Source + ")"
			}
			fmt.Fprintf(stdout, "%-28s %-5s %s%s\n", t.Slug, kind, t.Name, src)
		}
		return 0
	case "host info":
		return hostInfo(f, stdout)
	}
	fmt.Fprintf(stderr, "%s: unknown command %q (try --help)\n", brand.Name, strings.Join(args, " "))
	return 2
}
