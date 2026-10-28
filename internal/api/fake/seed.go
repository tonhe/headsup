package fake

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/netip"
	"time"

	"github.com/tonhe/headsup/internal/api"
)

//go:embed seed.json
var demoSeed []byte

// Seed is the on-disk demo data. All times are offsets from load time
// (Go durations such as "72h" or "-24h") so the demo never goes stale.
type Seed struct {
	Version          string        `json:"version"`
	Users            []SeedUser    `json:"users"`
	Nodes            []SeedNode    `json:"nodes"`
	PreAuthKeys      []SeedPreAuth `json:"preauth_keys"`
	APIKeys          []SeedAPIKey  `json:"api_keys"`
	Policy           string        `json:"policy"`
	PolicyUpdatedAgo string        `json:"policy_updated_ago"`
	PendingAuth      []SeedPending `json:"pending_auth"`
}

// SeedUser is a user in the seed.
type SeedUser struct {
	ID          uint64 `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
	Provider    string `json:"provider"`
	ProviderID  string `json:"provider_id"`
	CreatedAgo  string `json:"created_ago"`
}

// SeedNode is a node in the seed. An empty ExpiresIn disables expiry.
type SeedNode struct {
	ID              uint64   `json:"id"`
	Name            string   `json:"name"`
	GivenName       string   `json:"given_name"`
	User            string   `json:"user"`
	IPv4            string   `json:"ipv4"`
	IPv6            string   `json:"ipv6"`
	Tags            []string `json:"tags"`
	Online          bool     `json:"online"`
	LastSeenAgo     string   `json:"last_seen_ago"`
	ExpiresIn       string   `json:"expires_in"`
	CreatedAgo      string   `json:"created_ago"`
	RegisterMethod  string   `json:"register_method"`
	PreAuthKey      uint64   `json:"preauth_key"`
	MachineKey      string   `json:"machine_key"`
	NodeKey         string   `json:"node_key"`
	DiscoKey        string   `json:"disco_key"`
	AvailableRoutes []string `json:"available_routes"`
	ApprovedRoutes  []string `json:"approved_routes"`
}

// SeedPreAuth is a pre-auth key in the seed. An empty User means tags-only.
type SeedPreAuth struct {
	ID         uint64   `json:"id"`
	Key        string   `json:"key"`
	User       string   `json:"user"`
	Reusable   bool     `json:"reusable"`
	Ephemeral  bool     `json:"ephemeral"`
	Used       bool     `json:"used"`
	ExpiresIn  string   `json:"expires_in"`
	CreatedAgo string   `json:"created_ago"`
	ACLTags    []string `json:"acl_tags"`
}

// SeedAPIKey is an API key in the seed.
type SeedAPIKey struct {
	ID          uint64 `json:"id"`
	Prefix      string `json:"prefix"`
	ExpiresIn   string `json:"expires_in"`
	CreatedAgo  string `json:"created_ago"`
	LastSeenAgo string `json:"last_seen_ago"`
}

// SeedPending is an interactive (web) auth session awaiting approval.
type SeedPending struct {
	AuthID   string `json:"auth_id"`
	Hostname string `json:"hostname"`
}

// LoadSeed parses seed JSON.
func LoadSeed(data []byte) (Seed, error) {
	var s Seed
	if err := json.Unmarshal(data, &s); err != nil {
		return Seed{}, fmt.Errorf("parsing seed: %w", err)
	}
	return s, nil
}

// DemoSeed returns the embedded standard seed.
func DemoSeed() Seed {
	s, err := LoadSeed(demoSeed)
	if err != nil {
		panic(err) // embedded data; covered by tests
	}
	return s
}

func offset(now time.Time, d string, sign time.Duration) time.Time {
	if d == "" {
		return time.Time{}
	}
	dur, err := time.ParseDuration(d)
	if err != nil {
		return time.Time{}
	}
	return now.Add(sign * dur)
}

func prefixes(ss []string) []netip.Prefix {
	var out []netip.Prefix
	for _, s := range ss {
		if p, err := netip.ParsePrefix(s); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// materialise converts the seed into live state relative to now.
func (b *Backend) materialise(s Seed, now time.Time) {
	byName := map[string]api.UserRef{}
	for _, su := range s.Users {
		u := api.User{
			ID: su.ID, Name: su.Name, DisplayName: su.DisplayName, Email: su.Email,
			Provider: su.Provider, ProviderID: su.ProviderID,
			CreatedAt: offset(now, su.CreatedAgo, -1),
		}
		b.users[u.ID] = &u
		byName[u.Name] = u.Ref()
		b.nextUser = max(b.nextUser, u.ID)
	}
	for _, sn := range s.Nodes {
		n := api.Node{
			ID: sn.ID, Name: sn.Name, GivenName: sn.GivenName, User: byName[sn.User],
			Tags: append([]string(nil), sn.Tags...), Online: sn.Online,
			LastSeen:   offset(now, sn.LastSeenAgo, -1),
			CreatedAt:  offset(now, sn.CreatedAgo, -1),
			MachineKey: sn.MachineKey, NodeKey: sn.NodeKey, DiscoKey: sn.DiscoKey,
			RegisterMethod:  api.RegisterMethod(sn.RegisterMethod),
			AvailableRoutes: prefixes(sn.AvailableRoutes),
			ApprovedRoutes:  prefixes(sn.ApprovedRoutes),
		}
		if sn.Online {
			n.LastSeen = now
		}
		for _, ip := range []string{sn.IPv4, sn.IPv6} {
			if a, err := netip.ParseAddr(ip); err == nil {
				n.IPAddresses = append(n.IPAddresses, a)
			}
		}
		if sn.ExpiresIn != "" {
			t := offset(now, sn.ExpiresIn, 1)
			n.Expiry = &t
		}
		if sn.PreAuthKey != 0 {
			n.PreAuthKey = &api.PreAuthKeyRef{ID: sn.PreAuthKey}
		}
		n.SubnetRoutes = served(n)
		b.nodes[n.ID] = &n
		b.nextNode = max(b.nextNode, n.ID)
	}
	for _, sk := range s.PreAuthKeys {
		k := api.PreAuthKey{
			ID: sk.ID, Key: api.NewSecret(sk.Key), Reusable: sk.Reusable,
			Ephemeral: sk.Ephemeral, Used: sk.Used,
			Expiration: offset(now, sk.ExpiresIn, 1),
			CreatedAt:  offset(now, sk.CreatedAgo, -1),
			ACLTags:    append([]string(nil), sk.ACLTags...),
		}
		if ref, ok := byName[sk.User]; ok {
			k.User = &ref
		}
		b.keys[k.ID] = &k
		b.nextKey = max(b.nextKey, k.ID)
	}
	for _, sa := range s.APIKeys {
		a := api.APIKey{
			ID: sa.ID, Prefix: sa.Prefix,
			Expiration: offset(now, sa.ExpiresIn, 1),
			CreatedAt:  offset(now, sa.CreatedAgo, -1),
			LastSeen:   offset(now, sa.LastSeenAgo, -1),
		}
		b.apiKeys[a.ID] = &a
		b.nextAPIKey = max(b.nextAPIKey, a.ID)
	}
	b.policy = api.Policy{Raw: s.Policy, UpdatedAt: offset(now, s.PolicyUpdatedAgo, -1)}
	for _, p := range s.PendingAuth {
		b.pending[p.AuthID] = p.Hostname
	}
}
