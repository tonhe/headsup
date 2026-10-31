// Package httpapi is the REST /api/v1 backend for headscale 0.29 (served by
// grpc-gateway) and 0.30+ (served by Huma), over TCP or a unix socket.
//
// Endpoints were checked against headscale v0.29.6
// gen/openapiv2/headscale/v1/headscale.swagger.json and main's
// hscontrol/api/v1/*.go (the source of /api/v1/openapi). Differences:
//
//   - POST /node/{id}/expire: 0.29 takes `expiry` and `disableExpiry` as
//     query parameters; 0.30 takes a JSON body {expiry, disableExpiry}. The
//     client sends both forms; each server ignores the one it does not read.
//   - Errors: 0.29 returns grpc-gateway {code, message, details}; 0.30
//     returns RFC 7807 application/problem+json {title, status, detail,
//     errors}.
//   - JSON: 0.29.6 uses the gateway's default protojson marshaler (camelCase,
//     not proto names); 0.30 is camelCase too. Both encode uint64 as strings.
//     The decoder accepts snake_case as well (see package wire).
//   - GET /api/v1/health requires the bearer key on both; GET /version
//     (outside /api) is unauthenticated on both.
//   - Unix socket: 0.30 serves /api/v1 over the socket with local trust (no
//     auth header). 0.29's socket speaks gRPC only, so use execcli there.
//   - POST /preauthkey takes the numeric user ID (string-encoded) on both, so
//     CreatePreAuthKey resolves the user name first.
package httpapi

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tonhe/headsup/internal/api"
	"github.com/tonhe/headsup/internal/api/wire"
)

// Option configures a Client.
type Option func(*Client) error

// WithUnixSocket dials path instead of TCP and sends no Authorization header.
func WithUnixSocket(path string) Option {
	return func(c *Client) error { c.socket = path; return nil }
}

// WithHTTPClient replaces the HTTP client (its transport is used as-is).
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) error { c.hc = hc; c.custom = true; return nil }
}

// WithInsecureSkipVerify disables TLS certificate verification.
func WithInsecureSkipVerify() Option {
	return func(c *Client) error { c.tlsConf().InsecureSkipVerify = true; return nil } //nolint:gosec // opt-in
}

// WithCAFile trusts the PEM certificates in path.
func WithCAFile(path string) Option {
	return func(c *Client) error {
		pem, err := os.ReadFile(path)
		if err != nil {
			return api.Errf(api.KindInvalid, "reading CA file: %v", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return api.Errf(api.KindInvalid, "no certificates found in %s", path)
		}
		c.tlsConf().RootCAs = pool
		return nil
	}
}

// WithTimeout bounds each request when the caller's ctx has no deadline.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) error { c.timeout = d; return nil }
}

// WithVersion presets the server version (and so Capabilities) before Connect.
func WithVersion(v string) Option {
	return func(c *Client) error { c.version = v; c.caps = wire.CapsFor(v); return nil }
}

// Client implements api.Backend over REST.
type Client struct {
	base    *url.URL
	key     api.Secret
	socket  string
	hc      *http.Client
	custom  bool
	tls     *tls.Config
	timeout time.Duration

	mu      sync.Mutex
	version string
	caps    api.Capabilities
}

var _ api.Backend = (*Client)(nil)

// SelfAPIKey returns the API key this client authenticates with, so the UI
// can warn before the admin expires or deletes it and locks the app out.
func (c *Client) SelfAPIKey() api.Secret { return c.key }

// New builds a client for baseURL (e.g. https://hs.example.com). With
// WithUnixSocket, baseURL may be empty.
func New(baseURL string, key api.Secret, opts ...Option) (*Client, error) {
	if baseURL == "" {
		baseURL = "http://unix"
	}
	u, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, api.Errf(api.KindInvalid, "invalid server URL %q", baseURL)
	}
	u.Path = strings.TrimSuffix(u.Path, "/api/v1")
	c := &Client{base: u, key: key, timeout: api.DefaultTimeout, caps: wire.CapsFor("")}
	for _, o := range opts {
		if err := o(c); err != nil {
			return nil, api.AsError(err)
		}
	}
	if !c.custom {
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.TLSClientConfig = c.tls
		if c.socket != "" {
			sock := c.socket
			tr.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", sock)
			}
		}
		c.hc = &http.Client{Transport: tr}
	}
	return c, nil
}

func (c *Client) tlsConf() *tls.Config {
	if c.tls == nil {
		c.tls = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	return c.tls
}

// Capabilities reports what the connected server supports.
func (c *Client) Capabilities() api.Capabilities {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.caps
}

func (c *Client) gate(x api.Cap) error {
	if !c.Capabilities().Has(x) {
		return api.Errf(api.KindUnsupported, "%s is not supported by this server", x)
	}
	return nil
}

func (c *Client) dropCap(x api.Cap) {
	c.mu.Lock()
	c.caps = c.caps.Without(x)
	c.mu.Unlock()
}

// response is a decoded reply.
type response struct {
	status int
	ctype  string
	body   []byte
}

// do performs one request. body is JSON-encoded when non-nil. auth=false
// skips the Authorization header (used for /version).
func (c *Client) do(ctx context.Context, method, path string, q url.Values, body any, auth bool) (*response, error) {
	if _, ok := ctx.Deadline(); !ok && c.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}
	u := *c.base
	u.Path = strings.TrimRight(c.base.Path, "/") + path
	if len(q) > 0 {
		u.RawQuery = q.Encode()
	}
	var rd io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, api.Errf(api.KindInvalid, "encoding request: %v", err)
		}
		rd = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rd)
	if err != nil {
		return nil, api.Errf(api.KindInvalid, "building request: %v", err)
	}
	req.Header.Set("Accept", "application/json, application/problem+json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth && c.socket == "" && !c.key.IsZero() {
		req.Header.Set("Authorization", "Bearer "+c.key.Reveal())
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, transportError(ctx, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, transportError(ctx, err)
	}
	r := &response{status: resp.StatusCode, ctype: resp.Header.Get("Content-Type"), body: data}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return r, httpError(r)
	}
	return r, nil
}

// call performs an authenticated request and decodes a JSON object reply.
func (c *Client) call(ctx context.Context, method, path string, q url.Values, body any) (wire.Obj, error) {
	r, err := c.do(ctx, method, path, q, body, true)
	if err != nil {
		return nil, err
	}
	o, err := wire.DecodeObj(r.body)
	if err != nil {
		return nil, &api.Error{Kind: api.KindServer, Message: "decoding response: " + err.Error(), Status: r.status, Err: err}
	}
	return o, nil
}

func transportError(ctx context.Context, err error) *api.Error {
	e := &api.Error{Kind: api.KindUnreachable, Message: err.Error(), Err: err}
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded):
		e.Message = "request timed out"
		e.Hint = "The server did not answer in time. Check the address and network."
	case errors.Is(ctx.Err(), context.Canceled):
		e.Message = "request cancelled"
	default:
		var ue *url.Error
		var ce *tls.CertificateVerificationError
		switch {
		case errors.As(err, &ce):
			e.Hint = "The server certificate is not trusted. Set a CA file or allow insecure TLS."
		case errors.As(err, &ue):
			e.Message = ue.Err.Error()
			e.Hint = "Check the server address and that headscale is running."
		default:
			e.Hint = "Check the server address and that headscale is running."
		}
	}
	return e
}

// gRPC status codes used by grpc-gateway error bodies.
const (
	grpcInvalidArgument    = 3
	grpcDeadlineExceeded   = 4
	grpcNotFound           = 5
	grpcAlreadyExists      = 6
	grpcPermissionDenied   = 7
	grpcFailedPrecondition = 9
	grpcUnimplemented      = 12
	grpcUnavailable        = 14
	grpcUnauthenticated    = 16
)

// httpError normalises an error reply (problem+json or grpc-gateway) into
// *api.Error.
func httpError(r *response) *api.Error {
	kind := kindForStatus(r.status)
	msg := strings.TrimSpace(string(r.body))
	if o, err := wire.DecodeObj(r.body); err == nil && len(o) > 0 {
		switch {
		case o.Get("detail") != nil || o.Get("title") != nil: // RFC 7807 (0.30)
			msg = o.Str("detail")
			if msg == "" {
				msg = o.Str("title")
			}
			for _, d := range o.Objs("errors") {
				if m := d.Str("message"); m != "" && !strings.Contains(msg, m) {
					msg += ": " + m
				}
			}
		case o.Get("code") != nil || o.Get("message") != nil: // grpc-gateway (0.29)
			msg = o.Str("message")
			if k, ok := kindForGRPC(int(wire.ToU64(o.Get("code")))); ok {
				kind = k
			}
		case o.Get("error") != nil:
			msg = o.Str("error")
		}
	}
	if msg == "" {
		msg = http.StatusText(r.status)
	}
	e := api.Errf(kind, "%s", msg)
	e.Status = r.status
	if kind == api.KindInvalid {
		e.Line, e.Col = wire.PolicyPos(msg)
	}
	return e
}

func kindForStatus(s int) api.ErrorKind {
	switch {
	case s == http.StatusUnauthorized || s == http.StatusForbidden:
		return api.KindUnauthorized
	case s == http.StatusNotFound:
		return api.KindNotFound
	case s == http.StatusConflict:
		return api.KindConflict
	case s == http.StatusBadRequest || s == http.StatusUnprocessableEntity:
		return api.KindInvalid
	case s == http.StatusNotImplemented || s == http.StatusMethodNotAllowed:
		return api.KindUnsupported
	case s == http.StatusBadGateway || s == http.StatusServiceUnavailable || s == http.StatusGatewayTimeout:
		return api.KindUnreachable
	default:
		return api.KindServer
	}
}

func kindForGRPC(code int) (api.ErrorKind, bool) {
	switch code {
	case grpcInvalidArgument, grpcFailedPrecondition:
		return api.KindInvalid, true
	case grpcNotFound:
		return api.KindNotFound, true
	case grpcAlreadyExists:
		return api.KindConflict, true
	case grpcPermissionDenied, grpcUnauthenticated:
		return api.KindUnauthorized, true
	case grpcUnimplemented:
		return api.KindUnsupported, true
	case grpcUnavailable, grpcDeadlineExceeded:
		return api.KindUnreachable, true
	}
	return 0, false
}

func idStr(id uint64) string { return strconv.FormatUint(id, 10) }

// Connect checks health, detects the version and sets capabilities.
func (c *Client) Connect(ctx context.Context) (api.ServerInfo, error) {
	info := api.ServerInfo{}
	ver := ""
	if r, err := c.do(ctx, http.MethodGet, "/version", nil, nil, false); err == nil {
		if o, err := wire.DecodeObj(r.body); err == nil {
			ver = o.Str("version")
		}
	} else if api.IsKind(err, api.KindUnreachable) {
		return info, err
	}
	caps := wire.CapsFor(ver)
	if ver == "" {
		// No usable /version: assume the oldest supported line.
		caps = wire.CapsFor("v0.29.0")
		ver = "unknown"
	}

	h, err := c.do(ctx, http.MethodGet, "/api/v1/health", nil, nil, true)
	switch {
	case err == nil:
		o, _ := wire.DecodeObj(h.body)
		info.Healthy = o.Get("databaseconnectivity") == nil || o.Bool("databaseconnectivity")
	case api.IsKind(err, api.KindNotFound) || api.IsKind(err, api.KindUnsupported):
		caps = caps.Without(api.CapHealthEndpoint)
		// Probe a cheap authenticated endpoint instead.
		if _, err := c.do(ctx, http.MethodGet, "/api/v1/apikey", nil, nil, true); err != nil {
			return info, err
		}
		info.Healthy = true
	default:
		return info, err
	}
	if h != nil && strings.Contains(h.ctype, "problem+json") {
		caps = caps.With(api.CapProblemJSON)
	}

	c.mu.Lock()
	c.version, c.caps = ver, caps
	c.mu.Unlock()
	info.Version, info.Caps = ver, caps
	return info, nil
}

// ListNodes lists nodes, optionally for one user.
func (c *Client) ListNodes(ctx context.Context, f api.NodeFilter) ([]api.Node, error) {
	q := url.Values{}
	if f.User != "" {
		q.Set("user", f.User)
	}
	o, err := c.call(ctx, http.MethodGet, "/api/v1/node", q, nil)
	if err != nil {
		return nil, err
	}
	return wire.Nodes(o.Get("nodes")), nil
}

func nodeReply(o wire.Obj, err error) (api.Node, error) {
	if err != nil {
		return api.Node{}, err
	}
	n := o.Obj("node")
	if n == nil {
		return api.Node{}, api.Errf(api.KindServer, "response has no node")
	}
	return wire.Node(n), nil
}

// GetNode fetches one node.
func (c *Client) GetNode(ctx context.Context, id uint64) (api.Node, error) {
	return nodeReply(c.call(ctx, http.MethodGet, "/api/v1/node/"+idStr(id), nil, nil))
}

// RegisterNode registers a pending machine key for user.
func (c *Client) RegisterNode(ctx context.Context, user, key string) (api.Node, error) {
	q := url.Values{"user": {user}, "key": {key}}
	return nodeReply(c.call(ctx, http.MethodPost, "/api/v1/node/register", q, nil))
}

// RenameNode sets the node's given name.
func (c *Client) RenameNode(ctx context.Context, id uint64, newName string) (api.Node, error) {
	p := "/api/v1/node/" + idStr(id) + "/rename/" + url.PathEscape(newName)
	return nodeReply(c.call(ctx, http.MethodPost, p, nil, map[string]any{}))
}

// SetTags replaces the node's tags.
func (c *Client) SetTags(ctx context.Context, id uint64, tags []string) (api.Node, error) {
	if tags == nil {
		tags = []string{}
	}
	return nodeReply(c.call(ctx, http.MethodPost, "/api/v1/node/"+idStr(id)+"/tags", nil, map[string]any{"tags": tags}))
}

// SetApprovedRoutes replaces the node's approved routes.
func (c *Client) SetApprovedRoutes(ctx context.Context, id uint64, routes []netip.Prefix) (api.Node, error) {
	body := map[string]any{"routes": wire.PrefixStrings(routes)}
	return nodeReply(c.call(ctx, http.MethodPost, "/api/v1/node/"+idStr(id)+"/approve_routes", nil, body))
}

// ExpireNode expires the node now, at opt.At, or disables expiry.
func (c *Client) ExpireNode(ctx context.Context, id uint64, opt api.ExpireOpts) (api.Node, error) {
	if opt.DisableExpiry {
		if err := c.gate(api.CapDisableExpiry); err != nil {
			return api.Node{}, err
		}
	}
	q := url.Values{}
	body := map[string]any{}
	switch {
	case opt.DisableExpiry:
		q.Set("disableExpiry", "true")
		body["disableExpiry"] = true
	case !opt.At.IsZero():
		at := opt.At.UTC().Format(time.RFC3339Nano)
		q.Set("expiry", at)
		body["expiry"] = at
	}
	return nodeReply(c.call(ctx, http.MethodPost, "/api/v1/node/"+idStr(id)+"/expire", q, body))
}

// DeleteNode removes a node.
func (c *Client) DeleteNode(ctx context.Context, id uint64) error {
	_, err := c.call(ctx, http.MethodDelete, "/api/v1/node/"+idStr(id), nil, nil)
	return err
}

// BackfillIPs assigns missing addresses and reports the changes.
func (c *Client) BackfillIPs(ctx context.Context) ([]string, error) {
	if err := c.gate(api.CapBackfillIPs); err != nil {
		return nil, err
	}
	o, err := c.call(ctx, http.MethodPost, "/api/v1/node/backfillips", url.Values{"confirmed": {"true"}}, nil)
	if err != nil {
		return nil, err
	}
	ch := o.Strs("changes")
	if ch == nil {
		ch = []string{}
	}
	return ch, nil
}

// ListUsers lists users matching f.
func (c *Client) ListUsers(ctx context.Context, f api.UserFilter) ([]api.User, error) {
	q := url.Values{}
	if f.ID != 0 {
		q.Set("id", idStr(f.ID))
	}
	if f.Name != "" {
		q.Set("name", f.Name)
	}
	if f.Email != "" {
		q.Set("email", f.Email)
	}
	o, err := c.call(ctx, http.MethodGet, "/api/v1/user", q, nil)
	if err != nil {
		return nil, err
	}
	return wire.Users(o.Get("users")), nil
}

func userReply(o wire.Obj, err error) (api.User, error) {
	if err != nil {
		return api.User{}, err
	}
	u := o.Obj("user")
	if u == nil {
		return api.User{}, api.Errf(api.KindServer, "response has no user")
	}
	return wire.User(u), nil
}

// CreateUser creates a local user.
func (c *Client) CreateUser(ctx context.Context, u api.NewUser) (api.User, error) {
	body := map[string]any{"name": u.Name}
	if u.DisplayName != "" {
		body["displayName"] = u.DisplayName
	}
	if u.Email != "" {
		body["email"] = u.Email
	}
	return userReply(c.call(ctx, http.MethodPost, "/api/v1/user", nil, body))
}

// RenameUser renames a user by ID.
func (c *Client) RenameUser(ctx context.Context, id uint64, newName string) (api.User, error) {
	p := "/api/v1/user/" + idStr(id) + "/rename/" + url.PathEscape(newName)
	return userReply(c.call(ctx, http.MethodPost, p, nil, map[string]any{}))
}

// DeleteUser removes a user by ID.
func (c *Client) DeleteUser(ctx context.Context, id uint64) error {
	_, err := c.call(ctx, http.MethodDelete, "/api/v1/user/"+idStr(id), nil, nil)
	return err
}

// ListPreAuthKeys lists pre-auth keys; the user filter is applied locally
// because neither version filters GET /preauthkey by user.
func (c *Client) ListPreAuthKeys(ctx context.Context, f api.KeyFilter) ([]api.PreAuthKey, error) {
	o, err := c.call(ctx, http.MethodGet, "/api/v1/preauthkey", nil, nil)
	if err != nil {
		return nil, err
	}
	keys := wire.PreAuthKeys(o.Get("preauthkeys"))
	if f.User == "" {
		return keys, nil
	}
	out := keys[:0]
	for _, k := range keys {
		if k.User != nil && k.User.Name == f.User {
			out = append(out, k)
		}
	}
	return out, nil
}

func (c *Client) userID(ctx context.Context, name string) (uint64, error) {
	users, err := c.ListUsers(ctx, api.UserFilter{Name: name})
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

// CreatePreAuthKey mints a key for k.User (or a tags-only key on 0.30+).
func (c *Client) CreatePreAuthKey(ctx context.Context, k api.NewPreAuthKey) (api.PreAuthKey, error) {
	body := map[string]any{"reusable": k.Reusable, "ephemeral": k.Ephemeral}
	if k.User == "" {
		if err := c.gate(api.CapTagOnlyKeys); err != nil {
			return api.PreAuthKey{}, err
		}
	} else {
		id, err := c.userID(ctx, k.User)
		if err != nil {
			return api.PreAuthKey{}, err
		}
		body["user"] = idStr(id)
	}
	if !k.Expiration.IsZero() {
		body["expiration"] = k.Expiration.UTC().Format(time.RFC3339Nano)
	}
	if len(k.ACLTags) > 0 {
		body["aclTags"] = k.ACLTags
	}
	o, err := c.call(ctx, http.MethodPost, "/api/v1/preauthkey", nil, body)
	if err != nil {
		return api.PreAuthKey{}, err
	}
	pk := o.Obj("preauthkey")
	if pk == nil {
		return api.PreAuthKey{}, api.Errf(api.KindServer, "response has no preAuthKey")
	}
	return wire.PreAuthKey(pk), nil
}

// ExpirePreAuthKey expires a key by ID.
func (c *Client) ExpirePreAuthKey(ctx context.Context, id uint64) error {
	_, err := c.call(ctx, http.MethodPost, "/api/v1/preauthkey/expire", nil, map[string]any{"id": idStr(id)})
	return err
}

// DeletePreAuthKey removes a key by ID.
func (c *Client) DeletePreAuthKey(ctx context.Context, id uint64) error {
	if err := c.gate(api.CapPreAuthKeyDelete); err != nil {
		return err
	}
	_, err := c.call(ctx, http.MethodDelete, "/api/v1/preauthkey", url.Values{"id": {idStr(id)}}, nil)
	return err
}

// ListAPIKeys lists API keys.
func (c *Client) ListAPIKeys(ctx context.Context) ([]api.APIKey, error) {
	o, err := c.call(ctx, http.MethodGet, "/api/v1/apikey", nil, nil)
	if err != nil {
		return nil, err
	}
	return wire.APIKeys(o.Get("apikeys")), nil
}

// CreateAPIKey mints an API key; the secret is only returned once.
func (c *Client) CreateAPIKey(ctx context.Context, expiration time.Time) (api.Secret, error) {
	body := map[string]any{}
	if !expiration.IsZero() {
		body["expiration"] = expiration.UTC().Format(time.RFC3339Nano)
	}
	o, err := c.call(ctx, http.MethodPost, "/api/v1/apikey", nil, body)
	if err != nil {
		return api.Secret{}, err
	}
	k := o.Str("apikey")
	if k == "" {
		return api.Secret{}, api.Errf(api.KindServer, "response has no apiKey")
	}
	return api.NewSecret(k), nil
}

// ExpireAPIKey expires an API key by prefix.
func (c *Client) ExpireAPIKey(ctx context.Context, prefix string) error {
	_, err := c.call(ctx, http.MethodPost, "/api/v1/apikey/expire", nil, map[string]any{"prefix": prefix})
	return err
}

// DeleteAPIKey removes an API key by prefix.
func (c *Client) DeleteAPIKey(ctx context.Context, prefix string) error {
	if err := c.gate(api.CapAPIKeyDelete); err != nil {
		return err
	}
	_, err := c.call(ctx, http.MethodDelete, "/api/v1/apikey/"+url.PathEscape(prefix), nil, nil)
	return err
}

func policyReply(o wire.Obj, err error) (api.Policy, error) {
	if err != nil {
		return api.Policy{}, err
	}
	return api.Policy{Raw: o.Str("policy"), UpdatedAt: o.Time("updatedat")}, nil
}

// GetPolicy fetches the stored policy.
func (c *Client) GetPolicy(ctx context.Context) (api.Policy, error) {
	return policyReply(c.call(ctx, http.MethodGet, "/api/v1/policy", nil, nil))
}

// CheckPolicy validates raw against the live server without storing it.
func (c *Client) CheckPolicy(ctx context.Context, raw string) error {
	if err := c.gate(api.CapPolicyCheck); err != nil {
		return err
	}
	_, err := c.call(ctx, http.MethodPost, "/api/v1/policy/check", nil, map[string]any{"policy": raw})
	return asPolicyError(err)
}

// SetPolicy stores raw. A refusal because the server is in file mode drops
// CapPolicyWritable and returns KindUnsupported.
func (c *Client) SetPolicy(ctx context.Context, raw string) (api.Policy, error) {
	if err := c.gate(api.CapPolicyWritable); err != nil {
		return api.Policy{}, err
	}
	p, err := policyReply(c.call(ctx, http.MethodPut, "/api/v1/policy", nil, map[string]any{"policy": raw}))
	if err != nil {
		e := api.AsError(err)
		if isFileModeRefusal(e.Message) {
			c.dropCap(api.CapPolicyWritable)
			u := api.Errf(api.KindUnsupported, "%s", e.Message)
			u.Hint = "The server reads its policy from a file (policy.mode: file). Edit the file on the host."
			u.Status, u.Err = e.Status, e
			return api.Policy{}, u
		}
		return api.Policy{}, asPolicyError(err)
	}
	if p.Raw == "" {
		p.Raw = raw
	}
	return p, nil
}

func isFileModeRefusal(msg string) bool {
	m := strings.ToLower(msg)
	return strings.Contains(m, "mode") && strings.Contains(m, "database") && !strings.Contains(m, "line")
}

// asPolicyError makes server-side policy rejections KindInvalid with a
// position when the message carries one.
func asPolicyError(err error) error {
	if err == nil {
		return nil
	}
	e := api.AsError(err)
	if e.Kind == api.KindServer && e.Status >= 400 && e.Status < 500 {
		e.Kind = api.KindInvalid
	}
	if e.Kind == api.KindInvalid && e.Line == 0 {
		e.Line, e.Col = wire.PolicyPos(e.Message)
	}
	return e
}

// AuthRegister registers a pending web-auth session to user.
func (c *Client) AuthRegister(ctx context.Context, authID, user string) error {
	if err := c.gate(api.CapAuthApproval); err != nil {
		return err
	}
	_, err := c.call(ctx, http.MethodPost, "/api/v1/auth/register", nil, map[string]any{"authId": authID, "user": user})
	return err
}

// AuthApprove approves a pending web-auth session.
func (c *Client) AuthApprove(ctx context.Context, authID string) error {
	if err := c.gate(api.CapAuthApproval); err != nil {
		return err
	}
	_, err := c.call(ctx, http.MethodPost, "/api/v1/auth/approve", nil, map[string]any{"authId": authID})
	return err
}

// AuthReject rejects a pending web-auth session.
func (c *Client) AuthReject(ctx context.Context, authID string) error {
	if err := c.gate(api.CapAuthApproval); err != nil {
		return err
	}
	_, err := c.call(ctx, http.MethodPost, "/api/v1/auth/reject", nil, map[string]any{"authId": authID})
	return err
}

// String describes the client without revealing the key.
func (c *Client) String() string {
	if c.socket != "" {
		return fmt.Sprintf("httpapi(unix:%s)", c.socket)
	}
	return fmt.Sprintf("httpapi(%s)", c.base.Redacted())
}
