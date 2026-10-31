package hsconfig

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/common/model"
	"github.com/tonhe/headsup/internal/host"
	"gopkg.in/yaml.v3"
)

// FieldType is the value type of a config key.
type FieldType int

const (
	TypeString FieldType = iota
	TypeDuration
	TypeBool
	TypeInt
	TypeEnum
	TypeList
	TypeMap
	TypePath
	TypeSecret
	TypeOctal
)

func (t FieldType) String() string {
	return [...]string{"string", "duration", "bool", "int", "enum", "list", "map", "path", "secret", "octal"}[t]
}

// Sections.
const (
	SecGeneral  = "General"
	SecNodes    = "Nodes"
	SecDNS      = "DNS"
	SecDERP     = "DERP"
	SecTLS      = "TLS"
	SecOIDC     = "OIDC"
	SecPolicy   = "Policy"
	SecDatabase = "Database"
	SecLogging  = "Logging"
	SecFeatures = "Features"
)

// Field describes one config key.
type Field struct {
	Key        string
	Section    string
	Type       FieldType
	Default    any
	Since      string // first minor version with the key, e.g. "0.29"
	Deprecated string // first minor version where the key is deprecated or removed
	ReplacedBy string // key that replaces it from Deprecated on ("" = removed)
	Effect     host.Effect
	Danger     host.Danger
	Help       string
	Enum       []string
	Validate   func(value string) error // extra check beyond the type; nil = none
}

func f(key, sec string, t FieldType, def any, help string) Field {
	d := host.DangerConfirm
	for _, p := range []string{"server_url", "listen_addr", "database.", "noise.", "prefixes."} {
		if key == p || strings.HasPrefix(key, p) {
			d = host.DangerCritical
		}
	}
	return Field{Key: key, Section: sec, Type: t, Default: def, Since: "0.29", Effect: host.EffectRestart, Danger: d, Help: help}
}

func (x Field) since(v string) Field              { x.Since = v; return x }
func (x Field) deprecated(v, by string) Field     { x.Deprecated, x.ReplacedBy = v, by; return x }
func (x Field) enum(vals ...string) Field         { x.Type, x.Enum = TypeEnum, vals; return x }
func (x Field) check(fn func(string) error) Field { x.Validate = fn; return x }

var catalog = []Field{
	// General
	f("server_url", SecGeneral, TypeString, "http://127.0.0.1:8080", "URL clients connect to").check(validURL),
	f("listen_addr", SecGeneral, TypeString, "127.0.0.1:8080", "Address the HTTP server binds to").check(hostPort),
	f("metrics_listen_addr", SecGeneral, TypeString, "127.0.0.1:9090", "Address for /metrics and /debug; empty disables").check(optional(hostPort)),
	f("grpc_listen_addr", SecGeneral, TypeString, "127.0.0.1:50443", "gRPC listen address (removed in 0.30)").check(hostPort).deprecated("0.30", ""),
	f("grpc_allow_insecure", SecGeneral, TypeBool, false, "Allow unencrypted gRPC (removed in 0.30)").deprecated("0.30", ""),
	f("trusted_proxies", SecGeneral, TypeList, []string{}, "Reverse proxy CIDRs whose client-IP headers are honoured").check(eachPrefix),
	f("noise.private_key_path", SecGeneral, TypePath, "/var/lib/headscale/noise_private.key", "Noise protocol private key"),
	f("prefixes.v4", SecGeneral, TypeString, "100.64.0.0/10", "IPv4 prefix to allocate node addresses from").check(optional(prefixIn("100.64.0.0/10"))),
	f("prefixes.v6", SecGeneral, TypeString, "fd7a:115c:a1e0::/48", "IPv6 prefix to allocate node addresses from").check(optional(prefixIn("fd7a:115c:a1e0::/48"))),
	f("prefixes.allocation", SecGeneral, TypeEnum, "sequential", "Address allocation strategy").enum("sequential", "random"),
	f("disable_check_updates", SecGeneral, TypeBool, false, "Skip the update check on startup"),
	f("unix_socket", SecGeneral, TypePath, "/var/run/headscale/headscale.sock", "Local API socket"),
	f("unix_socket_permission", SecGeneral, TypeOctal, "0770", "Socket permission bits (quoted octal)"),

	// Nodes
	f("node.expiry", SecNodes, TypeDuration, "0", "Default key expiry for non-tagged nodes; 0 = never"),
	f("node.ephemeral.inactivity_timeout", SecNodes, TypeDuration, "30m", "Delete inactive ephemeral nodes after").check(minDuration(65 * time.Second)),
	f("node.routes.ha.probe_interval", SecNodes, TypeDuration, "10s", "HA subnet router probe interval; 0 disables"),
	f("node.routes.ha.probe_timeout", SecNodes, TypeDuration, "5s", "HA subnet router probe timeout"),

	// DERP
	f("derp.server.enabled", SecDERP, TypeBool, false, "Run the embedded DERP server"),
	f("derp.server.region_id", SecDERP, TypeInt, 999, "Region ID of the embedded DERP server").check(intRange(1, 999)),
	f("derp.server.region_code", SecDERP, TypeString, "headscale", "Region code of the embedded DERP server"),
	f("derp.server.region_name", SecDERP, TypeString, "Headscale Embedded DERP", "Region name of the embedded DERP server"),
	f("derp.server.verify_clients", SecDERP, TypeBool, true, "Only relay for known nodes"),
	f("derp.server.stun_listen_addr", SecDERP, TypeString, "0.0.0.0:3478", "STUN listen address").check(hostPort),
	f("derp.server.private_key_path", SecDERP, TypePath, "/var/lib/headscale/derp_server_private.key", "Embedded DERP private key"),
	f("derp.server.automatically_add_embedded_derp_region", SecDERP, TypeBool, true, "Add the embedded region to the DERP map"),
	f("derp.server.ipv4", SecDERP, TypeString, "", "Public IPv4 advertised for the embedded DERP").check(optional(ipFamily(4))),
	f("derp.server.ipv6", SecDERP, TypeString, "", "Public IPv6 advertised for the embedded DERP").check(optional(ipFamily(6))),
	f("derp.urls", SecDERP, TypeList, []string{"https://controlplane.tailscale.com/derpmap/default"}, "Remote DERP map URLs"),
	f("derp.paths", SecDERP, TypeList, []string{}, "Local DERP map files"),
	f("derp.auto_update_enabled", SecDERP, TypeBool, true, "Periodically refresh DERP sources"),
	f("derp.update_frequency", SecDERP, TypeDuration, "3h", "DERP refresh interval"),

	// Database
	f("database.type", SecDatabase, TypeEnum, "sqlite", "Database backend").enum("sqlite", "sqlite3", "postgres"),
	f("database.debug", SecDatabase, TypeBool, false, "Log SQL statements"),
	f("database.sqlite.path", SecDatabase, TypePath, "/var/lib/headscale/db.sqlite", "SQLite database file"),
	f("database.sqlite.write_ahead_log", SecDatabase, TypeBool, true, "Enable SQLite WAL mode"),
	f("database.sqlite.wal_autocheckpoint", SecDatabase, TypeInt, 1000, "WAL frames before checkpoint; 0 disables"),
	f("database.postgres.host", SecDatabase, TypeString, "", "Postgres host or socket directory"),
	f("database.postgres.port", SecDatabase, TypeInt, 5432, "Postgres port"),
	f("database.postgres.name", SecDatabase, TypeString, "headscale", "Postgres database name"),
	f("database.postgres.user", SecDatabase, TypeString, "", "Postgres user"),
	f("database.postgres.pass", SecDatabase, TypeSecret, "", "Postgres password"),
	f("database.postgres.ssl", SecDatabase, TypeString, "false", "Postgres sslmode"),

	// TLS: 0.29 flat form
	f("tls_cert_path", SecTLS, TypePath, "", "TLS certificate").deprecated("0.30", "tls.cert_path"),
	f("tls_key_path", SecTLS, TypePath, "", "TLS private key").deprecated("0.30", "tls.key_path"),
	f("acme_url", SecTLS, TypeString, "https://acme-v02.api.letsencrypt.org/directory", "ACME directory").check(optional(validURL)).deprecated("0.30", "tls.acme.url"),
	f("acme_email", SecTLS, TypeString, "", "ACME account email").deprecated("0.30", "tls.acme.email"),
	f("tls_letsencrypt_hostname", SecTLS, TypeString, "", "Domain to request a certificate for").deprecated("0.30", "tls.acme.hostname"),
	f("tls_letsencrypt_cache_dir", SecTLS, TypePath, "/var/lib/headscale/cache", "ACME cache directory").deprecated("0.30", "tls.acme.cache_dir"),
	f("tls_letsencrypt_challenge_type", SecTLS, TypeEnum, "HTTP-01", "ACME challenge type").enum("HTTP-01", "TLS-ALPN-01").deprecated("0.30", "tls.acme.challenge_type"),
	f("tls_letsencrypt_listen", SecTLS, TypeString, ":http", "HTTP-01 listener").deprecated("0.30", "tls.acme.http_listen"),
	// TLS: 0.30 nested form
	f("tls.cert_path", SecTLS, TypePath, "", "TLS certificate").since("0.30"),
	f("tls.key_path", SecTLS, TypePath, "", "TLS private key").since("0.30"),
	f("tls.acme.hostname", SecTLS, TypeString, "", "Domain to request a certificate for").since("0.30"),
	f("tls.acme.challenge_type", SecTLS, TypeEnum, "HTTP-01", "ACME challenge type").enum("HTTP-01", "TLS-ALPN-01", "DNS-01").since("0.30"),
	f("tls.acme.http_listen", SecTLS, TypeString, ":http", "HTTP-01 listener").since("0.30"),
	f("tls.acme.url", SecTLS, TypeString, "https://acme-v02.api.letsencrypt.org/directory", "ACME directory").check(optional(validURL)).since("0.30"),
	f("tls.acme.email", SecTLS, TypeString, "", "ACME account email").since("0.30"),
	f("tls.acme.cache_dir", SecTLS, TypePath, "/var/lib/headscale/cache", "ACME cache directory").since("0.30"),

	// Logging
	f("log.level", SecLogging, TypeEnum, "info", "Log level").enum("trace", "debug", "info", "warn", "error", "fatal", "panic"),
	f("log.format", SecLogging, TypeEnum, "text", "Log format").enum("text", "json"),

	// Policy
	f("policy.mode", SecPolicy, TypeEnum, "file", "Where the policy is stored").enum("file", "database"),
	f("policy.path", SecPolicy, TypePath, "", "HuJSON policy file (policy.mode: file)"),

	// DNS
	f("dns.magic_dns", SecDNS, TypeBool, true, "Enable MagicDNS"),
	f("dns.base_domain", SecDNS, TypeString, "example.com", "MagicDNS base domain (must differ from the server_url host)"),
	f("dns.override_local_dns", SecDNS, TypeBool, true, "Force clients to use the tailnet resolvers"),
	f("dns.nameservers.global", SecDNS, TypeList, []string{"1.1.1.1", "1.0.0.1"}, "Global resolvers (IPs or DoH URLs)"),
	f("dns.nameservers.split", SecDNS, TypeMap, map[string][]string{}, "Split DNS: domain -> resolvers"),
	f("dns.search_domains", SecDNS, TypeList, []string{}, "Search domains"),
	f("dns.extra_records", SecDNS, TypeList, []string{}, "Inline extra DNS records"),
	f("dns.extra_records_path", SecDNS, TypePath, "", "File with extra DNS records (exclusive with dns.extra_records)"),
	f("dns.certificates.enabled", SecDNS, TypeBool, false, "Let nodes get TLS certificates for MagicDNS names").since("0.30"),
	f("dns.challenge.propagation_timeout", SecDNS, TypeDuration, "2m", "DNS-01 propagation wait").since("0.30"),

	// OIDC
	f("oidc.only_start_if_oidc_is_available", SecOIDC, TypeBool, true, "Block startup until the IdP is reachable"),
	f("oidc.issuer", SecOIDC, TypeString, "", "OIDC issuer URL").check(optional(validURL)),
	f("oidc.client_id", SecOIDC, TypeString, "", "OIDC client ID"),
	f("oidc.client_secret", SecOIDC, TypeSecret, "", "OIDC client secret (exclusive with client_secret_path)"),
	f("oidc.client_secret_path", SecOIDC, TypePath, "", "File holding the OIDC client secret"),
	f("oidc.scope", SecOIDC, TypeList, []string{"openid", "profile", "email"}, "OIDC scopes"),
	f("oidc.extra_params", SecOIDC, TypeMap, map[string]string{}, "Extra authorization request parameters"),
	f("oidc.allowed_domains", SecOIDC, TypeList, []string{}, "Allowed email domains"),
	f("oidc.allowed_users", SecOIDC, TypeList, []string{}, "Allowed email addresses"),
	f("oidc.allowed_groups", SecOIDC, TypeList, []string{}, "Allowed groups"),
	f("oidc.email_verified_required", SecOIDC, TypeBool, true, "Require verified emails"),
	f("oidc.pkce.enabled", SecOIDC, TypeBool, false, "Use PKCE"),
	f("oidc.pkce.method", SecOIDC, TypeEnum, "S256", "PKCE method").enum("plain", "S256"),
	f("oidc.expiry", SecOIDC, TypeDuration, "180d", "Node expiry for OIDC logins"),
	f("oidc.use_expiry_from_token", SecOIDC, TypeBool, false, "Use the token's expiry for OIDC nodes"),

	// Features
	f("logtail.enabled", SecFeatures, TypeBool, false, "Let clients send logs to Tailscale"),
	f("randomize_client_port", SecFeatures, TypeBool, false, "Randomize the WireGuard port on clients"),
	f("taildrop.enabled", SecFeatures, TypeBool, true, "Enable Taildrop file sharing"),
	f("auto_update.enabled", SecFeatures, TypeBool, false, "Default client auto-update").since("0.30"),
}

var byKey = func() map[string]Field {
	m := make(map[string]Field, len(catalog))
	for _, x := range catalog {
		m[x.Key] = x
	}
	return m
}()

// All returns every field of every version.
func All() []Field { return slices.Clone(catalog) }

// Lookup returns the field for key.
func Lookup(key string) (Field, bool) { x, ok := byKey[key]; return x, ok }

// Catalog returns the fields valid for version ("0.29.6", "v0.30.0", "0.30").
// An empty or unparsable version returns every field.
func Catalog(version string) []Field {
	v, ok := minor(version)
	if !ok {
		return All()
	}
	var out []Field
	for _, x := range catalog {
		s, _ := minor(x.Since)
		if s > v {
			continue
		}
		if dep, ok := minor(x.Deprecated); ok && v >= dep {
			continue
		}
		out = append(out, x)
	}
	return out
}

// Deprecations maps deprecated keys to their replacements for version.
// Removed keys map to "".
func Deprecations(version string) map[string]string {
	v, ok := minor(version)
	out := map[string]string{}
	for _, x := range catalog {
		if dep, dok := minor(x.Deprecated); dok && (!ok || v >= dep) {
			out[x.Key] = x.ReplacedBy
		}
	}
	return out
}

// devMinor is what minor returns for a development build.
const devMinor = 1 << 30

// minor parses "0.30.1" / "v0.30" into 30 (major*1000+minor).
func minor(v string) (int, bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if strings.HasPrefix(v, "0.0.0-") {
		// A development build's pseudo-version: newer than any release.
		return devMinor, true
	}
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		return 0, false
	}
	mi := parts[1]
	if i := strings.IndexFunc(mi, func(r rune) bool { return r < '0' || r > '9' }); i >= 0 {
		mi = mi[:i]
	}
	ma, err1 := strconv.Atoi(parts[0])
	n, err2 := strconv.Atoi(mi)
	if err1 != nil || err2 != nil {
		return 0, false
	}
	return ma*1000 + n, true
}

// --- validation -------------------------------------------------------------

// ParseDuration accepts what headscale accepts: Go durations, bare integers
// (nanoseconds via viper's cast, so "0" means zero) and day/week units.
func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("empty duration")
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return time.Duration(n), nil
	}
	if d, err := time.ParseDuration(s); err == nil {
		return d, nil
	}
	d, err := model.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	return time.Duration(d), nil
}

// CheckValue validates value against field x.
func CheckValue(x Field, value string) error {
	switch x.Type {
	case TypeBool:
		switch strings.ToLower(value) {
		case "true", "false", "yes", "no", "on", "off":
		default:
			return fmt.Errorf("%s: %q is not a boolean", x.Key, value)
		}
	case TypeInt:
		if _, err := strconv.Atoi(value); err != nil {
			return fmt.Errorf("%s: %q is not an integer", x.Key, value)
		}
	case TypeDuration:
		if _, err := ParseDuration(value); err != nil {
			return fmt.Errorf("%s: %v", x.Key, err)
		}
	case TypeEnum:
		if len(x.Enum) > 0 && !slices.Contains(x.Enum, value) {
			return fmt.Errorf("%s: %q is not one of %s", x.Key, value, strings.Join(x.Enum, ", "))
		}
	case TypeOctal:
		if _, err := strconv.ParseUint(value, 8, 32); err != nil || value == "" {
			return fmt.Errorf("%s: %q is not an octal permission", x.Key, value)
		}
	}
	if x.Validate != nil {
		if err := x.Validate(value); err != nil {
			return fmt.Errorf("%s: %v", x.Key, err)
		}
	}
	return nil
}

// Validate type-checks every catalogued key in doc and runs cross-field checks.
func Validate(d *Doc) []host.Issue { return ValidateFor(d, "") }

// ValidateFor is Validate plus deprecation warnings for version.
func ValidateFor(d *Doc, version string) []host.Issue {
	var out []host.Issue
	add := func(sev host.Severity, key, msg string) {
		out = append(out, host.Issue{Severity: sev, Message: msg, Line: d.Line(key), Source: "schema"})
	}
	for _, x := range catalog {
		n, ok := d.Node(x.Key)
		if !ok {
			continue
		}
		switch x.Type {
		case TypeList:
			if n.Kind != yaml.SequenceNode && n.Kind != yaml.ScalarNode {
				add(host.SevError, x.Key, x.Key+": expected a list")
				continue
			}
			if x.Validate != nil {
				l, _ := d.List(x.Key)
				if err := x.Validate(strings.Join(l, "\n")); err != nil {
					add(host.SevError, x.Key, fmt.Sprintf("%s: %v", x.Key, err))
				}
			}
			continue
		case TypeMap:
			if n.Kind != yaml.MappingNode && !(n.Kind == yaml.ScalarNode && n.Tag == "!!null") {
				add(host.SevError, x.Key, x.Key+": expected a mapping")
			}
			continue
		}
		if n.Kind != yaml.ScalarNode {
			add(host.SevError, x.Key, fmt.Sprintf("%s: expected a %s value", x.Key, x.Type))
			continue
		}
		if n.Tag == "!!null" {
			continue
		}
		if x.Type == TypeOctal && n.Style&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle) == 0 {
			add(host.SevError, x.Key, x.Key+" must be a quoted string, e.g. \"0770\"; unquoted YAML reads it as a decimal number")
			continue
		}
		if err := CheckValue(x, n.Value); err != nil {
			add(host.SevError, x.Key, err.Error())
		}
	}
	if d.Has("dns.extra_records") && d.Has("dns.extra_records_path") {
		if l, _ := d.List("dns.extra_records"); len(l) > 0 || nonEmptySeq(d, "dns.extra_records") {
			if p, _ := d.Get("dns.extra_records_path"); p != "" {
				add(host.SevError, "dns.extra_records_path", "dns.extra_records and dns.extra_records_path cannot both be set")
			}
		}
	}
	if bd, _ := d.Get("dns.base_domain"); bd != "" {
		if su, _ := d.Get("server_url"); su != "" {
			if u, err := url.Parse(su); err == nil {
				h := strings.ToLower(u.Hostname())
				b := strings.ToLower(strings.Trim(bd, "."))
				if h == b || strings.HasSuffix(h, "."+b) {
					add(host.SevError, "dns.base_domain", fmt.Sprintf("dns.base_domain %q must not contain the server_url host %q", bd, h))
				}
			}
		}
	}
	if s, _ := d.Get("oidc.client_secret"); s != "" {
		if p, _ := d.Get("oidc.client_secret_path"); p != "" {
			add(host.SevError, "oidc.client_secret_path", "oidc.client_secret and oidc.client_secret_path are mutually exclusive")
		}
	}
	if pm, _ := d.Get("policy.mode"); pm == "database" {
		if p, _ := d.Get("policy.path"); p != "" {
			add(host.SevWarn, "policy.path", "policy.path is ignored when policy.mode is database")
		}
	}
	if version != "" {
		for old, repl := range Deprecations(version) {
			if !d.Has(old) {
				continue
			}
			if repl == "" {
				add(host.SevWarn, old, fmt.Sprintf("%s is no longer used in %s", old, version))
			} else {
				add(host.SevWarn, old, fmt.Sprintf("%s is deprecated in %s; use %s", old, version, repl))
			}
		}
	}
	slices.SortStableFunc(out, func(a, b host.Issue) int { return a.Line - b.Line })
	return out
}

func nonEmptySeq(d *Doc, key string) bool {
	n, ok := d.Node(key)
	return ok && n.Kind == yaml.SequenceNode && len(n.Content) > 0
}

// Danger rates a config change by the most dangerous key it touches.
// Keys outside the catalog count as DangerConfirm.
func Danger(old, next []byte) host.Danger {
	a, err1 := Parse(old)
	b, err2 := Parse(next)
	if err1 != nil || err2 != nil {
		return host.DangerCritical
	}
	worst := host.DangerNone
	for _, k := range ChangedKeys(a, b) {
		dg := host.DangerConfirm
		if x, ok := Lookup(k); ok {
			dg = x.Danger
		} else {
			for _, x := range catalog {
				if strings.HasPrefix(k, x.Key+".") {
					dg = x.Danger
				}
			}
			for _, p := range []string{"database.", "noise.", "prefixes."} {
				if strings.HasPrefix(k, p) {
					dg = host.DangerCritical
				}
			}
		}
		worst = max(worst, dg)
	}
	return worst
}

// ChangedKeys lists the leaf keys whose values differ between a and b.
func ChangedKeys(a, b *Doc) []string {
	ka, kb := a.Keys(), b.Keys()
	var out []string
	for k, v := range ka {
		if w, ok := kb[k]; !ok || w != v {
			out = append(out, k)
		}
	}
	for k := range kb {
		if _, ok := ka[k]; !ok {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

func validURL(s string) error {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("%q is not an http(s) URL", s)
	}
	return nil
}

func hostPort(s string) error {
	_, port, err := splitHostPort(s)
	if err != nil {
		return fmt.Errorf("%q is not host:port", s)
	}
	if p, err := strconv.Atoi(port); err != nil || p < 0 || p > 65535 {
		return fmt.Errorf("%q has an invalid port", s)
	}
	return nil
}

func splitHostPort(s string) (string, string, error) {
	i := strings.LastIndex(s, ":")
	if i < 0 {
		return "", "", errors.New("missing port")
	}
	h := strings.TrimSuffix(strings.TrimPrefix(s[:i], "["), "]")
	return h, s[i+1:], nil
}

func optional(fn func(string) error) func(string) error {
	return func(s string) error {
		if s == "" {
			return nil
		}
		return fn(s)
	}
}

func prefixIn(outer string) func(string) error {
	o := netip.MustParsePrefix(outer)
	return func(s string) error {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return fmt.Errorf("%q is not a CIDR prefix", s)
		}
		if p.Bits() < o.Bits() || !o.Contains(p.Addr()) {
			return fmt.Errorf("%s must be within %s", s, outer)
		}
		return nil
	}
}

func eachPrefix(s string) error {
	for _, l := range strings.Split(s, "\n") {
		if l == "" {
			continue
		}
		if _, err := netip.ParsePrefix(l); err != nil {
			return fmt.Errorf("%q is not a CIDR prefix", l)
		}
	}
	return nil
}

func ipFamily(fam int) func(string) error {
	return func(s string) error {
		a, err := netip.ParseAddr(s)
		if err != nil || (fam == 4) != a.Is4() {
			return fmt.Errorf("%q is not an IPv%d address", s, fam)
		}
		return nil
	}
}

func intRange(lo, hi int) func(string) error {
	return func(s string) error {
		n, err := strconv.Atoi(s)
		if err != nil || n < lo || n > hi {
			return fmt.Errorf("%q must be between %d and %d", s, lo, hi)
		}
		return nil
	}
}

func minDuration(minD time.Duration) func(string) error {
	return func(s string) error {
		d, err := ParseDuration(s)
		if err != nil {
			return err
		}
		if d != 0 && d < minD {
			return fmt.Errorf("%s is below the minimum %s", s, minD)
		}
		return nil
	}
}
