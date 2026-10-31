package api

import (
	"sort"
	"strings"
)

// Cap is a single server capability modules can gate on.
type Cap uint32

const (
	CapNone             Cap = 0
	CapHealthEndpoint   Cap = 1 << iota
	CapPolicyCheck          // POST /policy/check
	CapPolicyWritable       // policy.mode == database
	CapAuthApproval         // /auth/{register,approve,reject}
	CapPreAuthKeyDelete     // DELETE /preauthkey
	CapAPIKeyDelete         // DELETE /apikey/{prefix}
	CapOAuthClients         // planned v1.1
	CapProblemJSON          // RFC 7807 errors
	CapTagOnlyKeys          // pre-auth keys without a user
	CapBackfillIPs          // POST /node/backfillips
	CapDisableExpiry        // ExpireNode.disableExpiry
)

var capNames = map[Cap]string{
	CapHealthEndpoint:   "health endpoint",
	CapPolicyCheck:      "policy check",
	CapPolicyWritable:   "policy writable via API",
	CapAuthApproval:     "web-auth approval",
	CapPreAuthKeyDelete: "pre-auth key delete",
	CapAPIKeyDelete:     "API key delete",
	CapOAuthClients:     "OAuth clients (planned)",
	CapProblemJSON:      "problem+json errors",
	CapTagOnlyKeys:      "tag-only pre-auth keys",
	CapBackfillIPs:      "backfill node IPs",
	CapDisableExpiry:    "disable node expiry",
}

func (c Cap) String() string {
	if n, ok := capNames[c]; ok {
		return n
	}
	return "unknown capability"
}

// AllCaps lists every defined capability in a stable order.
func AllCaps() []Cap {
	out := make([]Cap, 0, len(capNames))
	for c := range capNames {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Capabilities is a set of Cap flags.
type Capabilities struct {
	set Cap
}

// NewCapabilities builds a set from flags.
func NewCapabilities(cs ...Cap) Capabilities {
	var c Capabilities
	for _, x := range cs {
		c.set |= x
	}
	return c
}

// Has reports whether every given capability is present. CapNone is always present.
func (c Capabilities) Has(x Cap) bool { return c.set&x == x }

// With returns a copy with x added.
func (c Capabilities) With(x Cap) Capabilities { c.set |= x; return c }

// Without returns a copy with x removed.
func (c Capabilities) Without(x Cap) Capabilities { c.set &^= x; return c }

func (c Capabilities) String() string {
	var names []string
	for _, x := range AllCaps() {
		if c.Has(x) {
			names = append(names, x.String())
		}
	}
	return strings.Join(names, ", ")
}

// CapsForVersion returns the capabilities a server version is expected to
// have. Detection may refine these (e.g. CapPolicyWritable is learned lazily).
func CapsForVersion(v string) Capabilities {
	c := NewCapabilities(CapHealthEndpoint, CapPolicyWritable, CapBackfillIPs,
		CapPreAuthKeyDelete, CapAPIKeyDelete, CapPolicyCheck, CapAuthApproval)
	if versionAtLeastPatch(v, 0, 29, 6) {
		c = c.With(CapDisableExpiry)
	}
	if VersionAtLeast(v, 0, 30) {
		c = c.With(CapProblemJSON).With(CapTagOnlyKeys).With(CapDisableExpiry)
	}
	return c
}

// versionAtLeastPatch is VersionAtLeast including the patch number.
func versionAtLeastPatch(v string, major, minor, patch int) bool {
	maj, min, ok := ParseMajorMinor(v)
	if !ok {
		return true
	}
	if maj != major || min != minor {
		return VersionAtLeast(v, major, minor)
	}
	parts := strings.SplitN(strings.TrimPrefix(strings.TrimSpace(v), "v"), ".", 3)
	if len(parts) < 3 {
		return false
	}
	p, ok := atoi(parts[2])
	return ok && p >= patch
}

// VersionAtLeast reports whether a "v0.29.6"-style version is >= major.minor.
// Unparseable versions (e.g. "dev") are treated as newest.
func VersionAtLeast(v string, major, minor int) bool {
	maj, min, ok := ParseMajorMinor(v)
	if !ok {
		return true
	}
	return maj > major || (maj == major && min >= minor)
}

// ParseMajorMinor extracts major and minor from a version string. A Go
// pseudo-version ("v0.0.0-20261030160536-7721d8c29978", what headscale's
// development builds report) is not a release, so it is unparseable here and
// callers treat it as newest.
func ParseMajorMinor(v string) (int, int, bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if IsDevVersion(v) {
		return 0, 0, false
	}
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		return 0, 0, false
	}
	maj, ok1 := atoi(parts[0])
	min, ok2 := atoi(parts[1])
	return maj, min, ok1 && ok2
}

// IsDevVersion reports whether v is the Go pseudo-version of an untagged
// build (headscale built from main reports "v0.0.0-<timestamp>-<commit>").
func IsDevVersion(v string) bool {
	return strings.HasPrefix(strings.TrimPrefix(strings.TrimSpace(v), "v"), "0.0.0-")
}

func atoi(s string) (int, bool) {
	n := 0
	if s == "" {
		return 0, false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
	}
	return n, s[0] >= '0' && s[0] <= '9'
}

// Supported reports whether a version is in the supported range (0.29.x, 0.30.x).
func Supported(v string) bool {
	maj, min, ok := ParseMajorMinor(v)
	if !ok {
		return true
	}
	return maj == 0 && (min == 29 || min == 30)
}
