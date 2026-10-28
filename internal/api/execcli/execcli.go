// Package execcli is an api.Backend that runs the local `headscale` CLI with
// `-o json`, directly or through `docker|podman exec`. It is the host-mode
// transport for 0.29.x, whose unix socket speaks gRPC only.
//
// Subcommands and flags were checked against headscale v0.29.6 and main
// cmd/headscale/cli/*.go. They are the same on both except:
//
//   - `preauthkeys create --user`: a numeric user ID on 0.29 (Uint64P), "ID,
//     or name if not a number" on main. The backend always passes the ID.
//   - JSON output: 0.29 marshals protobuf Go structs with encoding/json
//     (snake_case, numeric IDs, {seconds,nanos} timestamps, integer enums);
//     main marshals oapi-codegen types (camelCase, string IDs, RFC 3339).
//     package wire decodes both.
//   - Error text on stderr: 0.29 wraps gRPC statuses ("rpc error: code = X
//     desc = ..."); main wraps HTTP client errors.
//
// `nodes delete`, `users destroy` and `nodes backfillips` prompt unless
// `--force` is given, and print a success-shaped "not deleted" result when
// the prompt is declined, so the backend always passes `--force` to
// destructive commands. `policy get` prints raw HuJSON regardless of -o.
package execcli

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tonhe/headsup/internal/api"
	"github.com/tonhe/headsup/internal/api/wire"
)

// Runner executes argv (argv[0] is the program) and returns its output. A
// non-nil err means the process failed to start or exited non-zero.
type Runner func(ctx context.Context, args ...string) (stdout, stderr []byte, err error)

// ExecRunner runs commands with os/exec.
func ExecRunner(ctx context.Context, args ...string) ([]byte, []byte, error) {
	if len(args) == 0 {
		return nil, nil, errors.New("no command")
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	return []byte(out.String()), []byte(errb.String()), err
}

// Option configures a Backend.
type Option func(*Backend)

// WithRunner replaces the process runner (tests, remote shells).
func WithRunner(r Runner) Option { return func(b *Backend) { b.run = r } }

// WithBinary sets the headscale binary path (default "headscale").
func WithBinary(p string) Option { return func(b *Backend) { b.binary = p } }

// WithConfig passes `-c path` to every command.
func WithConfig(p string) Option { return func(b *Backend) { b.config = p } }

// WithContainer runs `<runtime> exec <name> headscale ...` (runtime is
// docker or podman).
func WithContainer(runtime, name string) Option {
	return func(b *Backend) { b.runtime, b.container = runtime, name }
}

// WithVersion presets the version (and capabilities) before Connect.
func WithVersion(v string) Option {
	return func(b *Backend) { b.version = v; b.caps = capsFor(v) }
}

// Backend implements api.Backend over the headscale CLI.
type Backend struct {
	run       Runner
	binary    string
	config    string
	runtime   string
	container string
	now       func() time.Time

	mu      sync.Mutex
	version string
	caps    api.Capabilities
}

var _ api.Backend = (*Backend)(nil)

// New builds a CLI backend.
func New(opts ...Option) *Backend {
	b := &Backend{run: ExecRunner, binary: "headscale", now: time.Now, caps: capsFor("")}
	for _, o := range opts {
		o(b)
	}
	return b
}

func capsFor(v string) api.Capabilities {
	// problem+json describes REST error bodies; it has no CLI meaning.
	return wire.CapsFor(v).Without(api.CapProblemJSON)
}

// argv builds the full command line for a headscale subcommand.
func (b *Backend) argv(sub ...string) []string {
	var a []string
	if b.container != "" {
		rt := b.runtime
		if rt == "" {
			rt = "docker"
		}
		bin := b.binary
		if bin == "" || bin == "headscale" {
			bin = "headscale"
		}
		a = append(a, rt, "exec", b.container, bin)
	} else {
		a = append(a, b.binary)
	}
	a = append(a, sub...)
	if b.config != "" {
		a = append(a, "-c", b.config)
	}
	return append(a, "-o", "json")
}

// exec runs a subcommand and returns stdout, mapping failures to *api.Error.
func (b *Backend) exec(ctx context.Context, sub ...string) ([]byte, error) {
	ctx, cancel := api.WithTimeout(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, ctxError(err)
	}
	out, errb, err := b.run(ctx, b.argv(sub...)...)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctxError(ctx.Err())
		}
		return nil, cliError(err, errb, out)
	}
	return out, nil
}

func ctxError(err error) *api.Error {
	e := api.Errf(api.KindUnreachable, "%v", err)
	if errors.Is(err, context.DeadlineExceeded) {
		e.Message = "headscale CLI timed out"
	}
	e.Err = err
	return e
}

var (
	grpcCode   = regexp.MustCompile(`code = (\w+) desc = (.*)`)
	httpStatus = regexp.MustCompile(`\b([45]\d\d) [A-Z][a-zA-Z ]+`)
)

// cliError maps a failed run (exit status plus stderr) to *api.Error.
func cliError(runErr error, stderr, stdout []byte) *api.Error {
	if errors.Is(runErr, exec.ErrNotFound) || errors.Is(runErr, os.ErrNotExist) {
		e := api.Errf(api.KindUnreachable, "headscale CLI not found: %v", runErr)
		e.Hint = "Install headscale or set its binary path."
		e.Err = runErr
		return e
	}
	msg := strings.TrimSpace(string(stderr))
	if o, err := wire.DecodeObj(stderr); err == nil && o.Str("error") != "" {
		msg = o.Str("error")
	} else if o, err := wire.DecodeObj(stdout); err == nil && o.Str("error") != "" {
		msg = o.Str("error")
	}
	msg = strings.TrimPrefix(msg, "Error: ")
	if msg == "" {
		msg = runErr.Error()
	}
	e := api.Errf(classify(msg), "%s", msg)
	e.Err = runErr
	var xe *exec.ExitError
	if errors.As(runErr, &xe) {
		e.Status = xe.ExitCode()
	}
	if e.Kind == api.KindInvalid {
		e.Line, e.Col = wire.PolicyPos(msg)
	}
	if e.Kind == api.KindUnauthorized && strings.Contains(strings.ToLower(msg), "permission denied") {
		e.Hint = "Run as root or as a member of the headscale group to use the local socket."
	}
	return e
}

func classify(msg string) api.ErrorKind {
	if m := grpcCode.FindStringSubmatch(msg); m != nil {
		switch m[1] {
		case "NotFound":
			return api.KindNotFound
		case "InvalidArgument", "FailedPrecondition", "OutOfRange":
			return api.KindInvalid
		case "AlreadyExists":
			return api.KindConflict
		case "Unauthenticated", "PermissionDenied":
			return api.KindUnauthorized
		case "Unimplemented":
			return api.KindUnsupported
		case "Unavailable", "DeadlineExceeded":
			return api.KindUnreachable
		}
	}
	l := strings.ToLower(msg)
	switch {
	case strings.Contains(l, "unknown command") || strings.Contains(l, "unknown flag") || strings.Contains(l, "unknown shorthand"):
		return api.KindUnsupported
	case strings.Contains(l, "permission denied"):
		return api.KindUnauthorized
	case strings.Contains(l, "connection refused") || strings.Contains(l, "no such file or directory") ||
		strings.Contains(l, "dial unix") || strings.Contains(l, "dial tcp") || strings.Contains(l, "is not running"):
		return api.KindUnreachable
	}
	if m := httpStatus.FindStringSubmatch(msg); m != nil {
		n, _ := strconv.Atoi(m[1])
		switch {
		case n == 401 || n == 403:
			return api.KindUnauthorized
		case n == 404:
			return api.KindNotFound
		case n == 409:
			return api.KindConflict
		case n == 400 || n == 422:
			return api.KindInvalid
		case n == 501:
			return api.KindUnsupported
		}
	}
	switch {
	case strings.Contains(l, "not found") || strings.Contains(l, "no user matches") || strings.Contains(l, "no pending auth"):
		return api.KindNotFound
	case strings.Contains(l, "already exists") || strings.Contains(l, "already in use"):
		return api.KindConflict
	case strings.Contains(l, "unauthorized") || strings.Contains(l, "unauthenticated"):
		return api.KindUnauthorized
	case strings.Contains(l, "invalid") || strings.Contains(l, "parsing") || strings.Contains(l, "hujson") ||
		strings.Contains(l, "missing parameters") || strings.Contains(l, "required"):
		return api.KindInvalid
	}
	return api.KindServer
}

func decode(out []byte) (any, error) {
	if strings.TrimSpace(string(out)) == "" || strings.TrimSpace(string(out)) == "null" {
		return []any{}, nil
	}
	v, err := wire.Decode(out)
	if err != nil {
		return nil, &api.Error{Kind: api.KindServer, Message: "decoding CLI output: " + err.Error(), Err: err}
	}
	return v, nil
}

func decodeObj(out []byte) (wire.Obj, error) {
	v, err := decode(out)
	if err != nil {
		return nil, err
	}
	o, ok := v.(wire.Obj)
	if !ok {
		return nil, api.Errf(api.KindServer, "unexpected CLI output %T", v)
	}
	return o, nil
}

func (b *Backend) gate(c api.Cap) error {
	if !b.Capabilities().Has(c) {
		return api.Errf(api.KindUnsupported, "%s is not supported by this headscale", c)
	}
	return nil
}

// Capabilities reports what the local headscale supports.
func (b *Backend) Capabilities() api.Capabilities {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.caps
}

// Connect runs `headscale version` and `headscale health`.
func (b *Backend) Connect(ctx context.Context) (api.ServerInfo, error) {
	out, err := b.exec(ctx, "version")
	if err != nil {
		return api.ServerInfo{}, err
	}
	ver := ""
	if o, err := decodeObj(out); err == nil {
		ver = o.Str("version")
	} else {
		ver = strings.TrimSpace(string(out))
	}
	if ver == "" {
		ver = "unknown"
	}
	caps := capsFor(ver)
	info := api.ServerInfo{Version: ver}
	hout, err := b.exec(ctx, "health")
	switch {
	case err == nil:
		o, _ := decodeObj(hout)
		info.Healthy = o == nil || o.Get("databaseconnectivity") == nil || o.Bool("databaseconnectivity")
	case api.IsKind(err, api.KindUnsupported):
		caps = caps.Without(api.CapHealthEndpoint)
		if _, err := b.exec(ctx, "users", "list"); err != nil {
			return info, err
		}
		info.Healthy = true
	default:
		return info, err
	}
	b.mu.Lock()
	b.version, b.caps = ver, caps
	b.mu.Unlock()
	info.Caps = caps
	return info, nil
}

func id(n uint64) string { return strconv.FormatUint(n, 10) }

func (b *Backend) nodeCmd(ctx context.Context, sub ...string) (api.Node, error) {
	out, err := b.exec(ctx, sub...)
	if err != nil {
		return api.Node{}, err
	}
	o, err := decodeObj(out)
	if err != nil {
		return api.Node{}, err
	}
	if n := o.Obj("node"); n != nil { // tolerate a wrapped response
		o = n
	}
	return wire.Node(o), nil
}

// ListNodes runs `nodes list [-u user]`.
func (b *Backend) ListNodes(ctx context.Context, f api.NodeFilter) ([]api.Node, error) {
	sub := []string{"nodes", "list"}
	if f.User != "" {
		sub = append(sub, "-u", f.User)
	}
	out, err := b.exec(ctx, sub...)
	if err != nil {
		return nil, err
	}
	v, err := decode(out)
	if err != nil {
		return nil, err
	}
	return wire.Nodes(v), nil
}

// GetNode lists nodes and picks one; the CLI has no `nodes get`.
func (b *Backend) GetNode(ctx context.Context, nid uint64) (api.Node, error) {
	nodes, err := b.ListNodes(ctx, api.NodeFilter{})
	if err != nil {
		return api.Node{}, err
	}
	for _, n := range nodes {
		if n.ID == nid {
			return n, nil
		}
	}
	return api.Node{}, api.Errf(api.KindNotFound, "node %d not found", nid)
}

// RegisterNode runs `nodes register -u user -k key`.
func (b *Backend) RegisterNode(ctx context.Context, user, key string) (api.Node, error) {
	return b.nodeCmd(ctx, "nodes", "register", "-u", user, "-k", key)
}

// RenameNode runs `nodes rename -i id NEW_NAME`.
func (b *Backend) RenameNode(ctx context.Context, nid uint64, newName string) (api.Node, error) {
	return b.nodeCmd(ctx, "nodes", "rename", "-i", id(nid), newName)
}

// SetTags runs `nodes tag -i id --tags=a,b` (empty clears).
func (b *Backend) SetTags(ctx context.Context, nid uint64, tags []string) (api.Node, error) {
	return b.nodeCmd(ctx, "nodes", "tag", "-i", id(nid), "--tags="+strings.Join(tags, ","))
}

// SetApprovedRoutes runs `nodes approve-routes -i id --routes=a,b`.
func (b *Backend) SetApprovedRoutes(ctx context.Context, nid uint64, routes []netip.Prefix) (api.Node, error) {
	return b.nodeCmd(ctx, "nodes", "approve-routes", "-i", id(nid), "--routes="+strings.Join(wire.PrefixStrings(routes), ","))
}

// ExpireNode runs `nodes expire -i id [-e RFC3339 | -d]`.
func (b *Backend) ExpireNode(ctx context.Context, nid uint64, opt api.ExpireOpts) (api.Node, error) {
	sub := []string{"nodes", "expire", "-i", id(nid)}
	switch {
	case opt.DisableExpiry:
		if err := b.gate(api.CapDisableExpiry); err != nil {
			return api.Node{}, err
		}
		sub = append(sub, "-d")
	case !opt.At.IsZero():
		sub = append(sub, "-e", opt.At.UTC().Format(time.RFC3339))
	}
	return b.nodeCmd(ctx, sub...)
}

// DeleteNode runs `nodes delete -i id --force`.
func (b *Backend) DeleteNode(ctx context.Context, nid uint64) error {
	_, err := b.exec(ctx, "nodes", "delete", "-i", id(nid), "--force")
	return err
}

// BackfillIPs runs `nodes backfillips --force`.
func (b *Backend) BackfillIPs(ctx context.Context) ([]string, error) {
	if err := b.gate(api.CapBackfillIPs); err != nil {
		return nil, err
	}
	out, err := b.exec(ctx, "nodes", "backfillips", "--force")
	if err != nil {
		return nil, err
	}
	ch := []string{}
	if o, err := decodeObj(out); err == nil {
		ch = append(ch, o.Strs("changes")...)
	}
	return ch, nil
}

// ListUsers runs `users list [-i id] [-n name] [-e email]`.
func (b *Backend) ListUsers(ctx context.Context, f api.UserFilter) ([]api.User, error) {
	sub := []string{"users", "list"}
	if f.ID != 0 {
		sub = append(sub, "-i", id(f.ID))
	}
	if f.Name != "" {
		sub = append(sub, "-n", f.Name)
	}
	if f.Email != "" {
		sub = append(sub, "-e", f.Email)
	}
	out, err := b.exec(ctx, sub...)
	if err != nil {
		return nil, err
	}
	v, err := decode(out)
	if err != nil {
		return nil, err
	}
	return wire.Users(v), nil
}

func (b *Backend) userCmd(ctx context.Context, sub ...string) (api.User, error) {
	out, err := b.exec(ctx, sub...)
	if err != nil {
		return api.User{}, err
	}
	o, err := decodeObj(out)
	if err != nil {
		return api.User{}, err
	}
	if u := o.Obj("user"); u != nil {
		o = u
	}
	return wire.User(o), nil
}

// CreateUser runs `users create NAME [-d display] [-e email]`.
func (b *Backend) CreateUser(ctx context.Context, u api.NewUser) (api.User, error) {
	sub := []string{"users", "create", u.Name}
	if u.DisplayName != "" {
		sub = append(sub, "-d", u.DisplayName)
	}
	if u.Email != "" {
		sub = append(sub, "-e", u.Email)
	}
	return b.userCmd(ctx, sub...)
}

// RenameUser runs `users rename -i id -r new`.
func (b *Backend) RenameUser(ctx context.Context, uid uint64, newName string) (api.User, error) {
	return b.userCmd(ctx, "users", "rename", "-i", id(uid), "-r", newName)
}

// DeleteUser runs `users destroy -i id --force`.
func (b *Backend) DeleteUser(ctx context.Context, uid uint64) error {
	_, err := b.exec(ctx, "users", "destroy", "-i", id(uid), "--force")
	return err
}

// ListPreAuthKeys runs `preauthkeys list`; the user filter is local.
func (b *Backend) ListPreAuthKeys(ctx context.Context, f api.KeyFilter) ([]api.PreAuthKey, error) {
	out, err := b.exec(ctx, "preauthkeys", "list")
	if err != nil {
		return nil, err
	}
	v, err := decode(out)
	if err != nil {
		return nil, err
	}
	keys := wire.PreAuthKeys(v)
	if f.User == "" {
		return keys, nil
	}
	res := keys[:0]
	for _, k := range keys {
		if k.User != nil && k.User.Name == f.User {
			res = append(res, k)
		}
	}
	return res, nil
}

// duration renders time until t as a CLI duration ("3600s"), rounded up.
func duration(now, t time.Time) string {
	secs := int64(t.Sub(now).Seconds() + 0.999)
	if secs < 1 {
		secs = 1
	}
	return strconv.FormatInt(secs, 10) + "s"
}

func (b *Backend) userID(ctx context.Context, name string) (uint64, error) {
	users, err := b.ListUsers(ctx, api.UserFilter{Name: name})
	if err != nil {
		return 0, err
	}
	for _, u := range users {
		if u.Name == name {
			return u.ID, nil
		}
	}
	return 0, api.Errf(api.KindNotFound, "user %q not found", name)
}

// CreatePreAuthKey runs `preauthkeys create -u ID [-e DUR] [--reusable]
// [--ephemeral] [--tags=..]`.
func (b *Backend) CreatePreAuthKey(ctx context.Context, k api.NewPreAuthKey) (api.PreAuthKey, error) {
	sub := []string{"preauthkeys", "create"}
	if k.User == "" {
		if err := b.gate(api.CapTagOnlyKeys); err != nil {
			return api.PreAuthKey{}, err
		}
	} else {
		uid, err := b.userID(ctx, k.User)
		if err != nil {
			return api.PreAuthKey{}, err
		}
		sub = append(sub, "-u", id(uid))
	}
	if !k.Expiration.IsZero() {
		sub = append(sub, "-e", duration(b.now(), k.Expiration))
	}
	if k.Reusable {
		sub = append(sub, "--reusable")
	}
	if k.Ephemeral {
		sub = append(sub, "--ephemeral")
	}
	if len(k.ACLTags) > 0 {
		sub = append(sub, "--tags="+strings.Join(k.ACLTags, ","))
	}
	out, err := b.exec(ctx, sub...)
	if err != nil {
		return api.PreAuthKey{}, err
	}
	o, err := decodeObj(out)
	if err != nil {
		return api.PreAuthKey{}, err
	}
	if pk := o.Obj("preauthkey"); pk != nil {
		o = pk
	}
	return wire.PreAuthKey(o), nil
}

// ExpirePreAuthKey runs `preauthkeys expire -i id`.
func (b *Backend) ExpirePreAuthKey(ctx context.Context, kid uint64) error {
	_, err := b.exec(ctx, "preauthkeys", "expire", "-i", id(kid))
	return err
}

// DeletePreAuthKey runs `preauthkeys delete -i id --force`.
func (b *Backend) DeletePreAuthKey(ctx context.Context, kid uint64) error {
	if err := b.gate(api.CapPreAuthKeyDelete); err != nil {
		return err
	}
	_, err := b.exec(ctx, "preauthkeys", "delete", "-i", id(kid), "--force")
	return err
}

// ListAPIKeys runs `apikeys list`.
func (b *Backend) ListAPIKeys(ctx context.Context) ([]api.APIKey, error) {
	out, err := b.exec(ctx, "apikeys", "list")
	if err != nil {
		return nil, err
	}
	v, err := decode(out)
	if err != nil {
		return nil, err
	}
	return wire.APIKeys(v), nil
}

// CreateAPIKey runs `apikeys create [-e DUR]`; stdout is a JSON string.
func (b *Backend) CreateAPIKey(ctx context.Context, expiration time.Time) (api.Secret, error) {
	sub := []string{"apikeys", "create"}
	if !expiration.IsZero() {
		sub = append(sub, "-e", duration(b.now(), expiration))
	}
	out, err := b.exec(ctx, sub...)
	if err != nil {
		return api.Secret{}, err
	}
	v, err := wire.Decode(out)
	key := ""
	switch t := v.(type) {
	case string:
		key = t
	case wire.Obj:
		key = t.Str("apikey")
	}
	if err != nil {
		key = strings.TrimSpace(string(out))
	}
	if key == "" {
		return api.Secret{}, api.Errf(api.KindServer, "CLI returned no API key")
	}
	return api.NewSecret(key), nil
}

// ExpireAPIKey runs `apikeys expire -p prefix`.
func (b *Backend) ExpireAPIKey(ctx context.Context, prefix string) error {
	_, err := b.exec(ctx, "apikeys", "expire", "-p", prefix)
	return err
}

// DeleteAPIKey runs `apikeys delete -p prefix --force`.
func (b *Backend) DeleteAPIKey(ctx context.Context, prefix string) error {
	if err := b.gate(api.CapAPIKeyDelete); err != nil {
		return err
	}
	_, err := b.exec(ctx, "apikeys", "delete", "-p", prefix, "--force")
	return err
}

// GetPolicy runs `policy get` (raw HuJSON on stdout; no update time).
func (b *Backend) GetPolicy(ctx context.Context) (api.Policy, error) {
	out, err := b.exec(ctx, "policy", "get")
	if err != nil {
		return api.Policy{}, err
	}
	return api.Policy{Raw: strings.TrimSuffix(string(out), "\n")}, nil
}

// withPolicyFile writes raw to a temp file visible to headscale (copied into
// the container when one is used) and calls fn with its path.
func (b *Backend) withPolicyFile(ctx context.Context, raw string, fn func(path string) error) error {
	f, err := os.CreateTemp("", "headsup-policy-*.hujson")
	if err != nil {
		return api.Errf(api.KindServer, "creating temp policy file: %v", err)
	}
	host := f.Name()
	defer os.Remove(host)
	if _, err := f.WriteString(raw); err != nil {
		f.Close()
		return api.Errf(api.KindServer, "writing temp policy file: %v", err)
	}
	if err := f.Close(); err != nil {
		return api.Errf(api.KindServer, "writing temp policy file: %v", err)
	}
	if b.container == "" {
		return fn(host)
	}
	rt := b.runtime
	if rt == "" {
		rt = "docker"
	}
	inCtr := "/tmp/" + path.Base(host)
	if _, errb, err := b.run(ctx, rt, "cp", host, b.container+":"+inCtr); err != nil {
		return cliError(err, errb, nil)
	}
	defer func() { _, _, _ = b.run(context.WithoutCancel(ctx), rt, "exec", b.container, "rm", "-f", inCtr) }()
	return fn(inCtr)
}

func policyErr(err error) error {
	if err == nil {
		return nil
	}
	e := api.AsError(err)
	if e.Kind == api.KindServer {
		e.Kind = api.KindInvalid
	}
	if e.Kind == api.KindInvalid && e.Line == 0 {
		e.Line, e.Col = wire.PolicyPos(e.Message)
	}
	return e
}

// CheckPolicy runs `policy check -f <tmp>`.
func (b *Backend) CheckPolicy(ctx context.Context, raw string) error {
	if err := b.gate(api.CapPolicyCheck); err != nil {
		return err
	}
	return b.withPolicyFile(ctx, raw, func(p string) error {
		_, err := b.exec(ctx, "policy", "check", "-f", p)
		return policyErr(err)
	})
}

// SetPolicy runs `policy set -f <tmp>`.
func (b *Backend) SetPolicy(ctx context.Context, raw string) (api.Policy, error) {
	if err := b.gate(api.CapPolicyWritable); err != nil {
		return api.Policy{}, err
	}
	err := b.withPolicyFile(ctx, raw, func(p string) error {
		_, err := b.exec(ctx, "policy", "set", "-f", p)
		if err != nil {
			e := api.AsError(err)
			m := strings.ToLower(e.Message)
			if strings.Contains(m, "mode") && strings.Contains(m, "database") && !strings.Contains(m, "line") {
				b.mu.Lock()
				b.caps = b.caps.Without(api.CapPolicyWritable)
				b.mu.Unlock()
				u := api.Errf(api.KindUnsupported, "%s", e.Message)
				u.Hint = "The server reads its policy from a file (policy.mode: file). Edit the file on the host."
				u.Err = e
				return u
			}
			return policyErr(err)
		}
		return nil
	})
	if err != nil {
		return api.Policy{}, err
	}
	return api.Policy{Raw: raw, UpdatedAt: b.now()}, nil
}

// AuthRegister runs `auth register --auth-id ID -u user`.
func (b *Backend) AuthRegister(ctx context.Context, authID, user string) error {
	if err := b.gate(api.CapAuthApproval); err != nil {
		return err
	}
	_, err := b.exec(ctx, "auth", "register", "--auth-id", authID, "-u", user)
	return err
}

// AuthApprove runs `auth approve --auth-id ID`.
func (b *Backend) AuthApprove(ctx context.Context, authID string) error {
	if err := b.gate(api.CapAuthApproval); err != nil {
		return err
	}
	_, err := b.exec(ctx, "auth", "approve", "--auth-id", authID)
	return err
}

// AuthReject runs `auth reject --auth-id ID`.
func (b *Backend) AuthReject(ctx context.Context, authID string) error {
	if err := b.gate(api.CapAuthApproval); err != nil {
		return err
	}
	_, err := b.exec(ctx, "auth", "reject", "--auth-id", authID)
	return err
}

// String describes the backend.
func (b *Backend) String() string {
	if b.container != "" {
		return fmt.Sprintf("execcli(%s exec %s)", b.runtime, b.container)
	}
	return fmt.Sprintf("execcli(%s)", b.binary)
}
