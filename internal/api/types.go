// Package api defines the transport-independent domain model and the Backend
// interface every headscale transport (REST, local CLI, fake) implements.
package api

import (
	"net/netip"
	"slices"
	"strings"
	"time"
)

// RegisterMethod is how a node joined the tailnet.
type RegisterMethod string

const (
	RegisterUnknown RegisterMethod = ""
	RegisterCLI     RegisterMethod = "cli"
	RegisterAuthKey RegisterMethod = "authkey"
	RegisterOIDC    RegisterMethod = "oidc"
)

// UserRef is the minimal user identity embedded in other objects.
type UserRef struct {
	ID   uint64
	Name string
}

// PreAuthKeyRef identifies the key a node registered with.
type PreAuthKeyRef struct {
	ID uint64
}

// Node is a machine in the tailnet.
type Node struct {
	ID              uint64
	Name            string // hostname reported by the client
	GivenName       string // admin-editable display name
	User            UserRef
	IPAddresses     []netip.Addr
	Tags            []string
	Online          bool
	LastSeen        time.Time
	Expiry          *time.Time // nil or zero time means expiry disabled
	CreatedAt       time.Time
	RegisterMethod  RegisterMethod
	PreAuthKey      *PreAuthKeyRef
	MachineKey      string
	NodeKey         string
	DiscoKey        string
	AvailableRoutes []netip.Prefix
	ApprovedRoutes  []netip.Prefix
	SubnetRoutes    []netip.Prefix
}

// NodeStatus summarises a node's liveness for display.
type NodeStatus int

const (
	StatusOffline NodeStatus = iota
	StatusIdle
	StatusOnline
	StatusExpired
)

func (s NodeStatus) String() string {
	switch s {
	case StatusOnline:
		return "online"
	case StatusIdle:
		return "idle"
	case StatusExpired:
		return "expired"
	default:
		return "offline"
	}
}

var (
	exitV4 = netip.MustParsePrefix("0.0.0.0/0")
	exitV6 = netip.MustParsePrefix("::/0")
)

// DisplayName is the admin-given name, falling back to the hostname.
func (n Node) DisplayName() string {
	if n.GivenName != "" {
		return n.GivenName
	}
	return n.Name
}

// IsExitNode reports whether the node advertises both default routes.
func (n Node) IsExitNode() bool {
	return slices.Contains(n.AvailableRoutes, exitV4) && slices.Contains(n.AvailableRoutes, exitV6)
}

// ExitApproved reports whether exit-node use has been approved.
func (n Node) ExitApproved() bool {
	return slices.Contains(n.ApprovedRoutes, exitV4) && slices.Contains(n.ApprovedRoutes, exitV6)
}

// IsExitRoute reports whether p is one of the two default routes.
func IsExitRoute(p netip.Prefix) bool { return p == exitV4 || p == exitV6 }

// PendingRoutes returns advertised routes that are not yet approved.
func (n Node) PendingRoutes() []netip.Prefix {
	var out []netip.Prefix
	for _, r := range n.AvailableRoutes {
		if !slices.Contains(n.ApprovedRoutes, r) {
			out = append(out, r)
		}
	}
	return out
}

// ExpiryDisabled reports whether the node never expires.
func (n Node) ExpiryDisabled() bool { return n.Expiry == nil || n.Expiry.IsZero() }

// Expired reports whether the node's key has expired as of now.
func (n Node) Expired(now time.Time) bool {
	return !n.ExpiryDisabled() && n.Expiry.Before(now)
}

// ExpiresSoon reports whether the node expires within the warning window.
func (n Node) ExpiresSoon(now time.Time, window time.Duration) bool {
	return !n.ExpiryDisabled() && !n.Expired(now) && n.Expiry.Sub(now) < window
}

// Status derives the display status. Idle means offline but seen in the last 24h.
func (n Node) Status(now time.Time) NodeStatus {
	switch {
	case n.Expired(now):
		return StatusExpired
	case n.Online:
		return StatusOnline
	case !n.LastSeen.IsZero() && now.Sub(n.LastSeen) < 24*time.Hour:
		return StatusIdle
	default:
		return StatusOffline
	}
}

// HasTag reports whether the node carries tag t (with or without "tag:").
func (n Node) HasTag(t string) bool {
	if !strings.HasPrefix(t, "tag:") {
		t = "tag:" + t
	}
	return slices.Contains(n.Tags, t)
}

// User is an identity that owns nodes and keys.
type User struct {
	ID            uint64
	Name          string
	DisplayName   string
	Email         string
	Provider      string // "" for local, "oidc" for an identity provider
	ProviderID    string
	ProfilePicURL string
	CreatedAt     time.Time
}

// Ref returns the user's reference form.
func (u User) Ref() UserRef { return UserRef{ID: u.ID, Name: u.Name} }

// PreAuthKey is a credential that lets a device join.
type PreAuthKey struct {
	ID         uint64
	Key        Secret
	User       *UserRef // nil for tags-only keys
	Reusable   bool
	Ephemeral  bool
	Used       bool
	Expiration time.Time
	CreatedAt  time.Time
	ACLTags    []string
}

// KeyState summarises a pre-auth key's usability.
type KeyState int

const (
	KeyActive KeyState = iota
	KeyUsed
	KeyExpired
)

func (s KeyState) String() string {
	switch s {
	case KeyUsed:
		return "used"
	case KeyExpired:
		return "expired"
	default:
		return "active"
	}
}

// State derives whether the key can still be used.
func (k PreAuthKey) State(now time.Time) KeyState {
	switch {
	case !k.Expiration.IsZero() && k.Expiration.Before(now):
		return KeyExpired
	case k.Used && !k.Reusable:
		return KeyUsed
	default:
		return KeyActive
	}
}

// APIKey is an admin credential. Prefix is the stable identifier.
type APIKey struct {
	ID         uint64
	Prefix     string
	Expiration time.Time
	CreatedAt  time.Time
	LastSeen   time.Time
}

// Policy is the access-control document as stored on the server.
type Policy struct {
	Raw       string
	UpdatedAt time.Time
}

// ServerInfo is what Connect learns about the server.
type ServerInfo struct {
	Version string
	Healthy bool
	Caps    Capabilities
}

// NodeFilter narrows ListNodes.
type NodeFilter struct {
	User string
}

// UserFilter narrows ListUsers.
type UserFilter struct {
	ID    uint64
	Name  string
	Email string
}

// KeyFilter narrows ListPreAuthKeys.
type KeyFilter struct {
	User string
}

// ExpireOpts controls ExpireNode. A zero At means "now".
type ExpireOpts struct {
	At            time.Time
	DisableExpiry bool
}

// NewUser is the CreateUser payload.
type NewUser struct {
	Name        string
	DisplayName string
	Email       string
}

// NewPreAuthKey is the CreatePreAuthKey payload.
type NewPreAuthKey struct {
	User       string
	Reusable   bool
	Ephemeral  bool
	Expiration time.Time
	ACLTags    []string
}
