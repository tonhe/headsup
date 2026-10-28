package api

import (
	"context"
	"net/netip"
	"time"
)

// Backend is the full headscale admin surface. Every method honours ctx,
// returns *Error on failure, and returns KindUnsupported without network I/O
// when the connected server lacks the capability.
type Backend interface {
	Connect(ctx context.Context) (ServerInfo, error)
	Capabilities() Capabilities

	ListNodes(ctx context.Context, f NodeFilter) ([]Node, error)
	GetNode(ctx context.Context, id uint64) (Node, error)
	RegisterNode(ctx context.Context, user, key string) (Node, error)
	RenameNode(ctx context.Context, id uint64, newName string) (Node, error)
	SetTags(ctx context.Context, id uint64, tags []string) (Node, error)
	SetApprovedRoutes(ctx context.Context, id uint64, routes []netip.Prefix) (Node, error)
	ExpireNode(ctx context.Context, id uint64, opt ExpireOpts) (Node, error)
	DeleteNode(ctx context.Context, id uint64) error
	BackfillIPs(ctx context.Context) ([]string, error)

	ListUsers(ctx context.Context, f UserFilter) ([]User, error)
	CreateUser(ctx context.Context, u NewUser) (User, error)
	RenameUser(ctx context.Context, id uint64, newName string) (User, error)
	DeleteUser(ctx context.Context, id uint64) error

	ListPreAuthKeys(ctx context.Context, f KeyFilter) ([]PreAuthKey, error)
	CreatePreAuthKey(ctx context.Context, k NewPreAuthKey) (PreAuthKey, error)
	ExpirePreAuthKey(ctx context.Context, id uint64) error
	DeletePreAuthKey(ctx context.Context, id uint64) error

	ListAPIKeys(ctx context.Context) ([]APIKey, error)
	CreateAPIKey(ctx context.Context, expiration time.Time) (Secret, error)
	ExpireAPIKey(ctx context.Context, prefix string) error
	DeleteAPIKey(ctx context.Context, prefix string) error

	GetPolicy(ctx context.Context) (Policy, error)
	CheckPolicy(ctx context.Context, raw string) error
	SetPolicy(ctx context.Context, raw string) (Policy, error)

	AuthRegister(ctx context.Context, authID, user string) error
	AuthApprove(ctx context.Context, authID string) error
	AuthReject(ctx context.Context, authID string) error
}

// DefaultTimeout bounds a single backend call when the caller sets none.
const DefaultTimeout = 10 * time.Second

// WithTimeout derives a context bounded by DefaultTimeout if ctx has no deadline.
func WithTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, DefaultTimeout)
}
