package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/tonhe/headsup/internal/brand"
)

// handler runs one verb with the arguments after it.
type handler func(r *runner, args []string) error

// group is a noun with sub-verbs ("nodes" → "ls", "show", ...).
type group map[string]handler

// groups is the verb tree (contracts/cli.md). Single-word verbs are groups
// with a "" entry.
func groups() map[string]group {
	return map[string]group{
		"version": {"": cmdVersion},
		"nodes": {"ls": nodesLs, "list": nodesLs, "show": nodesShow, "rename": nodesRename, "tags": nodesTags,
			"expire": nodesExpire, "delete": nodesDelete, "rm": nodesDelete, "register": nodesRegister},
		"routes": {"ls": routesLs, "list": routesLs, "approve": routesApprove, "revoke": routesRevoke},
		"users": {"ls": usersLs, "list": usersLs, "create": usersCreate, "rename": usersRename,
			"delete": usersDelete, "rm": usersDelete},
		"keys":    {"preauth": keysPreauth, "api": keysAPI},
		"policy":  {"get": policyGet, "check": policyCheck, "diff": policyDiff, "set": policySet, "history": policyHistory, "rollback": policyRollback},
		"auth":    {"approve": authApprove, "reject": authReject},
		"server":  {"info": serverInfo, "caps": serverCaps, "backfill-ips": serverBackfill},
		"host":    {"info": hostInfo, "doctor": hostDoctor, "perms": hostPerms},
		"svc":     {"status": svcStatus, "start": svcOp("service.start"), "stop": svcOp("service.stop"), "restart": svcOp("service.restart"), "reload": svcOp("service.reload"), "logs": svcLogs},
		"conf":    {"get": confGet, "set": confSet, "diff": confDiff, "edit": tuiOnly("conf edit"), "migrate": confMigrate, "rollback": tuiOnly("conf rollback")},
		"dns":     {"show": dnsShow, "records": dnsRecords, "set": dnsSet},
		"derp":    {"show": derpShow, "regions": derpRegions, "map": tuiOnly("derp map edit")},
		"backup":  {"create": backupCreate, "ls": backupLs, "list": backupLs, "restore": backupRestore},
		"upgrade": {"check": upgradeCheck},
		"themes":  {"": themesLs, "ls": themesLs, "list": themesLs, "preview": themesPreview, "reload": themesReload},
		"config":  {"path": configPath, "theme": configTheme, "profile": configProfile},
	}
}

// aliases map the alternative nouns people type.
var aliases = map[string]string{
	"node": "nodes", "machines": "nodes", "machine": "nodes", "route": "routes", "user": "users",
	"key": "keys", "service": "svc", "theme": "themes",
}

func (r *runner) dispatch(args []string) error {
	if len(args) == 0 {
		r.usage(r.stderr)
		return usagef("no command given")
	}
	noun := args[0]
	if a, ok := aliases[noun]; ok {
		noun = a
	}
	switch noun {
	case "help", "-h", "--help":
		r.usage(r.stdout)
		return nil
	}
	g, ok := groups()[noun]
	if !ok {
		return usagef("unknown command %q (try `%s help`)", args[0], brand.Name)
	}
	rest := args[1:]
	if h, ok := g[""]; ok && (len(rest) == 0 || strings.HasPrefix(rest[0], "-") || g[rest[0]] == nil) {
		return h(r, rest)
	}
	if len(rest) == 0 {
		return usagef("%s needs a sub-command: %s", noun, subs(g))
	}
	h, ok := g[rest[0]]
	if !ok {
		return usagef("unknown %s command %q (one of: %s)", noun, rest[0], subs(g))
	}
	return h(r, rest[1:])
}

func subs(g group) string {
	var out []string
	for k := range g {
		if k != "" {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

// sub dispatches a third-level verb ("keys preauth ls").
func sub(r *runner, name string, args []string, g group) error {
	if len(args) == 0 {
		return usagef("%s needs a sub-command: %s", name, subs(g))
	}
	h, ok := g[args[0]]
	if !ok {
		return usagef("unknown %s command %q (one of: %s)", name, args[0], subs(g))
	}
	return h(r, args[1:])
}

func tuiOnly(name string) handler {
	return func(*runner, []string) error {
		return errorf(ExitUnsupported, "`%s` is interactive; use the %s TUI for it", name, brand.Name)
	}
}

// verbFlags is the flag set of one verb: the output format, --yes, and the
// connection flags, which may also follow the verb.
type verbFlags struct {
	fs  *flag.FlagSet
	out string
	yes bool
}

func (r *runner) newFlags(name, defOut string) *verbFlags {
	v := &verbFlags{fs: flag.NewFlagSet(brand.Name+" "+name, flag.ContinueOnError)}
	v.fs.SetOutput(r.stderr)
	v.fs.StringVar(&v.out, "o", defOut, "output format: table, wide, json, yaml")
	v.fs.StringVar(&v.out, "output", defOut, "output format (same as -o)")
	v.fs.BoolVar(&v.yes, "yes", false, "do not ask for confirmation")
	v.fs.BoolVar(&v.yes, "y", false, "do not ask for confirmation (shorthand)")
	f := &r.flags
	v.fs.StringVar(&f.Profile, "profile", f.Profile, "connection profile")
	v.fs.StringVar(&f.Profile, "p", f.Profile, "connection profile (shorthand)")
	v.fs.StringVar(&f.URL, "url", f.URL, "headscale server URL")
	v.fs.StringVar(&f.APIKeyFile, "api-key-file", f.APIKeyFile, "file holding the API key")
	v.fs.BoolVar(&f.ReadOnly, "read-only", f.ReadOnly, "refuse every change")
	v.fs.BoolVar(&f.Insecure, "insecure", f.Insecure, "skip TLS verification (unsafe)")
	v.fs.BoolVar(&f.Demo, "demo", f.Demo, "use built-in demo data")
	v.fs.BoolVar(&f.Host, "host", f.Host, "require host mode")
	v.fs.BoolVar(&f.Remote, "remote", f.Remote, "never use host mode")
	v.fs.StringVar(&f.HSConfig, "hs-config", f.HSConfig, "headscale config.yaml path")
	return v
}

// parse accepts flags before, between and after positional arguments.
func (v *verbFlags) parse(args []string) ([]string, error) {
	var pos []string
	for {
		if err := v.fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil, &Error{Code: ExitOK}
			}
			return nil, usagef("%v", err)
		}
		rest := v.fs.Args()
		if len(rest) == 0 {
			return pos, nil
		}
		// After "--" everything is positional.
		consumed := len(args) - len(rest)
		if consumed > 0 && args[consumed-1] == "--" {
			return append(pos, rest...), nil
		}
		pos = append(pos, rest[0])
		args = rest[1:]
	}
}

// args parses and checks the positional count (max < 0 means no limit).
func (v *verbFlags) args(in []string, min, max int, usage string) ([]string, error) {
	pos, err := v.parse(in)
	if err != nil {
		return nil, err
	}
	if len(pos) < min || (max >= 0 && len(pos) > max) {
		return nil, usagef("usage: %s %s", brand.Name, usage)
	}
	return pos, nil
}

func (r *runner) usage(w io.Writer) {
	fmt.Fprintf(w, `%s - a terminal UI and CLI for headscale

Usage:
  %[2]s                                  launch the TUI
  %[2]s nodes   ls [--user U] [--filter Q] | show ID|NAME | rename ID NEW
  %[2]s nodes   tags ID [--set a,b | --add a | --rm a] | expire ID [--disable]
  %[2]s nodes   delete ID [--yes] | register --user U KEY|URL
  %[2]s routes  ls [--pending] | approve ID PREFIX... | revoke ID PREFIX...
  %[2]s users   ls | create NAME [--display-name N --email E] | rename ID NEW | delete ID
  %[2]s keys    preauth ls|create|expire|delete   (create: --user --reusable --ephemeral --expiration 24h --tags a,b)
  %[2]s keys    api ls|create|expire|delete       (create: --expiration 90d)
  %[2]s policy  get [-o raw|json] | check FILE | diff FILE | set FILE | history | rollback N
  %[2]s auth    approve AUTH_ID [--user U] | reject AUTH_ID
  %[2]s server  info | caps | backfill-ips
  %[2]s host    info | doctor | perms
  %[2]s svc     status | start | stop | restart | reload | logs [-f] [--level warn] [--grep RE]
  %[2]s conf    get KEY | set KEY VALUE [--no-restart] | diff | migrate
  %[2]s dns     show | records ls|add NAME TYPE VALUE|rm NAME | set KEY VALUE
  %[2]s derp    show | regions ls
  %[2]s backup  create | ls | restore ID [--files-only]
  %[2]s upgrade check
  %[2]s themes  ls | preview SLUG | reload
  %[2]s config  path | theme SLUG | profile add|ls|rm|default
  %[2]s version

Every verb takes -o table|wide|json|yaml and the connection flags (--profile,
--url, --demo, --host, --remote, --read-only). Destructive verbs ask on a
terminal; pass --yes in scripts (without it they exit 7).

Exit codes: 0 ok, 1 error, 2 usage, 3 unauthorized or read-only, 4 not found,
5 unsupported, 6 validation failed, 7 not confirmed, 8 no host mode,
9 host permission denied.
`, brand.Display, brand.Name)
}

// multiFlag collects a repeatable flag; each value may be comma-separated.
type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(s string) error { *m = append(*m, s); return nil }

func (m multiFlag) list() []string {
	var out []string
	for _, v := range m {
		out = append(out, splitList(v)...)
	}
	return out
}

// flagGiven reports whether a flag was set on the command line.
func flagGiven(v *verbFlags, name string) bool {
	given := false
	v.fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			given = true
		}
	})
	return given
}
