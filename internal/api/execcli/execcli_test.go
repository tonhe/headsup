package execcli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tonhe/headsup/internal/api"
	"github.com/tonhe/headsup/internal/api/apitest"
)

func TestContract(t *testing.T) {
	for _, v := range []string{"v0.29.6", "v0.30.0"} {
		t.Run(v, func(t *testing.T) {
			apitest.RunContract(t, func(t *testing.T) api.Backend {
				return New(WithRunner(newCLISim(v).run))
			})
		})
	}
}

func TestContractInContainer(t *testing.T) {
	sim := newCLISim("v0.30.0")
	var copied []string
	runner := func(ctx context.Context, args ...string) ([]byte, []byte, error) {
		switch {
		case len(args) > 1 && args[1] == "cp":
			copied = append(copied, args[2])
			return nil, nil, nil
		case len(args) > 3 && args[3] == "rm":
			return nil, nil, nil
		}
		// Map the in-container path back to the host temp file.
		for i, a := range args {
			if strings.HasPrefix(a, "/tmp/headsup-policy-") {
				for _, h := range copied {
					if filepath.Base(h) == filepath.Base(a) {
						args[i] = h
					}
				}
			}
		}
		return sim.run(ctx, args...)
	}
	b := New(WithRunner(runner), WithContainer("podman", "hs"))
	ctx := context.Background()
	if _, err := b.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	if err := b.CheckPolicy(ctx, "{}"); err != nil {
		t.Fatalf("CheckPolicy: %v", err)
	}
	if len(copied) != 1 {
		t.Errorf("policy file copied %d times", len(copied))
	}
	if got := sim.argv[0][:4]; !slices.Equal(got, []string{"podman", "exec", "hs", "headscale"}) {
		t.Errorf("argv prefix %v", got)
	}
}

func TestArgs(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		opts []Option
		call func(b *Backend) error
		want []string
	}{
		{"list nodes", nil, func(b *Backend) error { _, err := b.ListNodes(ctx, api.NodeFilter{User: "bob"}); return err },
			[]string{"headscale", "nodes", "list", "-u", "bob", "-o", "json"}},
		{"config", []Option{WithConfig("/etc/hs.yaml"), WithBinary("/usr/bin/headscale")},
			func(b *Backend) error { _, err := b.ListAPIKeys(ctx); return err },
			[]string{"/usr/bin/headscale", "apikeys", "list", "-c", "/etc/hs.yaml", "-o", "json"}},
		{"docker", []Option{WithContainer("docker", "headscale")},
			func(b *Backend) error { _, err := b.ListUsers(ctx, api.UserFilter{Name: "ops"}); return err },
			[]string{"docker", "exec", "headscale", "headscale", "users", "list", "-n", "ops", "-o", "json"}},
		{"rename", nil, func(b *Backend) error { _, err := b.RenameNode(ctx, 7, "nas"); return err },
			[]string{"headscale", "nodes", "rename", "-i", "7", "nas", "-o", "json"}},
		{"tags", nil, func(b *Backend) error { _, err := b.SetTags(ctx, 7, []string{"tag:a", "tag:b"}); return err },
			[]string{"headscale", "nodes", "tag", "-i", "7", "--tags=tag:a,tag:b", "-o", "json"}},
		{"clear tags", nil, func(b *Backend) error { _, err := b.SetTags(ctx, 7, nil); return err },
			[]string{"headscale", "nodes", "tag", "-i", "7", "--tags=", "-o", "json"}},
		{"routes", nil, func(b *Backend) error {
			_, err := b.SetApprovedRoutes(ctx, 3, nil)
			return err
		}, []string{"headscale", "nodes", "approve-routes", "-i", "3", "--routes=", "-o", "json"}},
		{"expire at", nil, func(b *Backend) error { _, err := b.ExpireNode(ctx, 3, api.ExpireOpts{At: at}); return err },
			[]string{"headscale", "nodes", "expire", "-i", "3", "-e", "2026-11-01T12:00:00Z", "-o", "json"}},
		{"disable expiry", []Option{WithVersion("v0.30.0")}, func(b *Backend) error {
			_, err := b.ExpireNode(ctx, 3, api.ExpireOpts{DisableExpiry: true})
			return err
		}, []string{"headscale", "nodes", "expire", "-i", "3", "-d", "-o", "json"}},
		{"delete node", nil, func(b *Backend) error { return b.DeleteNode(ctx, 3) },
			[]string{"headscale", "nodes", "delete", "-i", "3", "--force", "-o", "json"}},
		{"backfill", nil, func(b *Backend) error { _, err := b.BackfillIPs(ctx); return err },
			[]string{"headscale", "nodes", "backfillips", "--force", "-o", "json"}},
		{"register", nil, func(b *Backend) error { _, err := b.RegisterNode(ctx, "bob", "abc"); return err },
			[]string{"headscale", "nodes", "register", "-u", "bob", "-k", "abc", "-o", "json"}},
		{"create user", nil, func(b *Backend) error {
			_, err := b.CreateUser(ctx, api.NewUser{Name: "dave", DisplayName: "Dave", Email: "d@x"})
			return err
		}, []string{"headscale", "users", "create", "dave", "-d", "Dave", "-e", "d@x", "-o", "json"}},
		{"rename user", nil, func(b *Backend) error { _, err := b.RenameUser(ctx, 2, "robert"); return err },
			[]string{"headscale", "users", "rename", "-i", "2", "-r", "robert", "-o", "json"}},
		{"destroy user", nil, func(b *Backend) error { return b.DeleteUser(ctx, 2) },
			[]string{"headscale", "users", "destroy", "-i", "2", "--force", "-o", "json"}},
		{"expire key", nil, func(b *Backend) error { return b.ExpirePreAuthKey(ctx, 9) },
			[]string{"headscale", "preauthkeys", "expire", "-i", "9", "-o", "json"}},
		{"delete key", nil, func(b *Backend) error { return b.DeletePreAuthKey(ctx, 9) },
			[]string{"headscale", "preauthkeys", "delete", "-i", "9", "--force", "-o", "json"}},
		{"expire api key", nil, func(b *Backend) error { return b.ExpireAPIKey(ctx, "abc123") },
			[]string{"headscale", "apikeys", "expire", "-p", "abc123", "-o", "json"}},
		{"delete api key", nil, func(b *Backend) error { return b.DeleteAPIKey(ctx, "abc123") },
			[]string{"headscale", "apikeys", "delete", "-p", "abc123", "--force", "-o", "json"}},
		{"policy get", nil, func(b *Backend) error { _, err := b.GetPolicy(ctx); return err },
			[]string{"headscale", "policy", "get", "-o", "json"}},
		{"auth register", nil, func(b *Backend) error { return b.AuthRegister(ctx, "AID", "bob") },
			[]string{"headscale", "auth", "register", "--auth-id", "AID", "-u", "bob", "-o", "json"}},
		{"auth approve", nil, func(b *Backend) error { return b.AuthApprove(ctx, "AID") },
			[]string{"headscale", "auth", "approve", "--auth-id", "AID", "-o", "json"}},
		{"auth reject", nil, func(b *Backend) error { return b.AuthReject(ctx, "AID") },
			[]string{"headscale", "auth", "reject", "--auth-id", "AID", "-o", "json"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			r := func(_ context.Context, args ...string) ([]byte, []byte, error) {
				got = slices.Clone(args)
				return []byte("{}"), nil, nil
			}
			b := New(append([]Option{WithRunner(r)}, tt.opts...)...)
			_ = tt.call(b)
			if !slices.Equal(got, tt.want) {
				t.Errorf("argv\n got %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestCreatePreAuthKeyArgs(t *testing.T) {
	var calls [][]string
	r := func(_ context.Context, args ...string) ([]byte, []byte, error) {
		calls = append(calls, slices.Clone(args))
		if args[1] == "users" {
			return []byte(`[{"id": 3, "name": "carol"}]`), nil, nil
		}
		return []byte(`{"id": 12, "key": "hskey-auth-x-y", "user": {"id": 3, "name": "carol"}}`), nil, nil
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	b := New(WithRunner(r))
	b.now = func() time.Time { return now }
	k, err := b.CreatePreAuthKey(context.Background(), api.NewPreAuthKey{
		User: "carol", Reusable: true, Ephemeral: true, Expiration: now.Add(2 * time.Hour), ACLTags: []string{"tag:ci"},
	})
	if err != nil || k.ID != 12 || k.User.Name != "carol" {
		t.Fatalf("%+v %v", k, err)
	}
	want := []string{"headscale", "preauthkeys", "create", "-u", "3", "-e", "7200s", "--reusable", "--ephemeral", "--tags=tag:ci", "-o", "json"}
	if len(calls) != 2 || !slices.Equal(calls[1], want) {
		t.Errorf("calls %q", calls)
	}
}

func TestGatedNoExec(t *testing.T) {
	ran := 0
	r := func(context.Context, ...string) ([]byte, []byte, error) { ran++; return nil, nil, nil }
	b := New(WithRunner(r), WithVersion("v0.29.6"))
	ctx := context.Background()
	if _, err := b.CreatePreAuthKey(ctx, api.NewPreAuthKey{ACLTags: []string{"tag:ci"}}); !api.IsKind(err, api.KindUnsupported) {
		t.Errorf("got %v", err)
	}
	if ran != 0 {
		t.Errorf("runner called %d times", ran)
	}
}

func TestClassify(t *testing.T) {
	tests := []struct {
		name      string
		runErr    error
		stderr    string
		kind      api.ErrorKind
		line, col int
	}{
		{"grpc not found", errExit, `{"error": "renaming node: rpc error: code = NotFound desc = node not found"}`, api.KindNotFound, 0, 0},
		{"grpc invalid policy", errExit, `{"error": "rpc error: code = InvalidArgument desc = hujson: line 4, column 2: bad"}`, api.KindInvalid, 4, 2},
		{"grpc unavailable", errExit, `{"error": "rpc error: code = Unavailable desc = connection error"}`, api.KindUnreachable, 0, 0},
		{"grpc unimplemented", errExit, `{"error": "rpc error: code = Unimplemented desc = unknown method"}`, api.KindUnsupported, 0, 0},
		{"grpc exists", errExit, `{"error": "rpc error: code = AlreadyExists desc = user exists"}`, api.KindConflict, 0, 0},
		{"http 404", errExit, `{"error": "renaming node: 404 Not Found: node not found"}`, api.KindNotFound, 0, 0},
		{"http 401", errExit, `{"error": "listing users: 401 Unauthorized: Unauthorized"}`, api.KindUnauthorized, 0, 0},
		{"http 400 policy", errExit, `{"error": "checking policy: 400 Bad Request: hujson: line 3, column 37: x"}`, api.KindInvalid, 3, 37},
		{"socket refused", errExit, `{"error": "dial unix /var/run/headscale/headscale.sock: connect: connection refused"}`, api.KindUnreachable, 0, 0},
		{"socket perms", errExit, "Error: dial unix /var/run/headscale/headscale.sock: connect: permission denied", api.KindUnauthorized, 0, 0},
		{"unknown command", errExit, `Error: unknown command "auth" for "headscale"`, api.KindUnsupported, 0, 0},
		{"unknown flag", errExit, "Error: unknown flag: --disable", api.KindUnsupported, 0, 0},
		{"plain", errExit, "something odd", api.KindServer, 0, 0},
		{"binary missing", &exec.Error{Name: "headscale", Err: exec.ErrNotFound}, "", api.KindUnreachable, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := cliError(tt.runErr, []byte(tt.stderr), nil)
			if e.Kind != tt.kind || e.Line != tt.line || e.Col != tt.col {
				t.Errorf("got %+v", e)
			}
		})
	}
}

// cliFixture is one hand-built CLI run from testdata/cli.
type cliFixture struct {
	Name   string   `json:"name"`
	Args   []string `json:"args"`
	Exit   int      `json:"exit"`
	Stdout string   `json:"stdout"`
	Stderr string   `json:"stderr"`
}

func loadCLIFixtures(t *testing.T, ver string) map[string]cliFixture {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join("..", "..", "..", "testdata", "cli", ver, "*.json"))
	if len(files) == 0 {
		t.Fatalf("no fixtures for %s", ver)
	}
	out := map[string]cliFixture{}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var fx cliFixture
		if err := json.Unmarshal(data, &fx); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		out[fx.Name] = fx
	}
	return out
}

// replayRunner answers every command whose args match a fixture.
func replayRunner(t *testing.T, fxs map[string]cliFixture) Runner {
	return func(_ context.Context, args ...string) ([]byte, []byte, error) {
		sub := args[1:]
		for _, fx := range fxs {
			if len(fx.Args) != len(sub) {
				continue
			}
			match := true
			for i := range sub {
				if fx.Args[i] != sub[i] && fx.Args[i] != "<tmp>" {
					match = false
					break
				}
			}
			if match {
				var err error
				if fx.Exit != 0 {
					err = errors.New("exit status " + string(rune('0'+fx.Exit)))
				}
				return []byte(fx.Stdout), []byte(fx.Stderr), err
			}
		}
		t.Errorf("no fixture for %q", sub)
		return nil, []byte(`{"error": "no fixture"}`), errExit
	}
}

func TestFixtures(t *testing.T) {
	ctx := context.Background()
	for _, ver := range []string{"v029", "v030"} {
		t.Run(ver, func(t *testing.T) {
			fxs := loadCLIFixtures(t, ver)
			delete(fxs, "unreachable") // same argv as users_list; see TestFixtureUnreachable
			b := New(WithRunner(replayRunner(t, fxs)))
			info, err := b.Connect(ctx)
			if err != nil || !info.Healthy || !strings.HasPrefix(info.Version, "v0.") {
				t.Fatalf("Connect %+v %v", info, err)
			}
			ns, err := b.ListNodes(ctx, api.NodeFilter{User: "ops"})
			if err != nil || len(ns) != 2 {
				t.Fatalf("ListNodes %d %v", len(ns), err)
			}
			n := ns[0]
			if n.ID != 23 || n.User.Name != "ops" || !n.IsExitNode() || n.RegisterMethod != api.RegisterAuthKey ||
				n.Expiry == nil || n.LastSeen.IsZero() || n.CreatedAt.IsZero() || len(n.IPAddresses) != 2 {
				t.Errorf("node 0 %+v", n)
			}
			if len(ns[1].PendingRoutes()) != 1 || ns[1].Online || ns[1].RegisterMethod != api.RegisterCLI {
				t.Errorf("node 1 %+v", ns[1])
			}
			us, err := b.ListUsers(ctx, api.UserFilter{})
			if err != nil || len(us) != 2 || us[0].ID != 1 || us[0].DisplayName != "Alice Liddell" || us[0].CreatedAt.IsZero() {
				t.Errorf("users %+v %v", us, err)
			}
			ks, err := b.ListPreAuthKeys(ctx, api.KeyFilter{User: "ops"})
			if err != nil || len(ks) != 1 || ks[0].ID != 4 || !ks[0].Reusable || ks[0].ACLTags[0] != "tag:server" {
				t.Errorf("keys %+v %v", ks, err)
			}
			ak, err := b.ListAPIKeys(ctx)
			if err != nil || len(ak) != 1 || ak[0].Prefix != "3f9c2a7b1d0e" || ak[0].LastSeen.IsZero() {
				t.Errorf("api keys %+v %v", ak, err)
			}
			b.now = func() time.Time { return time.Unix(0, 0) }
			sec, err := b.CreateAPIKey(ctx, time.Unix(86400, 0))
			if err != nil || !strings.HasPrefix(sec.Reveal(), "hskey-api-3f9c2a7b1d0e-") {
				t.Errorf("CreateAPIKey %v", err)
			}
			p, err := b.GetPolicy(ctx)
			if err != nil || !strings.HasPrefix(p.Raw, "// demo") || !strings.HasSuffix(p.Raw, "}\n") {
				t.Errorf("policy %q %v", p.Raw, err)
			}
			_, err = b.RenameNode(ctx, 999, "new-name")
			if !api.IsKind(err, api.KindNotFound) {
				t.Errorf("rename: %v", err)
			}
			e := api.AsError(b.CheckPolicy(ctx, "{,}"))
			if e == nil || e.Kind != api.KindInvalid || e.Line != 3 || e.Col != 37 {
				t.Errorf("check: %+v", e)
			}
		})
	}
}

func TestFixtureUnreachable(t *testing.T) {
	for _, ver := range []string{"v029", "v030"} {
		t.Run(ver, func(t *testing.T) {
			fx := loadCLIFixtures(t, ver)["unreachable"]
			b := New(WithRunner(replayRunner(t, map[string]cliFixture{"u": fx})))
			_, err := b.ListUsers(context.Background(), api.UserFilter{})
			if !api.IsKind(err, api.KindUnreachable) {
				t.Errorf("got %v", err)
			}
		})
	}
}
