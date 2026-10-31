package cli

import (
	"context"
	"net/netip"
	"sync"
	"time"

	"github.com/tonhe/headsup/internal/api"
)

// recorder wraps the backend so a verb can print what a module Action
// created or changed. Actions only report a toast message; the objects the
// server returned are captured here on the way through.
type recorder struct {
	api.Backend

	mu      sync.Mutex
	node    *api.Node
	user    *api.User
	preKey  *api.PreAuthKey
	apiKey  *api.Secret
	changes []string
}

// Simulated forwards the demo marker so modules pick demo paths and probes.
func (r *recorder) Simulated() bool {
	s, ok := r.Backend.(interface{ Simulated() bool })
	return ok && s.Simulated()
}

// SelfAPIKey forwards the connection's own key so the keys module can warn
// before it is expired.
func (r *recorder) SelfAPIKey() api.Secret {
	if s, ok := r.Backend.(interface{ SelfAPIKey() api.Secret }); ok {
		return s.SelfAPIKey()
	}
	return api.Secret{}
}

func (r *recorder) keepNode(n api.Node, err error) (api.Node, error) {
	if err == nil {
		r.mu.Lock()
		r.node = &n
		r.mu.Unlock()
	}
	return n, err
}

func (r *recorder) keepUser(u api.User, err error) (api.User, error) {
	if err == nil {
		r.mu.Lock()
		r.user = &u
		r.mu.Unlock()
	}
	return u, err
}

func (r *recorder) RegisterNode(ctx context.Context, user, key string) (api.Node, error) {
	return r.keepNode(r.Backend.RegisterNode(ctx, user, key))
}

func (r *recorder) RenameNode(ctx context.Context, id uint64, name string) (api.Node, error) {
	return r.keepNode(r.Backend.RenameNode(ctx, id, name))
}

func (r *recorder) SetTags(ctx context.Context, id uint64, tags []string) (api.Node, error) {
	return r.keepNode(r.Backend.SetTags(ctx, id, tags))
}

func (r *recorder) SetApprovedRoutes(ctx context.Context, id uint64, routes []netip.Prefix) (api.Node, error) {
	return r.keepNode(r.Backend.SetApprovedRoutes(ctx, id, routes))
}

func (r *recorder) ExpireNode(ctx context.Context, id uint64, o api.ExpireOpts) (api.Node, error) {
	return r.keepNode(r.Backend.ExpireNode(ctx, id, o))
}

func (r *recorder) CreateUser(ctx context.Context, u api.NewUser) (api.User, error) {
	return r.keepUser(r.Backend.CreateUser(ctx, u))
}

func (r *recorder) RenameUser(ctx context.Context, id uint64, name string) (api.User, error) {
	return r.keepUser(r.Backend.RenameUser(ctx, id, name))
}

func (r *recorder) CreatePreAuthKey(ctx context.Context, k api.NewPreAuthKey) (api.PreAuthKey, error) {
	out, err := r.Backend.CreatePreAuthKey(ctx, k)
	if err == nil {
		r.mu.Lock()
		r.preKey = &out
		r.mu.Unlock()
	}
	return out, err
}

func (r *recorder) CreateAPIKey(ctx context.Context, exp time.Time) (api.Secret, error) {
	s, err := r.Backend.CreateAPIKey(ctx, exp)
	if err == nil {
		r.mu.Lock()
		r.apiKey = &s
		r.mu.Unlock()
	}
	return s, err
}

func (r *recorder) BackfillIPs(ctx context.Context) ([]string, error) {
	ch, err := r.Backend.BackfillIPs(ctx)
	if err == nil {
		r.mu.Lock()
		r.changes = ch
		r.mu.Unlock()
	}
	return ch, err
}

// lastNode returns the node the last node call returned.
func (r *recorder) lastNode() (api.Node, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.node == nil {
		return api.Node{}, false
	}
	return *r.node, true
}

func (r *recorder) lastUser() (api.User, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.user == nil {
		return api.User{}, false
	}
	return *r.user, true
}
