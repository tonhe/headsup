package httpapi

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tonhe/headsup/internal/api"
	"github.com/tonhe/headsup/internal/api/apitest"
)

func startSim(t *testing.T, version string) (*sim, *Client) {
	t.Helper()
	s := newSim(version)
	srv := httptest.NewServer(s.handler())
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, api.NewSecret(testKey))
	if err != nil {
		t.Fatal(err)
	}
	return s, c
}

func TestContract(t *testing.T) {
	for _, v := range []string{"v0.29.6", "v0.30.0"} {
		t.Run(v, func(t *testing.T) {
			apitest.RunContract(t, func(t *testing.T) api.Backend {
				_, c := startSim(t, v)
				return c
			})
		})
	}
}

func TestUnixSocket(t *testing.T) {
	dir, err := os.MkdirTemp("", "hs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Skipf("unix sockets unavailable: %v", err)
	}
	s := newSim("v0.30.0")
	s.noAuth = true
	srv := httptest.NewUnstartedServer(s.handler())
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)

	c, err := New("", api.NewSecret(testKey), WithUnixSocket(sock))
	if err != nil {
		t.Fatal(err)
	}
	info, err := c.Connect(context.Background())
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if info.Version != "v0.30.0" {
		t.Errorf("version %q", info.Version)
	}
	nodes, err := c.ListNodes(context.Background(), api.NodeFilter{})
	if err != nil || len(nodes) != apitest.SeedNodes {
		t.Fatalf("ListNodes: %d nodes, %v", len(nodes), err)
	}
	if s.sawAuth.Load() {
		t.Error("Authorization header sent over the unix socket")
	}
}

func TestGatedCallsMakeNoRequest(t *testing.T) {
	s, c := startSim(t, "v0.29.6")
	ctx := context.Background()
	if _, err := c.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	before := s.requests.Load()
	tests := []struct {
		name string
		call func() error
	}{
		{"tag-only key", func() error {
			_, err := c.CreatePreAuthKey(ctx, api.NewPreAuthKey{ACLTags: []string{"tag:ci"}})
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); !api.IsKind(err, api.KindUnsupported) {
				t.Fatalf("got %v, want unsupported", err)
			}
		})
	}
	// Caps removed explicitly.
	c.caps = c.caps.Without(api.CapPolicyCheck).Without(api.CapAPIKeyDelete).Without(api.CapAuthApproval).
		Without(api.CapBackfillIPs).Without(api.CapPreAuthKeyDelete).Without(api.CapPolicyWritable).Without(api.CapDisableExpiry)
	more := []func() error{
		func() error { return c.CheckPolicy(ctx, "{}") },
		func() error { return c.DeleteAPIKey(ctx, "x") },
		func() error { return c.AuthApprove(ctx, "x") },
		func() error { return c.AuthReject(ctx, "x") },
		func() error { return c.AuthRegister(ctx, "x", "alice") },
		func() error { _, err := c.BackfillIPs(ctx); return err },
		func() error { return c.DeletePreAuthKey(ctx, 1) },
		func() error { _, err := c.SetPolicy(ctx, "{}"); return err },
		func() error { _, err := c.ExpireNode(ctx, 1, api.ExpireOpts{DisableExpiry: true}); return err },
	}
	for i, f := range more {
		if err := f(); !api.IsKind(err, api.KindUnsupported) {
			t.Errorf("call %d: got %v, want unsupported", i, err)
		}
	}
	if got := s.requests.Load(); got != before {
		t.Errorf("gated calls made %d requests", got-before)
	}
}

func TestConnectCaps(t *testing.T) {
	tests := []struct {
		version string
		want    map[api.Cap]bool
	}{
		{"v0.29.6", map[api.Cap]bool{api.CapProblemJSON: false, api.CapTagOnlyKeys: false, api.CapDisableExpiry: true, api.CapPolicyCheck: true}},
		{"v0.30.0", map[api.Cap]bool{api.CapProblemJSON: true, api.CapTagOnlyKeys: true, api.CapDisableExpiry: true}},
	}
	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			_, c := startSim(t, tt.version)
			info, err := c.Connect(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			for cp, want := range tt.want {
				if info.Caps.Has(cp) != want {
					t.Errorf("%s: has=%v want %v", cp, !want, want)
				}
			}
		})
	}
}

func TestErrorNormalisation(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		ctype     string
		body      string
		kind      api.ErrorKind
		msg       string
		line, col int
	}{
		{"problem 404", 404, "application/problem+json", `{"title":"Not Found","status":404,"detail":"node not found"}`, api.KindNotFound, "node not found", 0, 0},
		{"problem 401", 401, "application/problem+json", `{"title":"Unauthorized","status":401,"detail":"Unauthorized"}`, api.KindUnauthorized, "Unauthorized", 0, 0},
		{"problem 409", 409, "application/problem+json", `{"title":"Conflict","status":409,"detail":"user already exists"}`, api.KindConflict, "user already exists", 0, 0},
		{"problem 400 errors", 400, "application/problem+json", `{"title":"Bad Request","status":400,"detail":"validation failed","errors":[{"message":"expected uint64","location":"path.nodeId"}]}`, api.KindInvalid, "validation failed: expected uint64", 0, 0},
		{"problem policy pos", 400, "application/problem+json", `{"title":"Bad Request","status":400,"detail":"hujson: line 7, column 12: invalid character"}`, api.KindInvalid, "line 7", 7, 12},
		{"grpc not found", 404, "application/json", `{"code":5,"message":"node not found","details":[]}`, api.KindNotFound, "node not found", 0, 0},
		{"grpc unimplemented", 501, "application/json", `{"code":12,"message":"method not implemented","details":[]}`, api.KindUnsupported, "not implemented", 0, 0},
		{"grpc unimplemented on 500", 500, "application/json", `{"code":12,"message":"unknown method","details":[]}`, api.KindUnsupported, "unknown method", 0, 0},
		{"grpc unauthenticated", 401, "application/json", `{"code":16,"message":"invalid token"}`, api.KindUnauthorized, "invalid token", 0, 0},
		{"grpc already exists", 409, "application/json", `{"code":6,"message":"exists"}`, api.KindConflict, "exists", 0, 0},
		{"grpc invalid policy", 400, "application/json", `{"code":3,"message":"parsing policy: err at line 4:9"}`, api.KindInvalid, "parsing policy", 4, 9},
		{"plain 401", 401, "text/plain", "Unauthorized", api.KindUnauthorized, "Unauthorized", 0, 0},
		{"plain 501", 501, "text/plain", "", api.KindUnsupported, "Not Implemented", 0, 0},
		{"plain 500", 500, "text/plain", "boom", api.KindServer, "boom", 0, 0},
		{"bad gateway", 502, "text/html", "<html>proxy</html>", api.KindUnreachable, "proxy", 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := httpError(&response{status: tt.status, ctype: tt.ctype, body: []byte(tt.body)})
			if e.Kind != tt.kind || !strings.Contains(e.Message, tt.msg) || e.Status != tt.status || e.Line != tt.line || e.Col != tt.col {
				t.Errorf("got %+v", e)
			}
		})
	}
}

func TestDialErrorIsUnreachable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	c, err := New("http://"+addr, api.NewSecret(testKey), WithTimeout(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Connect(context.Background())
	if !api.IsKind(err, api.KindUnreachable) {
		t.Fatalf("got %v, want unreachable", err)
	}
	if strings.Contains(err.Error(), testKey) {
		t.Error("error leaks the API key")
	}
}

func TestTimeoutIsUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer srv.Close()
	c, _ := New(srv.URL, api.NewSecret(testKey), WithTimeout(50*time.Millisecond))
	_, err := c.ListUsers(context.Background(), api.UserFilter{})
	e := api.AsError(err)
	if e == nil || e.Kind != api.KindUnreachable || !strings.Contains(e.Message, "timed out") {
		t.Fatalf("got %+v", e)
	}
}

func TestOptions(t *testing.T) {
	if _, err := New("not a url", api.Secret{}); !api.IsKind(err, api.KindInvalid) {
		t.Errorf("bad URL: %v", err)
	}
	if _, err := New("https://x.example", api.Secret{}, WithCAFile("/nonexistent/ca.pem")); !api.IsKind(err, api.KindInvalid) {
		t.Errorf("missing CA: %v", err)
	}
	c, err := New("https://hs.example.com/api/v1/", api.NewSecret(testKey), WithInsecureSkipVerify(), WithVersion("v0.29.6"))
	if err != nil {
		t.Fatal(err)
	}
	if c.base.Path != "" || !c.tls.InsecureSkipVerify || c.Capabilities().Has(api.CapTagOnlyKeys) {
		t.Errorf("options not applied: path=%q", c.base.Path)
	}
	if strings.Contains(c.String(), testKey) {
		t.Error("String leaks key")
	}
	hc := &http.Client{}
	c, _ = New("https://hs.example.com", api.Secret{}, WithHTTPClient(hc))
	if c.hc != hc {
		t.Error("WithHTTPClient ignored")
	}
}

// fixture is one hand-built exchange from testdata/http.
type fixture struct {
	Name    string `json:"name"`
	Request struct {
		Method string            `json:"method"`
		Path   string            `json:"path"`
		Query  map[string]string `json:"query"`
		Body   map[string]any    `json:"body"`
	} `json:"request"`
	Response struct {
		Status  int               `json:"status"`
		Headers map[string]string `json:"headers"`
		Body    json.RawMessage   `json:"body"`
	} `json:"response"`
}

func loadFixtures(t *testing.T, ver string) map[string]fixture {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("..", "..", "..", "testdata", "http", ver, "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no fixtures for %s: %v", ver, err)
	}
	out := map[string]fixture{}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var fx fixture
		if err := json.Unmarshal(data, &fx); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		out[fx.Name] = fx
	}
	return out
}

// replay serves exactly one fixture and verifies the request matches it.
func replay(t *testing.T, fx fixture) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != fx.Request.Method || r.URL.Path != fx.Request.Path {
			t.Errorf("%s: request %s %s, want %s %s", fx.Name, r.Method, r.URL.Path, fx.Request.Method, fx.Request.Path)
		}
		for k, v := range fx.Request.Query {
			if got := r.URL.Query().Get(k); got != v {
				t.Errorf("%s: query %s=%q, want %q", fx.Name, k, got, v)
			}
		}
		if fx.Request.Body != nil {
			var got map[string]any
			_ = json.NewDecoder(r.Body).Decode(&got)
			for k, v := range fx.Request.Body {
				if !reflect.DeepEqual(got[k], v) {
					t.Errorf("%s: body %s=%v, want %v", fx.Name, k, got[k], v)
				}
			}
		}
		for k, v := range fx.Response.Headers {
			w.Header().Set(k, v)
		}
		w.WriteHeader(fx.Response.Status)
		body := []byte(fx.Response.Body)
		var s string
		if json.Unmarshal(body, &s) == nil { // plain-text body stored as a JSON string
			body = []byte(s)
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, api.NewSecret(testKey))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestFixtures(t *testing.T) {
	ctx := context.Background()
	exit := netip.MustParsePrefix("0.0.0.0/0")
	for _, ver := range []string{"v029", "v030"} {
		fxs := loadFixtures(t, ver)
		cases := []struct {
			name  string
			check func(t *testing.T, c *Client)
		}{
			{"version", func(t *testing.T, c *Client) {
				r, err := c.do(ctx, http.MethodGet, "/version", nil, nil, false)
				if err != nil || !strings.Contains(string(r.body), "v0.") {
					t.Fatalf("version: %v", err)
				}
			}},
			{"health", func(t *testing.T, c *Client) {
				if _, err := c.call(ctx, http.MethodGet, "/api/v1/health", nil, nil); err != nil {
					t.Fatal(err)
				}
			}},
			{"list_nodes", func(t *testing.T, c *Client) {
				ns, err := c.ListNodes(ctx, api.NodeFilter{User: "ops"})
				if err != nil || len(ns) != 2 {
					t.Fatalf("%d nodes, %v", len(ns), err)
				}
				if ns[0].ID != 23 || ns[0].User.Name != "ops" || !ns[0].IsExitNode() || !ns[0].ExitApproved() ||
					!ns[0].IPAddresses[0].Is4() || ns[0].RegisterMethod != api.RegisterAuthKey || ns[0].Expiry == nil ||
					ns[0].LastSeen.IsZero() || !ns[0].HasTag("exit") {
					t.Errorf("node 0: %+v", ns[0])
				}
				if len(ns[1].PendingRoutes()) != 1 || ns[1].Online || ns[1].RegisterMethod != api.RegisterCLI {
					t.Errorf("node 1: %+v", ns[1])
				}
				_ = exit
			}},
			{"get_node_not_found", func(t *testing.T, c *Client) {
				_, err := c.GetNode(ctx, 999)
				if !api.IsKind(err, api.KindNotFound) {
					t.Fatalf("got %v", err)
				}
			}},
			{"list_users", func(t *testing.T, c *Client) {
				us, err := c.ListUsers(ctx, api.UserFilter{})
				if err != nil || len(us) != 2 || us[0].Name != "alice" || us[0].ID != 1 || us[0].Provider != "oidc" || us[0].CreatedAt.IsZero() {
					t.Fatalf("%+v %v", us, err)
				}
			}},
			{"list_preauthkeys", func(t *testing.T, c *Client) {
				ks, err := c.ListPreAuthKeys(ctx, api.KeyFilter{})
				if err != nil || len(ks) != 2 || ks[1].ACLTags[0] != "tag:server" || !ks[1].Reusable || ks[0].User.Name != "alice" || ks[0].Key.IsZero() {
					t.Fatalf("%+v %v", ks, err)
				}
			}},
			{"list_apikeys", func(t *testing.T, c *Client) {
				ks, err := c.ListAPIKeys(ctx)
				if err != nil || len(ks) != 1 || ks[0].Prefix != "3f9c2a7b1d0e" || ks[0].LastSeen.IsZero() {
					t.Fatalf("%+v %v", ks, err)
				}
			}},
			{"get_policy", func(t *testing.T, c *Client) {
				p, err := c.GetPolicy(ctx)
				if err != nil || !strings.HasPrefix(p.Raw, "// demo") || p.UpdatedAt.IsZero() {
					t.Fatalf("%+v %v", p, err)
				}
			}},
			{"check_policy_invalid", func(t *testing.T, c *Client) {
				err := c.CheckPolicy(ctx, fxs["check_policy_invalid"].Request.Body["policy"].(string))
				e := api.AsError(err)
				if e == nil || e.Kind != api.KindInvalid || e.Line != 3 || e.Col != 37 {
					t.Fatalf("got %+v", e)
				}
			}},
			{"unauthorized", func(t *testing.T, c *Client) {
				_, err := c.ListUsers(ctx, api.UserFilter{})
				if !api.IsKind(err, api.KindUnauthorized) {
					t.Fatalf("got %v", err)
				}
			}},
			{"create_user_conflict", func(t *testing.T, c *Client) {
				_, err := c.CreateUser(ctx, api.NewUser{Name: "alice"})
				if !api.IsKind(err, api.KindConflict) {
					t.Fatalf("got %v", err)
				}
			}},
			{"expire_node", func(t *testing.T, c *Client) {
				n, err := c.ExpireNode(ctx, 5, api.ExpireOpts{DisableExpiry: true})
				if err != nil || !n.ExpiryDisabled() {
					t.Fatalf("%+v %v", n, err)
				}
			}},
		}
		for _, tc := range cases {
			t.Run(ver+"/"+tc.name, func(t *testing.T) {
				fx, ok := fxs[tc.name]
				if !ok {
					t.Fatalf("missing fixture %s", tc.name)
				}
				tc.check(t, replay(t, fx))
			})
		}
	}
}

func TestSelfAPIKey(t *testing.T) {
	c, err := New("https://hs.example.com", api.NewSecret("hskey-api-abcdefabcdef-secret"))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.SelfAPIKey().Reveal(); got != "hskey-api-abcdefabcdef-secret" {
		t.Fatalf("SelfAPIKey = %q", got)
	}
}
