// Command headsup is a terminal UI for administering a headscale server. On the
// headscale host it needs no flags; elsewhere it uses the API with a key.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tonhe/headsup/internal/activity"
	"github.com/tonhe/headsup/internal/app"
	"github.com/tonhe/headsup/internal/brand"
	"github.com/tonhe/headsup/internal/cli"
	"github.com/tonhe/headsup/internal/config"
	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/modules/registry"
	"github.com/tonhe/headsup/internal/policy"
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
		fmt.Fprintf(stderr, "%s - a terminal UI and CLI for headscale\n\nUsage:\n  %s [flags]                 launch the TUI\n  %s [flags] VERB ...        run a command (%s help lists them)\n\nFlags:\n",
			brand.Display, brand.Name, brand.Name, brand.Name)
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
		return subcommand(rest, cfg, cfgPath, f, stdout, stderr)
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
		Config:       &cfg,
		ConfigPath:   cfgPath,
		ThemesDir:    config.ThemesDir(),
		Mouse:        res.UI.Mouse,
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

// subcommand runs a CLI verb (contracts/cli.md) through internal/cli.
func subcommand(args []string, cfg config.Config, cfgPath string, f config.Flags, stdout, stderr io.Writer) int {
	hist := ""
	logger := activity.Logger(activity.Nop{})
	if !f.Demo {
		hist = policy.DefaultHistoryDir()
		if l, err := activity.OpenDefault(); err == nil {
			logger = l
		}
	}
	return cli.Run(args, cli.Options{
		Stdin:            os.Stdin,
		Stdout:           stdout,
		Stderr:           stderr,
		Interactive:      isTerminal(os.Stdin) && isTerminal(os.Stderr),
		Flags:            f,
		Config:           cfg,
		ConfigPath:       cfgPath,
		Connect:          cliConnect(cfg, stderr),
		DetectEnv:        func(f config.Flags) host.Env { return detectEnv(f, cfg.Host) },
		Activity:         logger,
		PolicyHistoryDir: hist,
		ThemesDir:        config.ThemesDir(),
	})
}

// cliConnect opens the same connection the TUI would for the given flags.
func cliConnect(cfg config.Config, stderr io.Writer) func(config.Flags) (cli.Conn, error) {
	return func(f config.Flags) (cli.Conn, error) {
		res, err := config.Resolve(cfg, f, os.Getenv)
		if err != nil {
			return cli.Conn{}, &cli.Error{Code: cli.ExitUsage, Msg: err.Error()}
		}
		c, err := connect(res, f, stderr)
		if err != nil {
			var he *hostErr
			if errors.As(err, &he) {
				code := cli.ExitNoHost
				if he.denied {
					code = cli.ExitPermission
				}
				return cli.Conn{}, &cli.Error{Code: code, Msg: err.Error()}
			}
			return cli.Conn{}, err
		}
		return cli.Conn{Backend: c.backend, Host: c.host, Label: c.label, ServerURL: c.serverURL,
			ReadOnly: res.Profile.ReadOnly || c.readOnly}, nil
	}
}
