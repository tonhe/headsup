package apitest

import (
	"strconv"
	"time"

	"github.com/tonhe/headsup/internal/api"
)

// Style selects a headscale JSON wire shape for simulated servers and CLIs.
type Style struct {
	SnakeCase   bool // snake_case keys instead of camelCase
	NumericIDs  bool // uint64 as JSON numbers instead of strings
	TimeObjects bool // protobuf {seconds,nanos} instead of RFC 3339
	EnumInts    bool // registerMethod as an integer
	OmitEmpty   bool // drop zero values (encoding/json over protobuf structs)
}

var (
	// StyleV030 is 0.30 REST and CLI output (Huma / oapi-codegen).
	StyleV030 = Style{}
	// StyleV029REST is 0.29 grpc-gateway output (protojson, default options).
	StyleV029REST = Style{}
	// StyleV029CLI is 0.29 `headscale -o json` (encoding/json over protobuf
	// Go structs: snake_case tags with omitempty).
	StyleV029CLI = Style{SnakeCase: true, NumericIDs: true, TimeObjects: true, EnumInts: true, OmitEmpty: true}
)

// M is a JSON object under construction.
type M = map[string]any

func (s Style) key(camel, snake string) string {
	if s.SnakeCase {
		return snake
	}
	return camel
}

func (s Style) put(m M, camel, snake string, v any) {
	if s.OmitEmpty {
		switch t := v.(type) {
		case nil:
			return
		case string:
			if t == "" {
				return
			}
		case bool:
			if !t {
				return
			}
		case []string:
			if len(t) == 0 {
				return
			}
		case M:
			if len(t) == 0 {
				return
			}
		}
	}
	m[s.key(camel, snake)] = v
}

// ID encodes a uint64 identifier.
func (s Style) ID(id uint64) any {
	if s.NumericIDs {
		if id == 0 && s.OmitEmpty {
			return nil
		}
		return id
	}
	return strconv.FormatUint(id, 10)
}

// Time encodes a timestamp; the zero time is null (or omitted).
func (s Style) Time(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	if s.TimeObjects {
		return M{"seconds": t.Unix(), "nanos": t.Nanosecond()}
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func strs[T interface{ String() string }](xs []T) []string {
	out := make([]string, 0, len(xs))
	for _, x := range xs {
		out = append(out, x.String())
	}
	return out
}

func nonNil(ss []string) []string {
	if ss == nil {
		return []string{}
	}
	return ss
}

// User encodes a user.
func (s Style) User(u api.User) M {
	m := M{}
	s.put(m, "id", "id", s.ID(u.ID))
	s.put(m, "name", "name", u.Name)
	s.put(m, "createdAt", "created_at", s.Time(u.CreatedAt))
	s.put(m, "displayName", "display_name", u.DisplayName)
	s.put(m, "email", "email", u.Email)
	s.put(m, "providerId", "provider_id", u.ProviderID)
	s.put(m, "provider", "provider", u.Provider)
	s.put(m, "profilePicUrl", "profile_pic_url", u.ProfilePicURL)
	return m
}

func (s Style) userRef(r api.UserRef) M {
	m := M{}
	s.put(m, "id", "id", s.ID(r.ID))
	s.put(m, "name", "name", r.Name)
	return m
}

func (s Style) method(m api.RegisterMethod) any {
	ints := map[api.RegisterMethod]int{api.RegisterAuthKey: 1, api.RegisterCLI: 2, api.RegisterOIDC: 3}
	strs := map[api.RegisterMethod]string{
		api.RegisterAuthKey: "REGISTER_METHOD_AUTH_KEY", api.RegisterCLI: "REGISTER_METHOD_CLI",
		api.RegisterOIDC: "REGISTER_METHOD_OIDC",
	}
	if s.EnumInts {
		if v, ok := ints[m]; ok {
			return v
		}
		return nil
	}
	if v, ok := strs[m]; ok {
		return v
	}
	return "REGISTER_METHOD_UNSPECIFIED"
}

// Node encodes a node.
func (s Style) Node(n api.Node) M {
	m := M{}
	s.put(m, "id", "id", s.ID(n.ID))
	s.put(m, "machineKey", "machine_key", n.MachineKey)
	s.put(m, "nodeKey", "node_key", n.NodeKey)
	s.put(m, "discoKey", "disco_key", n.DiscoKey)
	s.put(m, "ipAddresses", "ip_addresses", nonNil(strs(n.IPAddresses)))
	s.put(m, "name", "name", n.Name)
	s.put(m, "user", "user", s.userRef(n.User))
	s.put(m, "lastSeen", "last_seen", s.Time(n.LastSeen))
	var exp time.Time
	if n.Expiry != nil {
		exp = *n.Expiry
	}
	s.put(m, "expiry", "expiry", s.Time(exp))
	if n.PreAuthKey != nil {
		s.put(m, "preAuthKey", "pre_auth_key", M{"id": s.ID(n.PreAuthKey.ID)})
	} else if !s.OmitEmpty {
		m[s.key("preAuthKey", "pre_auth_key")] = nil
	}
	s.put(m, "createdAt", "created_at", s.Time(n.CreatedAt))
	s.put(m, "registerMethod", "register_method", s.method(n.RegisterMethod))
	s.put(m, "givenName", "given_name", n.GivenName)
	s.put(m, "online", "online", n.Online)
	s.put(m, "approvedRoutes", "approved_routes", nonNil(strs(n.ApprovedRoutes)))
	s.put(m, "availableRoutes", "available_routes", nonNil(strs(n.AvailableRoutes)))
	s.put(m, "subnetRoutes", "subnet_routes", nonNil(strs(n.SubnetRoutes)))
	s.put(m, "tags", "tags", nonNil(n.Tags))
	return m
}

// PreAuthKey encodes a pre-auth key, revealing the key value.
func (s Style) PreAuthKey(k api.PreAuthKey) M {
	m := M{}
	if k.User != nil {
		s.put(m, "user", "user", s.userRef(*k.User))
	} else if !s.OmitEmpty {
		m["user"] = nil
	}
	s.put(m, "id", "id", s.ID(k.ID))
	s.put(m, "key", "key", k.Key.Reveal())
	s.put(m, "reusable", "reusable", k.Reusable)
	s.put(m, "ephemeral", "ephemeral", k.Ephemeral)
	s.put(m, "used", "used", k.Used)
	s.put(m, "expiration", "expiration", s.Time(k.Expiration))
	s.put(m, "createdAt", "created_at", s.Time(k.CreatedAt))
	s.put(m, "aclTags", "acl_tags", nonNil(k.ACLTags))
	return m
}

// APIKey encodes an API key.
func (s Style) APIKey(k api.APIKey) M {
	m := M{}
	s.put(m, "id", "id", s.ID(k.ID))
	s.put(m, "prefix", "prefix", k.Prefix)
	s.put(m, "expiration", "expiration", s.Time(k.Expiration))
	s.put(m, "createdAt", "created_at", s.Time(k.CreatedAt))
	s.put(m, "lastSeen", "last_seen", s.Time(k.LastSeen))
	return m
}

// Nodes encodes a node list.
func (s Style) Nodes(ns []api.Node) []M {
	out := make([]M, 0, len(ns))
	for _, n := range ns {
		out = append(out, s.Node(n))
	}
	return out
}

// Users encodes a user list.
func (s Style) Users(us []api.User) []M {
	out := make([]M, 0, len(us))
	for _, u := range us {
		out = append(out, s.User(u))
	}
	return out
}

// PreAuthKeys encodes a key list.
func (s Style) PreAuthKeys(ks []api.PreAuthKey) []M {
	out := make([]M, 0, len(ks))
	for _, k := range ks {
		out = append(out, s.PreAuthKey(k))
	}
	return out
}

// APIKeys encodes an API key list.
func (s Style) APIKeys(ks []api.APIKey) []M {
	out := make([]M, 0, len(ks))
	for _, k := range ks {
		out = append(out, s.APIKey(k))
	}
	return out
}

// Key returns the wire key for a camelCase/snake_case pair.
func (s Style) Key(camel, snake string) string { return s.key(camel, snake) }
