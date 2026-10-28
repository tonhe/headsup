package execcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tonhe/headsup/internal/api"
	"github.com/tonhe/headsup/internal/api/apitest"
	"github.com/tonhe/headsup/internal/api/fake"
)

var errExit = errors.New("exit status 1")

// cliSim emulates `headscale ... -o json` for one version on top of a fake.
type cliSim struct {
	fb    *fake.Backend
	v029  bool
	style apitest.Style

	mu   sync.Mutex
	argv [][]string
}

func newCLISim(version string) *cliSim {
	s := &cliSim{fb: fake.Demo(fake.WithVersion(version)), v029: !api.VersionAtLeast(version, 0, 30)}
	s.style = apitest.StyleV030
	if s.v029 {
		s.style = apitest.StyleV029CLI
	}
	return s
}

// parsed is a command line split into the subcommand path, flags and args.
type parsed struct {
	cmd   string
	flags map[string]string
	pos   []string
}

var boolFlags = map[string]bool{"--force": true, "--reusable": true, "--ephemeral": true}

func parse(argv []string) parsed {
	// Strip the program prefix: "headscale" or "<rt> exec <ctr> headscale".
	i := 1
	if argv[0] == "docker" || argv[0] == "podman" {
		i = 4
	}
	rest := argv[i:]
	p := parsed{flags: map[string]string{}}
	var path []string
	for j := 0; j < len(rest); j++ {
		a := rest[j]
		switch {
		case strings.HasPrefix(a, "-"):
			if k, v, ok := strings.Cut(a, "="); ok {
				p.flags[k] = v
				continue
			}
			isBool := boolFlags[a] || (a == "-d" && strings.Join(path, " ") == "nodes expire")
			if isBool || j+1 >= len(rest) {
				p.flags[a] = "true"
				continue
			}
			p.flags[a] = rest[j+1]
			j++
		case len(path) == 0 || (len(path) == 1 && path[0] != "version" && path[0] != "health"):
			path = append(path, a)
		default:
			p.pos = append(p.pos, a)
		}
	}
	p.cmd = strings.Join(path, " ")
	return p
}

func (s *cliSim) out(v any) []byte {
	b, _ := json.MarshalIndent(v, "", "\t")
	return append(b, '\n')
}

func (s *cliSim) fail(what string, err error) ([]byte, []byte, error) {
	e := api.AsError(err)
	var msg string
	if s.v029 {
		code := map[api.ErrorKind]string{
			api.KindNotFound: "NotFound", api.KindInvalid: "InvalidArgument", api.KindConflict: "AlreadyExists",
			api.KindUnauthorized: "Unauthenticated", api.KindUnsupported: "Unimplemented", api.KindUnreachable: "Unavailable",
		}[e.Kind]
		if code == "" {
			code = "Unknown"
		}
		msg = fmt.Sprintf("%s: rpc error: code = %s desc = %s", what, code, e.Message)
	} else {
		st := map[api.ErrorKind]int{
			api.KindNotFound: 404, api.KindInvalid: 400, api.KindConflict: 409,
			api.KindUnauthorized: 401, api.KindUnsupported: 501, api.KindUnreachable: 503,
		}[e.Kind]
		if st == 0 {
			st = 500
		}
		msg = fmt.Sprintf("%s: %d %s: %s", what, st, http.StatusText(st), e.Message)
	}
	return nil, s.out(map[string]string{"error": msg}), errExit
}

func u64(s string) uint64 { n, _ := strconv.ParseUint(s, 10, 64); return n }

func csv(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

func parseDur(s string) time.Duration {
	d, _ := time.ParseDuration(s)
	return d
}

func (s *cliSim) run(ctx context.Context, argv ...string) ([]byte, []byte, error) {
	s.mu.Lock()
	s.argv = append(s.argv, argv)
	s.mu.Unlock()
	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}
	p := parse(argv)
	if p.flags["-o"] != "json" {
		return nil, []byte("Error: expected -o json"), errExit
	}
	fb, st := s.fb, s.style
	node := func(what string, n api.Node, err error) ([]byte, []byte, error) {
		if err != nil {
			return s.fail(what, err)
		}
		return s.out(st.Node(n)), nil, nil
	}
	done := func(what string, err error) ([]byte, []byte, error) {
		if err != nil {
			return s.fail(what, err)
		}
		return s.out(map[string]any{}), nil, nil
	}
	f := p.flags
	switch p.cmd {
	case "version":
		return s.out(map[string]any{"version": fb.Version(), "commit": "abc", "dirty": false}), nil, nil
	case "health":
		return s.out(map[string]any{st.Key("databaseConnectivity", "database_connectivity"): true}), nil, nil
	case "nodes list":
		ns, err := fb.ListNodes(ctx, api.NodeFilter{User: f["-u"]})
		if err != nil {
			return s.fail("listing nodes", err)
		}
		return s.out(st.Nodes(ns)), nil, nil
	case "nodes register":
		n, err := fb.RegisterNode(ctx, f["-u"], f["-k"])
		return node("registering node", n, err)
	case "nodes rename":
		name := ""
		if len(p.pos) > 0 {
			name = p.pos[0]
		}
		n, err := fb.RenameNode(ctx, u64(f["-i"]), name)
		return node("renaming node", n, err)
	case "nodes tag":
		n, err := fb.SetTags(ctx, u64(f["-i"]), csv(f["--tags"]))
		return node("setting tags", n, err)
	case "nodes approve-routes":
		var ps []netip.Prefix
		for _, r := range csv(f["--routes"]) {
			ps = append(ps, netip.MustParsePrefix(r))
		}
		n, err := fb.SetApprovedRoutes(ctx, u64(f["-i"]), ps)
		return node("setting routes", n, err)
	case "nodes expire":
		opt := api.ExpireOpts{DisableExpiry: f["-d"] == "true"}
		if e := f["-e"]; e != "" {
			t, err := time.Parse(time.RFC3339, e)
			if err != nil {
				return s.fail("parsing expiry time", api.Errf(api.KindInvalid, "%v", err))
			}
			opt.At = t
		}
		n, err := fb.ExpireNode(ctx, u64(f["-i"]), opt)
		return node("expiring node", n, err)
	case "nodes delete":
		if f["--force"] != "true" {
			return s.out(map[string]string{"Result": "Node not deleted"}), nil, nil
		}
		return done("deleting node", fb.DeleteNode(ctx, u64(f["-i"])))
	case "nodes backfillips":
		ch, err := fb.BackfillIPs(ctx)
		if err != nil {
			return s.fail("backfilling IPs", err)
		}
		return s.out(map[string]any{"changes": ch}), nil, nil
	case "users list":
		us, err := fb.ListUsers(ctx, api.UserFilter{ID: u64(f["-i"]), Name: f["-n"], Email: f["-e"]})
		if err != nil {
			return s.fail("listing users", err)
		}
		return s.out(st.Users(us)), nil, nil
	case "users create":
		u, err := fb.CreateUser(ctx, api.NewUser{Name: p.pos[0], DisplayName: f["-d"], Email: f["-e"]})
		if err != nil {
			return s.fail("creating user", err)
		}
		return s.out(st.User(u)), nil, nil
	case "users rename":
		u, err := fb.RenameUser(ctx, u64(f["-i"]), f["-r"])
		if err != nil {
			return s.fail("renaming user", err)
		}
		return s.out(st.User(u)), nil, nil
	case "users destroy":
		if f["--force"] != "true" {
			return s.out(map[string]string{"Result": "User not destroyed"}), nil, nil
		}
		return done("destroying user", fb.DeleteUser(ctx, u64(f["-i"])))
	case "preauthkeys list":
		ks, err := fb.ListPreAuthKeys(ctx, api.KeyFilter{})
		if err != nil {
			return s.fail("listing preauthkeys", err)
		}
		return s.out(st.PreAuthKeys(ks)), nil, nil
	case "preauthkeys create":
		nk := api.NewPreAuthKey{Reusable: f["--reusable"] == "true", Ephemeral: f["--ephemeral"] == "true", ACLTags: csv(f["--tags"])}
		dur := time.Hour
		if e := f["-e"]; e != "" {
			dur = parseDur(e)
		}
		nk.Expiration = time.Now().Add(dur)
		if uid := u64(f["-u"]); uid != 0 {
			us, _ := fb.ListUsers(ctx, api.UserFilter{ID: uid})
			if len(us) == 0 {
				return s.fail("creating preauthkey", api.Errf(api.KindNotFound, "user not found"))
			}
			nk.User = us[0].Name
		}
		k, err := fb.CreatePreAuthKey(ctx, nk)
		if err != nil {
			return s.fail("creating preauthkey", err)
		}
		return s.out(st.PreAuthKey(k)), nil, nil
	case "preauthkeys expire":
		return done("expiring preauthkey", fb.ExpirePreAuthKey(ctx, u64(f["-i"])))
	case "preauthkeys delete":
		return done("deleting preauthkey", fb.DeletePreAuthKey(ctx, u64(f["-i"])))
	case "apikeys list":
		ks, err := fb.ListAPIKeys(ctx)
		if err != nil {
			return s.fail("listing api keys", err)
		}
		return s.out(st.APIKeys(ks)), nil, nil
	case "apikeys create":
		sec, err := fb.CreateAPIKey(ctx, time.Now().Add(parseDur(f["-e"])))
		if err != nil {
			return s.fail("creating api key", err)
		}
		return s.out(sec.Reveal()), nil, nil
	case "apikeys expire":
		return done("expiring api key", fb.ExpireAPIKey(ctx, f["-p"]))
	case "apikeys delete":
		return done("deleting api key", fb.DeleteAPIKey(ctx, f["-p"]))
	case "policy get":
		pol, err := fb.GetPolicy(ctx)
		if err != nil {
			return s.fail("loading policy", err)
		}
		return []byte(pol.Raw + "\n"), nil, nil
	case "policy check", "policy set":
		raw, err := os.ReadFile(f["-f"])
		if err != nil {
			return s.fail("reading policy file", api.Errf(api.KindInvalid, "%v", err))
		}
		if p.cmd == "policy check" {
			if err := fb.CheckPolicy(ctx, string(raw)); err != nil {
				return s.fail("checking policy", err)
			}
			return []byte("Policy is valid\n"), nil, nil
		}
		if _, err := fb.SetPolicy(ctx, string(raw)); err != nil {
			return s.fail("setting policy", err)
		}
		return []byte("Policy updated.\n"), nil, nil
	case "auth register":
		return done("registering", fb.AuthRegister(ctx, f["--auth-id"], f["-u"]))
	case "auth approve":
		return done("approving", fb.AuthApprove(ctx, f["--auth-id"]))
	case "auth reject":
		return done("rejecting", fb.AuthReject(ctx, f["--auth-id"]))
	}
	return nil, s.out(map[string]string{"error": "unknown command \"" + p.cmd + "\" for \"headscale\""}), errExit
}
