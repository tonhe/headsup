// Package wire decodes headscale JSON in every shape the supported versions
// emit, so the REST (httpapi) and local-CLI (execcli) backends share one
// mapping onto the api domain types.
//
// Shapes handled:
//
//   - 0.30 REST and CLI (Huma, oapi-codegen): camelCase keys, uint64 IDs as
//     JSON strings, RFC 3339 timestamps, registerMethod as an enum string.
//   - 0.29 REST (grpc-gateway, protojson): camelCase keys (headscale v0.29.6
//     builds the gateway mux with default marshaler options, so not proto
//     names), uint64 as strings, RFC 3339 timestamps, enum strings.
//   - 0.29 CLI (`-o json` is encoding/json over the protobuf Go structs):
//     snake_case keys, uint64 as numbers, timestamps as {seconds, nanos}
//     objects, registerMethod as an enum integer, zero values omitted.
//
// Keys are normalised by lowercasing and removing underscores, so
// "machine_key", "machineKey" and "MachineKey" all become "machinekey".
package wire

import (
	"encoding/json"
	"fmt"
	"math"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/tonhe/headsup/internal/api"
)

// Obj is a JSON object with normalised keys.
type Obj map[string]any

// NormKey lowercases k and strips underscores and dashes.
func NormKey(k string) string {
	k = strings.ToLower(k)
	k = strings.ReplaceAll(k, "_", "")
	return strings.ReplaceAll(k, "-", "")
}

// Normalize recursively rewrites every object key with NormKey.
func Normalize(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(Obj, len(t))
		for k, x := range t {
			out[NormKey(k)] = Normalize(x)
		}
		return out
	case Obj:
		out := make(Obj, len(t))
		for k, x := range t {
			out[NormKey(k)] = Normalize(x)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = Normalize(x)
		}
		return out
	default:
		return v
	}
}

// Decode parses JSON (numbers kept exact) and normalises its keys.
func Decode(data []byte) (any, error) {
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return Normalize(v), nil
}

// DecodeObj parses a JSON object. An empty body yields an empty Obj.
func DecodeObj(data []byte) (Obj, error) {
	if strings.TrimSpace(string(data)) == "" {
		return Obj{}, nil
	}
	v, err := Decode(data)
	if err != nil {
		return nil, err
	}
	o, ok := v.(Obj)
	if !ok {
		if v == nil {
			return Obj{}, nil
		}
		return nil, fmt.Errorf("expected a JSON object, got %T", v)
	}
	return o, nil
}

// AsObj converts v to an Obj if it is one.
func AsObj(v any) (Obj, bool) {
	o, ok := v.(Obj)
	return o, ok
}

// Get returns the raw value at a (normalised) key.
func (o Obj) Get(k string) any { return o[NormKey(k)] }

// Obj returns a nested object, or nil.
func (o Obj) Obj(k string) Obj {
	x, _ := o.Get(k).(Obj)
	return x
}

// Arr returns a nested array, or nil.
func (o Obj) Arr(k string) []any {
	x, _ := o.Get(k).([]any)
	return x
}

// Objs returns a nested array of objects.
func (o Obj) Objs(k string) []Obj { return Objs(o.Get(k)) }

// Objs converts an array value into objects, skipping non-objects.
func Objs(v any) []Obj {
	arr, _ := v.([]any)
	out := make([]Obj, 0, len(arr))
	for _, x := range arr {
		if ob, ok := x.(Obj); ok {
			out = append(out, ob)
		}
	}
	return out
}

// Str returns a string field; numbers and bools are formatted.
func (o Obj) Str(k string) string { return toStr(o.Get(k)) }

func toStr(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case json.Number:
		return t.String()
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	default:
		return fmt.Sprint(t)
	}
}

// U64 returns a uint64 field encoded as a JSON string or number.
func (o Obj) U64(k string) uint64 { return ToU64(o.Get(k)) }

// ToU64 converts a string or number value to uint64 (0 if not numeric).
func ToU64(v any) uint64 {
	switch t := v.(type) {
	case string:
		n, _ := strconv.ParseUint(strings.TrimSpace(t), 10, 64)
		return n
	case json.Number:
		n, err := strconv.ParseUint(t.String(), 10, 64)
		if err != nil {
			f, _ := t.Float64()
			if f > 0 && f < math.MaxUint64 {
				return uint64(f)
			}
		}
		return n
	case float64:
		if t > 0 {
			return uint64(t)
		}
	case int:
		if t > 0 {
			return uint64(t)
		}
	}
	return 0
}

// Bool returns a bool field (also accepting "true"/"false" strings).
func (o Obj) Bool(k string) bool {
	switch t := o.Get(k).(type) {
	case bool:
		return t
	case string:
		b, _ := strconv.ParseBool(t)
		return b
	}
	return false
}

// Strs returns a string-array field.
func (o Obj) Strs(k string) []string {
	arr := o.Arr(k)
	if len(arr) == 0 {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, x := range arr {
		out = append(out, toStr(x))
	}
	return out
}

// Time returns a timestamp field: RFC 3339 string, protobuf
// {seconds, nanos} object, or null. Missing or null yields the zero time.
func (o Obj) Time(k string) time.Time { return ToTime(o.Get(k)) }

// ToTime converts a wire timestamp value.
func ToTime(v any) time.Time {
	switch t := v.(type) {
	case string:
		if t == "" {
			return time.Time{}
		}
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
			if ts, err := time.Parse(layout, t); err == nil {
				return normZero(ts)
			}
		}
	case Obj:
		sec := int64(ToU64(t["seconds"]))
		if n, ok := t["seconds"].(json.Number); ok {
			sec, _ = n.Int64()
		}
		nanos := int64(ToU64(t["nanos"]))
		if sec == 0 && nanos == 0 {
			return time.Time{}
		}
		return time.Unix(sec, nanos).UTC()
	}
	return time.Time{}
}

// normZero maps the protobuf/Go zero-ish instants to time.Time{}.
func normZero(t time.Time) time.Time {
	if t.Year() <= 1 || t.Unix() == 0 {
		return time.Time{}
	}
	return t
}

// UserRef decodes a nested user object.
func UserRef(o Obj) api.UserRef {
	if o == nil {
		return api.UserRef{}
	}
	return api.UserRef{ID: o.U64("id"), Name: o.Str("name")}
}

// User decodes a user object.
func User(o Obj) api.User {
	return api.User{
		ID:            o.U64("id"),
		Name:          o.Str("name"),
		DisplayName:   o.Str("displayname"),
		Email:         o.Str("email"),
		Provider:      o.Str("provider"),
		ProviderID:    o.Str("providerid"),
		ProfilePicURL: o.Str("profilepicurl"),
		CreatedAt:     o.Time("createdat"),
	}
}

// Users decodes an array of user objects.
func Users(v any) []api.User {
	objs := Objs(v)
	out := make([]api.User, 0, len(objs))
	for _, o := range objs {
		out = append(out, User(o))
	}
	return out
}

// RegisterMethod decodes the enum as a string or protobuf integer.
func RegisterMethod(v any) api.RegisterMethod {
	s := strings.ToUpper(toStr(v))
	switch s {
	case "1", "REGISTER_METHOD_AUTH_KEY", "AUTHKEY", "AUTH_KEY":
		return api.RegisterAuthKey
	case "2", "REGISTER_METHOD_CLI", "CLI":
		return api.RegisterCLI
	case "3", "REGISTER_METHOD_OIDC", "OIDC":
		return api.RegisterOIDC
	}
	return api.RegisterUnknown
}

// Node decodes a node object.
func Node(o Obj) api.Node {
	n := api.Node{
		ID:              o.U64("id"),
		Name:            o.Str("name"),
		GivenName:       o.Str("givenname"),
		User:            UserRef(o.Obj("user")),
		IPAddresses:     Addrs(o.Strs("ipaddresses")),
		Tags:            o.Strs("tags"),
		Online:          o.Bool("online"),
		LastSeen:        o.Time("lastseen"),
		CreatedAt:       o.Time("createdat"),
		RegisterMethod:  RegisterMethod(o.Get("registermethod")),
		MachineKey:      o.Str("machinekey"),
		NodeKey:         o.Str("nodekey"),
		DiscoKey:        o.Str("discokey"),
		AvailableRoutes: Prefixes(o.Strs("availableroutes")),
		ApprovedRoutes:  Prefixes(o.Strs("approvedroutes")),
		SubnetRoutes:    Prefixes(o.Strs("subnetroutes")),
	}
	if exp := o.Time("expiry"); !exp.IsZero() {
		n.Expiry = &exp
	}
	if pk := o.Obj("preauthkey"); pk != nil && pk.U64("id") != 0 {
		n.PreAuthKey = &api.PreAuthKeyRef{ID: pk.U64("id")}
	}
	return n
}

// Nodes decodes an array of node objects.
func Nodes(v any) []api.Node {
	objs := Objs(v)
	out := make([]api.Node, 0, len(objs))
	for _, o := range objs {
		out = append(out, Node(o))
	}
	return out
}

// PreAuthKey decodes a pre-auth key object.
func PreAuthKey(o Obj) api.PreAuthKey {
	k := api.PreAuthKey{
		ID:         o.U64("id"),
		Key:        api.NewSecret(o.Str("key")),
		Reusable:   o.Bool("reusable"),
		Ephemeral:  o.Bool("ephemeral"),
		Used:       o.Bool("used"),
		Expiration: o.Time("expiration"),
		CreatedAt:  o.Time("createdat"),
		ACLTags:    o.Strs("acltags"),
	}
	if u := o.Obj("user"); u != nil && (u.U64("id") != 0 || u.Str("name") != "") {
		ref := UserRef(u)
		k.User = &ref
	}
	return k
}

// PreAuthKeys decodes an array of pre-auth key objects.
func PreAuthKeys(v any) []api.PreAuthKey {
	objs := Objs(v)
	out := make([]api.PreAuthKey, 0, len(objs))
	for _, o := range objs {
		out = append(out, PreAuthKey(o))
	}
	return out
}

// APIKey decodes an API key object.
func APIKey(o Obj) api.APIKey {
	return api.APIKey{
		ID:         o.U64("id"),
		Prefix:     o.Str("prefix"),
		Expiration: o.Time("expiration"),
		CreatedAt:  o.Time("createdat"),
		LastSeen:   o.Time("lastseen"),
	}
}

// APIKeys decodes an array of API key objects.
func APIKeys(v any) []api.APIKey {
	objs := Objs(v)
	out := make([]api.APIKey, 0, len(objs))
	for _, o := range objs {
		out = append(out, APIKey(o))
	}
	return out
}

// Addrs parses IP strings, skipping invalid ones.
func Addrs(ss []string) []netip.Addr {
	if len(ss) == 0 {
		return nil
	}
	out := make([]netip.Addr, 0, len(ss))
	for _, s := range ss {
		if a, err := netip.ParseAddr(strings.TrimSpace(s)); err == nil {
			out = append(out, a)
		}
	}
	return out
}

// Prefixes parses CIDR strings, skipping invalid ones.
func Prefixes(ss []string) []netip.Prefix {
	if len(ss) == 0 {
		return nil
	}
	out := make([]netip.Prefix, 0, len(ss))
	for _, s := range ss {
		if p, err := netip.ParsePrefix(strings.TrimSpace(s)); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// PrefixStrings formats prefixes for a request body.
func PrefixStrings(ps []netip.Prefix) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.String())
	}
	return out
}

var posPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)line\s+(\d+),?\s*(?:column|col)\s+(\d+)`),
	regexp.MustCompile(`(?i)line\s+(\d+):(\d+)`),
	regexp.MustCompile(`(?i)at\s+(\d+):(\d+)`),
	regexp.MustCompile(`:(\d+):(\d+)`),
}

var lineOnly = regexp.MustCompile(`(?i)line\s+(\d+)`)

// PolicyPos extracts a 1-based line and column from a policy error message.
// It returns zeros when the message carries no position.
func PolicyPos(msg string) (line, col int) {
	for _, re := range posPatterns {
		if m := re.FindStringSubmatch(msg); m != nil {
			l, _ := strconv.Atoi(m[1])
			c, _ := strconv.Atoi(m[2])
			return l, c
		}
	}
	if m := lineOnly.FindStringSubmatch(msg); m != nil {
		l, _ := strconv.Atoi(m[1])
		return l, 0
	}
	return 0, 0
}

// CapsFor is api.CapsForVersion refined with facts verified against the
// headscale sources: v0.29.6 already accepts disableExpiry on
// POST /node/{id}/expire (swagger query param) and `nodes expire --disable`.
func CapsFor(version string) api.Capabilities {
	c := api.CapsForVersion(version)
	if patchAtLeast(version, 0, 29, 6) {
		c = c.With(api.CapDisableExpiry)
	}
	return c
}

func patchAtLeast(v string, major, minor, patch int) bool {
	maj, min, ok := api.ParseMajorMinor(v)
	if !ok {
		return false
	}
	if maj != major || min != minor {
		return api.VersionAtLeast(v, major, minor)
	}
	parts := strings.SplitN(strings.TrimPrefix(strings.TrimSpace(v), "v"), ".", 3)
	if len(parts) < 3 {
		return false
	}
	p := 0
	for _, r := range parts[2] {
		if r < '0' || r > '9' {
			break
		}
		p = p*10 + int(r-'0')
	}
	return p >= patch
}
