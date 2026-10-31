package cli

import (
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/tonhe/headsup/internal/api"
)

// The json/yaml shapes. Field names are snake_case and stable across server
// versions (contracts/cli.md); timestamps are RFC 3339 UTC or null.

type userRefOut struct {
	ID   uint64 `json:"id" yaml:"id"`
	Name string `json:"name" yaml:"name"`
}

type nodeOut struct {
	ID              uint64     `json:"id" yaml:"id"`
	Name            string     `json:"name" yaml:"name"`
	GivenName       string     `json:"given_name" yaml:"given_name"`
	Hostname        string     `json:"hostname" yaml:"hostname"`
	User            userRefOut `json:"user" yaml:"user"`
	IPAddresses     []string   `json:"ip_addresses" yaml:"ip_addresses"`
	Tags            []string   `json:"tags" yaml:"tags"`
	Online          bool       `json:"online" yaml:"online"`
	Status          string     `json:"status" yaml:"status"`
	LastSeen        *string    `json:"last_seen" yaml:"last_seen"`
	Expiry          *string    `json:"expiry" yaml:"expiry"`
	CreatedAt       *string    `json:"created_at" yaml:"created_at"`
	RegisterMethod  string     `json:"register_method" yaml:"register_method"`
	ExitNode        bool       `json:"exit_node" yaml:"exit_node"`
	AvailableRoutes []string   `json:"available_routes" yaml:"available_routes"`
	ApprovedRoutes  []string   `json:"approved_routes" yaml:"approved_routes"`
	PendingRoutes   []string   `json:"pending_routes" yaml:"pending_routes"`
	MachineKey      string     `json:"machine_key" yaml:"machine_key"`
	NodeKey         string     `json:"node_key" yaml:"node_key"`
}

func prefixStrings(ps []netip.Prefix) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.String()
	}
	return out
}

func nodeDTO(n api.Node, now time.Time) nodeOut {
	ips := make([]string, len(n.IPAddresses))
	for i, a := range n.IPAddresses {
		ips[i] = a.String()
	}
	var exp *string
	if !n.ExpiryDisabled() {
		exp = ts(*n.Expiry)
	}
	return nodeOut{
		ID: n.ID, Name: n.DisplayName(), GivenName: n.GivenName, Hostname: n.Name,
		User:        userRefOut{ID: n.User.ID, Name: n.User.Name},
		IPAddresses: ips, Tags: nonNil(append([]string(nil), n.Tags...)),
		Online: n.Online, Status: n.Status(now).String(),
		LastSeen: ts(n.LastSeen), Expiry: exp, CreatedAt: ts(n.CreatedAt),
		RegisterMethod: string(n.RegisterMethod), ExitNode: n.IsExitNode(),
		AvailableRoutes: prefixStrings(n.AvailableRoutes), ApprovedRoutes: prefixStrings(n.ApprovedRoutes),
		PendingRoutes: prefixStrings(n.PendingRoutes()),
		MachineKey:    n.MachineKey, NodeKey: n.NodeKey,
	}
}

type userOut struct {
	ID          uint64  `json:"id" yaml:"id"`
	Name        string  `json:"name" yaml:"name"`
	DisplayName string  `json:"display_name" yaml:"display_name"`
	Email       string  `json:"email" yaml:"email"`
	Provider    string  `json:"provider" yaml:"provider"`
	CreatedAt   *string `json:"created_at" yaml:"created_at"`
	Nodes       int     `json:"nodes" yaml:"nodes"`
}

type preKeyOut struct {
	ID         uint64      `json:"id" yaml:"id"`
	Key        string      `json:"key" yaml:"key"`
	User       *userRefOut `json:"user" yaml:"user"`
	Reusable   bool        `json:"reusable" yaml:"reusable"`
	Ephemeral  bool        `json:"ephemeral" yaml:"ephemeral"`
	Used       bool        `json:"used" yaml:"used"`
	State      string      `json:"state" yaml:"state"`
	Expiration *string     `json:"expiration" yaml:"expiration"`
	CreatedAt  *string     `json:"created_at" yaml:"created_at"`
	Tags       []string    `json:"acl_tags" yaml:"acl_tags"`
}

// preKeyDTO masks the key unless reveal is set (right after creation).
func preKeyDTO(k api.PreAuthKey, now time.Time, reveal bool) preKeyOut {
	key := k.Key.Masked()
	if reveal {
		key = k.Key.Reveal()
	}
	var u *userRefOut
	if k.User != nil {
		u = &userRefOut{ID: k.User.ID, Name: k.User.Name}
	}
	return preKeyOut{ID: k.ID, Key: key, User: u, Reusable: k.Reusable, Ephemeral: k.Ephemeral, Used: k.Used,
		State: k.State(now).String(), Expiration: ts(k.Expiration), CreatedAt: ts(k.CreatedAt),
		Tags: nonNil(append([]string(nil), k.ACLTags...))}
}

type apiKeyOut struct {
	ID         uint64  `json:"id" yaml:"id"`
	Prefix     string  `json:"prefix" yaml:"prefix"`
	Expiration *string `json:"expiration" yaml:"expiration"`
	CreatedAt  *string `json:"created_at" yaml:"created_at"`
	LastSeen   *string `json:"last_seen" yaml:"last_seen"`
	Expired    bool    `json:"expired" yaml:"expired"`
}

func apiKeyDTO(k api.APIKey, now time.Time) apiKeyOut {
	return apiKeyOut{ID: k.ID, Prefix: k.Prefix, Expiration: ts(k.Expiration), CreatedAt: ts(k.CreatedAt),
		LastSeen: ts(k.LastSeen), Expired: !k.Expiration.IsZero() && k.Expiration.Before(now)}
}

type routeOut struct {
	NodeID   uint64 `json:"node_id" yaml:"node_id"`
	Node     string `json:"node" yaml:"node"`
	Prefix   string `json:"prefix" yaml:"prefix"`
	Approved bool   `json:"approved" yaml:"approved"`
	Exit     bool   `json:"exit" yaml:"exit"`
	Online   bool   `json:"online" yaml:"online"`
}

// sortNodes orders by display name, then ID, case-insensitively.
func sortNodes(ns []api.Node) {
	sort.SliceStable(ns, func(i, j int) bool {
		a, b := strings.ToLower(ns[i].DisplayName()), strings.ToLower(ns[j].DisplayName())
		if a != b {
			return a < b
		}
		return ns[i].ID < ns[j].ID
	})
}
