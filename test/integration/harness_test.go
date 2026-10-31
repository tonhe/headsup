//go:build integration

// Package integration runs the quickstart validation scenarios against a real
// headscale in docker: V1-V12 in remote mode (the HTTP API with a key) and
// H1-H11 in host mode, plus a shape-drift check of the unversioned /debug and
// /metrics endpoints.
//
// The headscale container mirrors the documented docker install (config
// bind-mounted read-only at /etc/headscale, state in /var/lib/headscale, a
// tmpfs for the socket) with one change: it uses the host network, so the
// debug listener on 127.0.0.1 is reachable from this machine the way it is
// on a host install (headscale refuses /debug from anything but loopback or
// the tailnet). Host mode runs alongside the container through
// `[host].container`, exactly as a user with a containerised headscale would
// run it; the permission explainer (H2) runs inside the container as an
// unprivileged user. Tailscale clients run in their own containers.
//
// Environment:
//
//	HEADSCALE_IMAGE_TAG  headscale/headscale tag (default 0.29.6; "development" is main)
//	HEADSCALE_IMAGE      full image reference, overriding the tag
//	TAILSCALE_IMAGE      client image (default tailscale/tailscale:stable)
//	HEADSUP_RECORD_FIXTURES=DIR  save the raw /debug and /metrics bodies to DIR/<v029|v030>
//	HEADSUP_KEEP=1       leave the containers running after the run
package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tonhe/headsup/internal/api"
	"github.com/tonhe/headsup/internal/api/httpapi"
	"github.com/tonhe/headsup/internal/brand"
	"github.com/tonhe/headsup/internal/ui/uitest"
)

// S is the shared environment, built once in TestMain.
var S *suite

type suite struct {
	tag, image, tsImage string
	series              string // v029 or v030, for fixtures
	prefix              string // container name prefix
	name                string // headscale container
	dir                 string // temp root
	cfgDir, libDir      string
	port, metricsPort   int
	gateway             string // docker bridge gateway: how clients reach headscale
	serverURL           string // what clients use
	url                 string // what remote mode uses
	apiKey              string
	bin                 string // headsup for this machine
	binLinux            string // headsup for inside the container (linux, static)
	home                string // isolated HOME for the CLI and the app
	cfgFile             string // headsup's own config.toml (with [host].container)

	mu      sync.Mutex
	clients []string // client containers to remove
}

func TestMain(m *testing.M) {
	if err := exec.Command("docker", "info").Run(); err != nil {
		if os.Getenv("CI") != "" {
			fmt.Fprintln(os.Stderr, "integration: docker is required in CI:", err)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "integration: docker not available; skipping")
		os.Exit(0)
	}
	s, err := setup()
	if err != nil {
		fmt.Fprintln(os.Stderr, "integration setup:", err)
		if s != nil {
			s.teardown(true)
		}
		os.Exit(1)
	}
	S = s
	// Colour on, so theme scenarios see the escapes a terminal would.
	uitest.ForceColor()
	code := m.Run()
	s.teardown(code != 0)
	os.Exit(code)
}

func setup() (*suite, error) {
	s := &suite{
		tag:     envOr("HEADSCALE_IMAGE_TAG", "0.29.6"),
		tsImage: envOr("TAILSCALE_IMAGE", "tailscale/tailscale:stable"),
		prefix:  fmt.Sprintf("%s-it-%d", brand.Name, os.Getpid()),
	}
	s.image = envOr("HEADSCALE_IMAGE", "headscale/headscale:"+s.tag)
	s.series = "v030"
	if strings.HasPrefix(strings.TrimPrefix(s.tag, "v"), "0.29") {
		s.series = "v029"
	}
	// The container name contains "headscale", as a real one usually does.
	s.name = s.prefix + "-headscale"
	var err error
	if s.dir, err = os.MkdirTemp("", brand.Name+"-it-"); err != nil {
		return nil, err
	}
	s.cfgDir, s.libDir = filepath.Join(s.dir, "config"), filepath.Join(s.dir, "lib")
	s.home = filepath.Join(s.dir, "home")
	for _, d := range []string{s.cfgDir, s.libDir, s.home} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return s, err
		}
	}
	// Isolate this process too: the app writes its activity log, theme
	// choice and backups under these.
	for k, v := range s.homeEnv() {
		os.Setenv(k, v)
	}
	for _, k := range []string{"HEADSCALE_URL", "HEADSCALE_API_KEY", "HEADSCALE_CONFIG", "NO_COLOR"} {
		os.Unsetenv(k)
	}

	for _, img := range []string{s.image, s.tsImage} {
		if out, err := run("docker", "pull", "-q", img); err != nil {
			return s, fmt.Errorf("docker pull %s: %v\n%s", img, err, out)
		}
	}
	if s.gateway, err = run("docker", "network", "inspect", "bridge", "-f", "{{(index .IPAM.Config 0).Gateway}}"); err != nil {
		return s, fmt.Errorf("bridge gateway: %v", err)
	}
	s.gateway = strings.TrimSpace(s.gateway)
	if s.port, err = freePort(); err != nil {
		return s, err
	}
	if s.metricsPort, err = freePort(); err != nil {
		return s, err
	}
	s.serverURL = fmt.Sprintf("http://%s:%d", s.gateway, s.port)
	s.url = fmt.Sprintf("http://127.0.0.1:%d", s.port)
	if err := s.writeServerFiles(); err != nil {
		return s, err
	}
	if err := s.buildBinaries(); err != nil {
		return s, err
	}
	if err := s.startHeadscale(); err != nil {
		return s, err
	}
	if _, err := s.hs("users", "create", "alice"); err != nil {
		return s, err
	}
	out, err := s.hs("apikeys", "create", "--expiration", "24h", "-o", "json")
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal([]byte(lastJSON(out)), &s.apiKey); err != nil || s.apiKey == "" {
		return s, fmt.Errorf("parsing API key from %q: %v", out, err)
	}
	if err := s.seedPolicy(); err != nil {
		return s, err
	}
	return s, s.writeAppConfig()
}

func (s *suite) teardown(failed bool) {
	if failed {
		if out, err := run("docker", "logs", "--tail", "80", s.name); err == nil {
			fmt.Fprintf(os.Stderr, "--- headscale logs (%s) ---\n%s\n", s.image, out)
		}
	}
	if os.Getenv("HEADSUP_KEEP") != "" {
		fmt.Fprintf(os.Stderr, "integration: leaving %s and its clients running; files in %s\n", s.name, s.dir)
		return
	}
	s.mu.Lock()
	names := append([]string{s.name}, s.clients...)
	s.mu.Unlock()
	_, _ = run("docker", append([]string{"rm", "-f"}, names...)...)
	// headscale wrote its state as root; remove it from a container.
	if s.libDir != "" {
		_, _ = run("docker", "run", "--rm", "-v", s.libDir+":/d", "--entrypoint", "sh", s.tsImage, "-c", "rm -rf /d/* /d/.[!.]*")
	}
	os.RemoveAll(s.dir)
}

const configYAML = `server_url: {{SERVER_URL}}
listen_addr: 0.0.0.0:{{PORT}}
metrics_listen_addr: 127.0.0.1:{{METRICS}}
noise:
  private_key_path: /var/lib/headscale/noise_private.key
prefixes:
  v4: 100.64.0.0/10
  v6: fd7a:115c:a1e0::/48
  allocation: sequential
derp:
  server:
    enabled: false
  urls:
    - https://controlplane.tailscale.com/derpmap/default
  paths: []
  auto_update_enabled: false
node:
  expiry: 0
database:
  type: sqlite
  sqlite:
    path: /var/lib/headscale/db.sqlite
    write_ahead_log: true
log:
  level: info
  format: text
policy:
  # database, so remote mode can edit it (V6, V7); H8 switches to file.
  mode: database
  path: /etc/headscale/policy.hujson
dns:
  magic_dns: true
  base_domain: tail.example
  override_local_dns: false
  nameservers:
    global: []
  extra_records_path: /etc/headscale/extra-records.json
unix_socket: /var/run/headscale/headscale.sock
unix_socket_permission: "0770"
`

// basePolicy lets everything through and lets alice own tag:server.
const basePolicy = `{
  "tagOwners": {"tag:server": ["alice@"]},
  "acls": [
    {"action": "accept", "src": ["*"], "dst": ["*:*"]}
  ]
}
`

func (s *suite) writeServerFiles() error {
	cfg := strings.NewReplacer("{{SERVER_URL}}", s.serverURL, "{{PORT}}", strconv.Itoa(s.port),
		"{{METRICS}}", strconv.Itoa(s.metricsPort)).Replace(configYAML)
	files := map[string]string{"config.yaml": cfg, "policy.hujson": basePolicy, "extra-records.json": "[]\n"}
	for n, b := range files {
		if err := os.WriteFile(filepath.Join(s.cfgDir, n), []byte(b), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func (s *suite) configPath() string { return filepath.Join(s.cfgDir, "config.yaml") }

func (s *suite) buildBinaries() error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	s.bin = filepath.Join(s.dir, "bin", brand.Name)
	if out, err := runIn(root, nil, "go", "build", "-o", s.bin, "./cmd/"+brand.Name); err != nil {
		return fmt.Errorf("go build: %v\n%s", err, out)
	}
	s.binLinux = s.bin
	if runtime.GOOS != "linux" || os.Getenv("CGO_ENABLED") != "0" {
		s.binLinux = filepath.Join(s.dir, "bin", "linux", brand.Name)
		env := []string{"CGO_ENABLED=0", "GOOS=linux", "GOARCH=" + runtime.GOARCH}
		if out, err := runIn(root, env, "go", "build", "-o", s.binLinux, "./cmd/"+brand.Name); err != nil {
			return fmt.Errorf("go build (linux): %v\n%s", err, out)
		}
	}
	return nil
}

func (s *suite) startHeadscale() error {
	_, _ = run("docker", "rm", "-f", s.name)
	args := []string{"run", "-d", "--name", s.name, "--network", "host",
		"--tmpfs", "/var/run/headscale",
		"-v", s.cfgDir + ":/etc/headscale:ro",
		"-v", s.libDir + ":/var/lib/headscale",
		s.image, "serve"}
	if out, err := run("docker", args...); err != nil {
		return fmt.Errorf("docker run headscale: %v\n%s", err, out)
	}
	return s.waitHealthy(60 * time.Second)
}

// waitHealthy waits for /health to answer 200.
func (s *suite) waitHealthy(d time.Duration) error {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		resp, err := http.Get(s.url + "/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return nil
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	logs, _ := run("docker", "logs", "--tail", "40", s.name)
	return fmt.Errorf("headscale not healthy after %v\n%s", d, logs)
}

// hs runs the headscale CLI inside the container.
func (s *suite) hs(args ...string) (string, error) {
	out, err := run("docker", append([]string{"exec", s.name, "headscale"}, args...)...)
	if err != nil {
		return out, fmt.Errorf("headscale %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out, nil
}

func (s *suite) homeEnv() map[string]string {
	return map[string]string{
		"HOME":            s.home,
		"XDG_CONFIG_HOME": filepath.Join(s.home, ".config"),
		"XDG_STATE_HOME":  filepath.Join(s.home, ".local", "state"),
		"XDG_DATA_HOME":   filepath.Join(s.home, ".local", "share"),
		"XDG_CACHE_HOME":  filepath.Join(s.home, ".cache"),
	}
}

// writeAppConfig writes headsup's config.toml: the [host] hint a container
// install uses, and nothing else.
func (s *suite) writeAppConfig() error {
	s.cfgFile = filepath.Join(s.home, ".config", brand.Name, "config.toml")
	if err := os.MkdirAll(filepath.Dir(s.cfgFile), 0o700); err != nil {
		return err
	}
	b := fmt.Sprintf("[host]\ncontainer = %q\nheadscale_config = %q\n", s.name, s.configPath())
	return os.WriteFile(s.cfgFile, []byte(b), 0o600)
}

// result is one CLI run.
type result struct {
	args           []string
	stdout, stderr string
	code           int
}

func (r result) String() string {
	return fmt.Sprintf("%s %s: exit %d\nstdout:\n%s\nstderr:\n%s", brand.Name, strings.Join(r.args, " "), r.code, r.stdout, r.stderr)
}

// cli runs the headsup binary in host mode (the [host] config), with no TTY.
func (s *suite) cli(t *testing.T, args ...string) result {
	t.Helper()
	return s.cliEnv(t, nil, args...)
}

// remoteCLI runs the binary in remote mode with the API key in the env.
func (s *suite) remoteCLI(t *testing.T, args ...string) result {
	t.Helper()
	return s.cliEnv(t, []string{"HEADSCALE_API_KEY=" + s.apiKey}, append([]string{"--remote", "--url", s.url}, args...)...)
}

func (s *suite) cliEnv(t *testing.T, extra []string, args ...string) result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.bin, args...)
	cmd.Env = s.childEnv(extra)
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr, cmd.Stdin = &so, &se, nil
	err := cmd.Run()
	r := result{args: args, stdout: so.String(), stderr: se.String()}
	var ee *exec.ExitError
	switch {
	case errors.As(err, &ee):
		r.code = ee.ExitCode()
	case err != nil:
		t.Fatalf("running %s: %v", brand.Name, err)
	}
	return r
}

func (s *suite) childEnv(extra []string) []string {
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "HEADSCALE_") || strings.HasPrefix(k, strings.ToUpper(brand.Name)+"_") {
			continue
		}
		env = append(env, kv)
	}
	for k, v := range s.homeEnv() {
		env = append(env, k+"="+v)
	}
	return append(env, extra...)
}

// ok fails the test unless r exited 0.
func ok(t *testing.T, r result) result {
	t.Helper()
	if r.code != 0 {
		t.Fatalf("expected success:\n%s", r)
	}
	return r
}

// exitCode fails the test unless r exited with code.
func exitCode(t *testing.T, r result, code int) result {
	t.Helper()
	if r.code != code {
		t.Fatalf("expected exit %d:\n%s", code, r)
	}
	return r
}

// seedPolicy stores basePolicy in the database.
func (s *suite) seedPolicy() error {
	c, err := httpapi.New(s.url, api.NewSecret(s.apiKey))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := c.Connect(ctx); err != nil {
		return err
	}
	_, err = c.SetPolicy(ctx, basePolicy)
	return err
}

// client returns the API client remote mode uses.
func (s *suite) client(t *testing.T) *httpapi.Client {
	t.Helper()
	c, err := httpapi.New(s.url, api.NewSecret(s.apiKey))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := c.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	return c
}

// --- tailscale clients ---

// tsClient is one tailscale client container.
type tsClient struct {
	s         *suite
	container string
	hostname  string
}

// startClient runs a tailscale client. With an auth key it joins at once;
// without one it waits for a web login whose URL loginURL returns.
func (s *suite) startClient(t *testing.T, hostname, authKey string, upArgs ...string) *tsClient {
	t.Helper()
	c := &tsClient{s: s, container: s.prefix + "-" + hostname, hostname: hostname}
	_, _ = run("docker", "rm", "-f", c.container)
	args := []string{"run", "-d", "--name", c.container, "--hostname", hostname,
		"-e", "TS_USERSPACE=true", "-e", "TS_STATE_DIR=/var/lib/tailscale", "-e", "TS_ACCEPT_DNS=true",
		"-e", "TS_EXTRA_ARGS=" + strings.Join(append([]string{"--login-server=" + s.serverURL}, upArgs...), " ")}
	if authKey != "" {
		args = append(args, "-e", "TS_AUTHKEY="+authKey)
	}
	args = append(args, s.tsImage)
	if out, err := run("docker", args...); err != nil {
		t.Fatalf("starting client %s: %v\n%s", hostname, err, out)
	}
	s.mu.Lock()
	s.clients = append(s.clients, c.container)
	s.mu.Unlock()
	return c
}

var loginURLRE = regexp.MustCompile(`https?://\S+/register/\S+`)

// loginURL waits for the web-login URL the client prints.
func (c *tsClient) loginURL(t *testing.T) string {
	t.Helper()
	var out string
	for i := 0; i < 120; i++ {
		out, _ = run("docker", "logs", c.container)
		if m := loginURLRE.FindString(out); m != "" {
			return m
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("client %s printed no login URL:\n%s", c.hostname, out)
	return ""
}

// exec runs a command in the client container.
func (c *tsClient) exec(args ...string) (string, error) {
	return run("docker", append([]string{"exec", c.container}, args...)...)
}

func (c *tsClient) stop(t *testing.T) {
	t.Helper()
	if out, err := run("docker", "stop", "-t", "2", c.container); err != nil {
		t.Fatalf("stopping %s: %v\n%s", c.hostname, err, out)
	}
}

// --- small helpers ---

// eventually retries f until it returns nil or d passes.
func eventually(t *testing.T, d time.Duration, what string, f func() error) {
	t.Helper()
	deadline := time.Now().Add(d)
	var err error
	for {
		if err = f(); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: not within %v: %v", what, d, err)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func run(name string, args ...string) (string, error) { return runIn("", nil, name, args...) }

func runIn(dir string, env []string, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func repoRoot() (string, error) {
	out, err := run("go", "env", "GOMOD")
	if err != nil {
		return "", fmt.Errorf("go env GOMOD: %v: %s", err, out)
	}
	return filepath.Dir(strings.TrimSpace(out)), nil
}

// lastJSON returns the last line of out that looks like JSON (the CLI may
// log before it).
func lastJSON(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if strings.HasPrefix(l, "\"") || strings.HasPrefix(l, "{") || strings.HasPrefix(l, "[") {
			return l
		}
	}
	return strings.TrimSpace(out)
}

// decode unmarshals a CLI's JSON stdout.
func decode[T any](t *testing.T, r result) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(r.stdout), &v); err != nil {
		t.Fatalf("decoding JSON: %v\n%s", err, r)
	}
	return v
}

func errf(format string, a ...any) error { return fmt.Errorf(format, a...) }
