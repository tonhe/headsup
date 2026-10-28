// Package fake is an in-memory api.Backend for tests and `headsup --demo`.
package fake

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/netip"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tailscale/hujson"

	"github.com/tonhe/headsup/internal/api"
	"github.com/tonhe/headsup/internal/api/wire"
)

// Call is one recorded Backend invocation.
type Call struct {
	Method string
	Args   []any
}

// Option configures a Backend.
type Option func(*Backend)

// WithLatency delays every call by d (honouring ctx).
func WithLatency(d time.Duration) Option { return func(b *Backend) { b.latency = d } }

// FailNext makes the next call to method fail with kind.
func FailNext(method string, kind api.ErrorKind) Option {
	return func(b *Backend) { b.failNext[method] = append(b.failNext[method], kind) }
}

// WithVersion makes the fake report version v and its capabilities.
func WithVersion(v string) Option {
	return func(b *Backend) { b.version = v; b.caps = wire.CapsFor(v) }
}

// WithCaps overrides the advertised capabilities.
func WithCaps(c api.Capabilities) Option { return func(b *Backend) { b.caps = c } }

// WithClock sets the time source.
func WithClock(now func() time.Time) Option { return func(b *Backend) { b.now = now } }

// Backend is a thread-safe in-memory headscale.
type Backend struct {
	mu       sync.Mutex
	now      func() time.Time
	latency  time.Duration
	failNext map[string][]api.ErrorKind
	calls    []Call
	version  string
	caps     api.Capabilities

	users   map[uint64]*api.User
	nodes   map[uint64]*api.Node
	keys    map[uint64]*api.PreAuthKey
	apiKeys map[uint64]*api.APIKey
	policy  api.Policy
	pending map[string]string

	nextUser, nextNode, nextKey, nextAPIKey uint64
}

var _ api.Backend = (*Backend)(nil)

// New builds a Backend from seed.
func New(seed Seed, opts ...Option) *Backend {
	b := &Backend{
		now:      time.Now,
		failNext: map[string][]api.ErrorKind{},
		users:    map[uint64]*api.User{},
		nodes:    map[uint64]*api.Node{},
		keys:     map[uint64]*api.PreAuthKey{},
		apiKeys:  map[uint64]*api.APIKey{},
		pending:  map[string]string{},
	}
	v := seed.Version
	if v == "" {
		v = "v0.30.0"
	}
	WithVersion(v)(b)
	for _, o := range opts {
		o(b)
	}
	b.materialise(seed, b.now())
	return b
}

// Demo builds a Backend from the embedded standard seed.
func Demo(opts ...Option) *Backend { return New(DemoSeed(), opts...) }

// FailNext makes the next call to method fail with kind.
func (b *Backend) FailNext(method string, kind api.ErrorKind) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failNext[method] = append(b.failNext[method], kind)
}

// SetLatency changes the per-call delay.
func (b *Backend) SetLatency(d time.Duration) {
	b.mu.Lock()
	b.latency = d
	b.mu.Unlock()
}

// Calls returns a copy of the recorded call log.
func (b *Backend) Calls() []Call {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.calls)
}

// Version reports the simulated server version.
func (b *Backend) Version() string { return b.version }

// enter records the call, applies latency and injected failures, and locks.
// On success the caller must defer b.mu.Unlock().
func (b *Backend) enter(ctx context.Context, method string, args ...any) error {
	b.mu.Lock()
	b.calls = append(b.calls, Call{Method: method, Args: args})
	lat := b.latency
	var inject *api.ErrorKind
	if q := b.failNext[method]; len(q) > 0 {
		k := q[0]
		inject = &k
		b.failNext[method] = q[1:]
	}
	b.mu.Unlock()

	if lat > 0 {
		t := time.NewTimer(lat)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctxErr(ctx)
		case <-t.C:
		}
	}
	if err := ctx.Err(); err != nil {
		return ctxErr(ctx)
	}
	if inject != nil {
		return api.Errf(*inject, "injected failure for %s", method)
	}
	b.mu.Lock()
	return nil
}

func ctxErr(ctx context.Context) error {
	e := api.Errf(api.KindUnreachable, "%v", ctx.Err())
	e.Err = ctx.Err()
	return e
}

func (b *Backend) gate(c api.Cap) error {
	if !b.caps.Has(c) {
		return api.Errf(api.KindUnsupported, "%s is not supported by headscale %s", c, b.version)
	}
	return nil
}

// Capabilities reports the simulated server's capabilities.
func (b *Backend) Capabilities() api.Capabilities {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.caps
}

// Connect reports the simulated server info.
func (b *Backend) Connect(ctx context.Context) (api.ServerInfo, error) {
	if err := b.enter(ctx, "Connect"); err != nil {
		return api.ServerInfo{}, err
	}
	defer b.mu.Unlock()
	return api.ServerInfo{Version: b.version, Healthy: true, Caps: b.caps}, nil
}

func cloneNode(n *api.Node) api.Node {
	c := *n
	c.IPAddresses = slices.Clone(n.IPAddresses)
	c.Tags = slices.Clone(n.Tags)
	c.AvailableRoutes = slices.Clone(n.AvailableRoutes)
	c.ApprovedRoutes = slices.Clone(n.ApprovedRoutes)
	c.SubnetRoutes = slices.Clone(n.SubnetRoutes)
	if n.Expiry != nil {
		t := *n.Expiry
		c.Expiry = &t
	}
	if n.PreAuthKey != nil {
		r := *n.PreAuthKey
		c.PreAuthKey = &r
	}
	return c
}

func served(n api.Node) []netip.Prefix {
	var out []netip.Prefix
	for _, r := range n.ApprovedRoutes {
		if slices.Contains(n.AvailableRoutes, r) {
			out = append(out, r)
		}
	}
	return out
}

func (b *Backend) userByName(name string) (*api.User, bool) {
	for _, u := range b.users {
		if u.Name == name {
			return u, true
		}
	}
	return nil, false
}

func (b *Backend) node(id uint64) (*api.Node, error) {
	n, ok := b.nodes[id]
	if !ok {
		return nil, api.Errf(api.KindNotFound, "node %d not found", id)
	}
	return n, nil
}

// ListNodes returns nodes ordered by ID.
func (b *Backend) ListNodes(ctx context.Context, f api.NodeFilter) ([]api.Node, error) {
	if err := b.enter(ctx, "ListNodes", f); err != nil {
		return nil, err
	}
	defer b.mu.Unlock()
	out := make([]api.Node, 0, len(b.nodes))
	for _, n := range b.nodes {
		if f.User != "" && n.User.Name != f.User {
			continue
		}
		out = append(out, cloneNode(n))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// GetNode returns one node.
func (b *Backend) GetNode(ctx context.Context, id uint64) (api.Node, error) {
	if err := b.enter(ctx, "GetNode", id); err != nil {
		return api.Node{}, err
	}
	defer b.mu.Unlock()
	n, err := b.node(id)
	if err != nil {
		return api.Node{}, err
	}
	return cloneNode(n), nil
}

// RegisterNode registers a pending machine under user.
func (b *Backend) RegisterNode(ctx context.Context, user, key string) (api.Node, error) {
	if err := b.enter(ctx, "RegisterNode", user, key); err != nil {
		return api.Node{}, err
	}
	defer b.mu.Unlock()
	return b.register(user, "node-"+strings.ToLower(trunc(key, 8)), api.RegisterCLI)
}

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func (b *Backend) register(user, host string, m api.RegisterMethod) (api.Node, error) {
	u, ok := b.userByName(user)
	if !ok {
		return api.Node{}, api.Errf(api.KindNotFound, "user %q not found", user)
	}
	b.nextNode++
	id := b.nextNode
	now := b.now()
	exp := now.Add(180 * 24 * time.Hour)
	n := &api.Node{
		ID: id, Name: host, GivenName: sanitize(host), User: u.Ref(), Online: true,
		LastSeen: now, CreatedAt: now, Expiry: &exp, RegisterMethod: m,
		IPAddresses: []netip.Addr{
			netip.AddrFrom4([4]byte{100, 64, byte(id / 250), byte(id%250 + 1)}),
			v6For(id),
		},
		MachineKey: "mkey:" + randHex(32), NodeKey: "nodekey:" + randHex(32), DiscoKey: "discokey:" + randHex(32),
	}
	b.nodes[id] = n
	return cloneNode(n), nil
}

var dnsLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

func sanitize(s string) string {
	s = strings.ToLower(s)
	var sb strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			sb.WriteRune(r)
		} else {
			sb.WriteByte('-')
		}
	}
	return strings.Trim(sb.String(), "-")
}

// RenameNode sets the node's given name.
func (b *Backend) RenameNode(ctx context.Context, id uint64, newName string) (api.Node, error) {
	if err := b.enter(ctx, "RenameNode", id, newName); err != nil {
		return api.Node{}, err
	}
	defer b.mu.Unlock()
	n, err := b.node(id)
	if err != nil {
		return api.Node{}, err
	}
	if !dnsLabel.MatchString(newName) {
		return api.Node{}, api.Errf(api.KindInvalid, "given name %q is not a valid DNS label", newName)
	}
	for _, o := range b.nodes {
		if o.ID != id && o.GivenName == newName {
			return api.Node{}, api.Errf(api.KindConflict, "given name %q is already in use", newName)
		}
	}
	n.GivenName = newName
	return cloneNode(n), nil
}

var tagRe = regexp.MustCompile(`^tag:[a-z0-9-]+$`)

// SetTags replaces the node's tags.
func (b *Backend) SetTags(ctx context.Context, id uint64, tags []string) (api.Node, error) {
	if err := b.enter(ctx, "SetTags", id, slices.Clone(tags)); err != nil {
		return api.Node{}, err
	}
	defer b.mu.Unlock()
	n, err := b.node(id)
	if err != nil {
		return api.Node{}, err
	}
	for _, t := range tags {
		if !tagRe.MatchString(t) {
			return api.Node{}, api.Errf(api.KindInvalid, "invalid tag %q", t)
		}
	}
	n.Tags = slices.Compact(slices.Sorted(slices.Values(tags)))
	if len(n.Tags) == 0 {
		n.Tags = nil
	}
	return cloneNode(n), nil
}

// SetApprovedRoutes replaces the node's approved routes.
func (b *Backend) SetApprovedRoutes(ctx context.Context, id uint64, routes []netip.Prefix) (api.Node, error) {
	if err := b.enter(ctx, "SetApprovedRoutes", id, slices.Clone(routes)); err != nil {
		return api.Node{}, err
	}
	defer b.mu.Unlock()
	n, err := b.node(id)
	if err != nil {
		return api.Node{}, err
	}
	var rs []netip.Prefix
	for _, r := range routes {
		rs = append(rs, r.Masked())
	}
	n.ApprovedRoutes = rs
	n.SubnetRoutes = served(*n)
	return cloneNode(n), nil
}

// ExpireNode expires the node now, at opt.At, or disables expiry.
func (b *Backend) ExpireNode(ctx context.Context, id uint64, opt api.ExpireOpts) (api.Node, error) {
	if err := b.enter(ctx, "ExpireNode", id, opt); err != nil {
		return api.Node{}, err
	}
	defer b.mu.Unlock()
	if opt.DisableExpiry {
		if err := b.gate(api.CapDisableExpiry); err != nil {
			return api.Node{}, err
		}
	}
	n, err := b.node(id)
	if err != nil {
		return api.Node{}, err
	}
	switch {
	case opt.DisableExpiry:
		n.Expiry = nil
	case opt.At.IsZero():
		t := b.now()
		n.Expiry = &t
	default:
		t := opt.At
		n.Expiry = &t
	}
	return cloneNode(n), nil
}

// DeleteNode removes a node.
func (b *Backend) DeleteNode(ctx context.Context, id uint64) error {
	if err := b.enter(ctx, "DeleteNode", id); err != nil {
		return err
	}
	defer b.mu.Unlock()
	if _, err := b.node(id); err != nil {
		return err
	}
	delete(b.nodes, id)
	return nil
}

// BackfillIPs reports address changes (none in the fake).
func (b *Backend) BackfillIPs(ctx context.Context) ([]string, error) {
	if err := b.enter(ctx, "BackfillIPs"); err != nil {
		return nil, err
	}
	defer b.mu.Unlock()
	if err := b.gate(api.CapBackfillIPs); err != nil {
		return nil, err
	}
	return []string{}, nil
}

// ListUsers returns users ordered by ID.
func (b *Backend) ListUsers(ctx context.Context, f api.UserFilter) ([]api.User, error) {
	if err := b.enter(ctx, "ListUsers", f); err != nil {
		return nil, err
	}
	defer b.mu.Unlock()
	out := []api.User{}
	for _, u := range b.users {
		if (f.ID != 0 && u.ID != f.ID) || (f.Name != "" && u.Name != f.Name) || (f.Email != "" && u.Email != f.Email) {
			continue
		}
		out = append(out, *u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// CreateUser adds a local user.
func (b *Backend) CreateUser(ctx context.Context, nu api.NewUser) (api.User, error) {
	if err := b.enter(ctx, "CreateUser", nu); err != nil {
		return api.User{}, err
	}
	defer b.mu.Unlock()
	if strings.TrimSpace(nu.Name) == "" {
		return api.User{}, api.Errf(api.KindInvalid, "user name is required")
	}
	if _, ok := b.userByName(nu.Name); ok {
		return api.User{}, api.Errf(api.KindConflict, "user %q already exists", nu.Name)
	}
	b.nextUser++
	u := &api.User{ID: b.nextUser, Name: nu.Name, DisplayName: nu.DisplayName, Email: nu.Email, CreatedAt: b.now()}
	b.users[u.ID] = u
	return *u, nil
}

// RenameUser renames a user by ID.
func (b *Backend) RenameUser(ctx context.Context, id uint64, newName string) (api.User, error) {
	if err := b.enter(ctx, "RenameUser", id, newName); err != nil {
		return api.User{}, err
	}
	defer b.mu.Unlock()
	u, ok := b.users[id]
	if !ok {
		return api.User{}, api.Errf(api.KindNotFound, "user %d not found", id)
	}
	if o, ok := b.userByName(newName); ok && o.ID != id {
		return api.User{}, api.Errf(api.KindConflict, "user %q already exists", newName)
	}
	u.Name = newName
	ref := u.Ref()
	for _, n := range b.nodes {
		if n.User.ID == id {
			n.User = ref
		}
	}
	for _, k := range b.keys {
		if k.User != nil && k.User.ID == id {
			r := ref
			k.User = &r
		}
	}
	return *u, nil
}

// DeleteUser removes a user that owns no nodes.
func (b *Backend) DeleteUser(ctx context.Context, id uint64) error {
	if err := b.enter(ctx, "DeleteUser", id); err != nil {
		return err
	}
	defer b.mu.Unlock()
	if _, ok := b.users[id]; !ok {
		return api.Errf(api.KindNotFound, "user %d not found", id)
	}
	for _, n := range b.nodes {
		if n.User.ID == id {
			return api.Errf(api.KindInvalid, "user still has nodes")
		}
	}
	delete(b.users, id)
	return nil
}

func cloneKey(k *api.PreAuthKey) api.PreAuthKey {
	c := *k
	c.ACLTags = slices.Clone(k.ACLTags)
	if k.User != nil {
		r := *k.User
		c.User = &r
	}
	return c
}

// ListPreAuthKeys returns keys ordered by ID.
func (b *Backend) ListPreAuthKeys(ctx context.Context, f api.KeyFilter) ([]api.PreAuthKey, error) {
	if err := b.enter(ctx, "ListPreAuthKeys", f); err != nil {
		return nil, err
	}
	defer b.mu.Unlock()
	out := []api.PreAuthKey{}
	for _, k := range b.keys {
		if f.User != "" && (k.User == nil || k.User.Name != f.User) {
			continue
		}
		out = append(out, cloneKey(k))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// CreatePreAuthKey mints a key.
func (b *Backend) CreatePreAuthKey(ctx context.Context, nk api.NewPreAuthKey) (api.PreAuthKey, error) {
	if err := b.enter(ctx, "CreatePreAuthKey", nk); err != nil {
		return api.PreAuthKey{}, err
	}
	defer b.mu.Unlock()
	if nk.User == "" {
		if err := b.gate(api.CapTagOnlyKeys); err != nil {
			return api.PreAuthKey{}, err
		}
		if len(nk.ACLTags) == 0 {
			return api.PreAuthKey{}, api.Errf(api.KindInvalid, "a key needs a user or at least one tag")
		}
	}
	for _, t := range nk.ACLTags {
		if !tagRe.MatchString(t) {
			return api.PreAuthKey{}, api.Errf(api.KindInvalid, "invalid tag %q", t)
		}
	}
	b.nextKey++
	now := b.now()
	k := &api.PreAuthKey{
		ID: b.nextKey, Key: api.NewSecret("hskey-auth-" + randHex(6) + "-" + randHex(16)),
		Reusable: nk.Reusable, Ephemeral: nk.Ephemeral, Expiration: nk.Expiration,
		CreatedAt: now, ACLTags: slices.Clone(nk.ACLTags),
	}
	if k.Expiration.IsZero() {
		k.Expiration = now.Add(time.Hour)
	}
	if nk.User != "" {
		u, ok := b.userByName(nk.User)
		if !ok {
			return api.PreAuthKey{}, api.Errf(api.KindNotFound, "user %q not found", nk.User)
		}
		r := u.Ref()
		k.User = &r
	}
	b.keys[k.ID] = k
	return cloneKey(k), nil
}

// ExpirePreAuthKey expires a key now.
func (b *Backend) ExpirePreAuthKey(ctx context.Context, id uint64) error {
	if err := b.enter(ctx, "ExpirePreAuthKey", id); err != nil {
		return err
	}
	defer b.mu.Unlock()
	k, ok := b.keys[id]
	if !ok {
		return api.Errf(api.KindNotFound, "pre-auth key %d not found", id)
	}
	k.Expiration = b.now()
	return nil
}

// DeletePreAuthKey removes a key.
func (b *Backend) DeletePreAuthKey(ctx context.Context, id uint64) error {
	if err := b.enter(ctx, "DeletePreAuthKey", id); err != nil {
		return err
	}
	defer b.mu.Unlock()
	if err := b.gate(api.CapPreAuthKeyDelete); err != nil {
		return err
	}
	if _, ok := b.keys[id]; !ok {
		return api.Errf(api.KindNotFound, "pre-auth key %d not found", id)
	}
	delete(b.keys, id)
	return nil
}

// ListAPIKeys returns API keys ordered by ID.
func (b *Backend) ListAPIKeys(ctx context.Context) ([]api.APIKey, error) {
	if err := b.enter(ctx, "ListAPIKeys"); err != nil {
		return nil, err
	}
	defer b.mu.Unlock()
	out := []api.APIKey{}
	for _, k := range b.apiKeys {
		out = append(out, *k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// CreateAPIKey mints an API key and returns the full secret once.
func (b *Backend) CreateAPIKey(ctx context.Context, expiration time.Time) (api.Secret, error) {
	if err := b.enter(ctx, "CreateAPIKey", expiration); err != nil {
		return api.Secret{}, err
	}
	defer b.mu.Unlock()
	now := b.now()
	if expiration.IsZero() {
		expiration = now.Add(90 * 24 * time.Hour)
	}
	b.nextAPIKey++
	prefix := randHex(6)
	b.apiKeys[b.nextAPIKey] = &api.APIKey{ID: b.nextAPIKey, Prefix: prefix, Expiration: expiration, CreatedAt: now}
	return api.NewSecret("hskey-api-" + prefix + "-" + randHex(24)), nil
}

func (b *Backend) apiKey(prefix string) (*api.APIKey, uint64, error) {
	for id, k := range b.apiKeys {
		if k.Prefix == prefix {
			return k, id, nil
		}
	}
	return nil, 0, api.Errf(api.KindNotFound, "API key %q not found", prefix)
}

// ExpireAPIKey expires an API key by prefix.
func (b *Backend) ExpireAPIKey(ctx context.Context, prefix string) error {
	if err := b.enter(ctx, "ExpireAPIKey", prefix); err != nil {
		return err
	}
	defer b.mu.Unlock()
	k, _, err := b.apiKey(prefix)
	if err != nil {
		return err
	}
	k.Expiration = b.now()
	return nil
}

// DeleteAPIKey removes an API key by prefix.
func (b *Backend) DeleteAPIKey(ctx context.Context, prefix string) error {
	if err := b.enter(ctx, "DeleteAPIKey", prefix); err != nil {
		return err
	}
	defer b.mu.Unlock()
	if err := b.gate(api.CapAPIKeyDelete); err != nil {
		return err
	}
	_, id, err := b.apiKey(prefix)
	if err != nil {
		return err
	}
	delete(b.apiKeys, id)
	return nil
}

// GetPolicy returns the stored policy.
func (b *Backend) GetPolicy(ctx context.Context) (api.Policy, error) {
	if err := b.enter(ctx, "GetPolicy"); err != nil {
		return api.Policy{}, err
	}
	defer b.mu.Unlock()
	return b.policy, nil
}

// ValidatePolicy parses raw as HuJSON and checks it is a JSON object. Errors
// are KindInvalid with Line and Col set when known.
func ValidatePolicy(raw string) error {
	v, err := hujson.Parse([]byte(raw))
	if err != nil {
		e := api.Errf(api.KindInvalid, "parsing policy: %v", err)
		e.Line, e.Col = wire.PolicyPos(err.Error())
		e.Err = err
		return e
	}
	v.Standardize()
	var m map[string]any
	if err := json.Unmarshal(v.Pack(), &m); err != nil {
		e := api.Errf(api.KindInvalid, "policy must be a JSON object: %v", err)
		e.Line, e.Col = 1, 1
		return e
	}
	return nil
}

// CheckPolicy validates raw without storing it.
func (b *Backend) CheckPolicy(ctx context.Context, raw string) error {
	if err := b.enter(ctx, "CheckPolicy", len(raw)); err != nil {
		return err
	}
	defer b.mu.Unlock()
	if err := b.gate(api.CapPolicyCheck); err != nil {
		return err
	}
	return ValidatePolicy(raw)
}

// SetPolicy validates and stores raw.
func (b *Backend) SetPolicy(ctx context.Context, raw string) (api.Policy, error) {
	if err := b.enter(ctx, "SetPolicy", len(raw)); err != nil {
		return api.Policy{}, err
	}
	defer b.mu.Unlock()
	if err := b.gate(api.CapPolicyWritable); err != nil {
		return api.Policy{}, err
	}
	if err := ValidatePolicy(raw); err != nil {
		return api.Policy{}, err
	}
	b.policy = api.Policy{Raw: raw, UpdatedAt: b.now()}
	return b.policy, nil
}

// AuthRegister registers a pending web-auth session to user.
func (b *Backend) AuthRegister(ctx context.Context, authID, user string) error {
	if err := b.enter(ctx, "AuthRegister", authID, user); err != nil {
		return err
	}
	defer b.mu.Unlock()
	if err := b.gate(api.CapAuthApproval); err != nil {
		return err
	}
	host, ok := b.pending[authID]
	if !ok {
		return api.Errf(api.KindNotFound, "no pending auth session for auth_id %s", authID)
	}
	if _, err := b.register(user, host, api.RegisterCLI); err != nil {
		return err
	}
	delete(b.pending, authID)
	return nil
}

// AuthApprove approves a pending web-auth session.
func (b *Backend) AuthApprove(ctx context.Context, authID string) error {
	return b.resolvePending(ctx, "AuthApprove", authID)
}

// AuthReject rejects a pending web-auth session.
func (b *Backend) AuthReject(ctx context.Context, authID string) error {
	return b.resolvePending(ctx, "AuthReject", authID)
}

func (b *Backend) resolvePending(ctx context.Context, method, authID string) error {
	if err := b.enter(ctx, method, authID); err != nil {
		return err
	}
	defer b.mu.Unlock()
	if err := b.gate(api.CapAuthApproval); err != nil {
		return err
	}
	if _, ok := b.pending[authID]; !ok {
		return api.Errf(api.KindNotFound, "no pending auth session for auth_id %s", authID)
	}
	delete(b.pending, authID)
	return nil
}

func v6For(id uint64) netip.Addr {
	a := netip.MustParseAddr("fd7a:115c:a1e0::").As16()
	a[14], a[15] = byte(id>>8), byte(id)
	return netip.AddrFrom16(a)
}

func randHex(n int) string {
	buf := make([]byte, n)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}
