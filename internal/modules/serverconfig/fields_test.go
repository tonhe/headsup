package serverconfig

import (
	"strings"
	"testing"

	"github.com/tonhe/headsup/internal/host/hsconfig"
	"github.com/tonhe/headsup/internal/icons"
	"github.com/tonhe/headsup/internal/module"
)

func field(t *testing.T, key string) hsconfig.Field {
	t.Helper()
	x, ok := hsconfig.Lookup(key)
	if !ok {
		t.Fatalf("no catalog field %s", key)
	}
	return x
}

func TestEditFormKindsByCatalogType(t *testing.T) {
	cases := []struct {
		key  string
		kind module.FieldKind
		req  bool
	}{
		{"node.expiry", module.FieldText, true},                        // duration
		{"log.level", module.FieldSelect, false},                       // enum
		{"taildrop.enabled", module.FieldToggle, false},                // bool
		{"oidc.client_secret", module.FieldSecret, false},              // secret
		{"unix_socket_permission", module.FieldText, true},             // quoted octal
		{"trusted_proxies", module.FieldText, false},                   // list
		{"oidc.extra_params", module.FieldText, false},                 // map
		{"database.sqlite.wal_autocheckpoint", module.FieldText, true}, // int
		{"server_url", module.FieldText, true},                         // string that cannot be empty
		{"oidc.client_id", module.FieldText, false},                    // string that may be empty
		{"policy.path", module.FieldText, false},                       // path
	}
	for _, c := range cases {
		spec := editForm(field(t, c.key), "", true)
		f := spec.Fields[0]
		if f.Kind != c.kind || f.Required != c.req {
			t.Errorf("%s: kind %v required %v, want %v %v", c.key, f.Kind, f.Required, c.kind, c.req)
		}
		if !strings.Contains(spec.Title, c.key) || !strings.Contains(f.Help, "default") {
			t.Errorf("%s: title %q help %q", c.key, spec.Title, f.Help)
		}
	}
	// Enum keeps an unknown current value selectable and seeds from the default.
	f := editForm(field(t, "log.level"), "verbose", true).Fields[0]
	if f.Options[0] != "verbose" || f.Value != "verbose" {
		t.Errorf("enum options %v value %q", f.Options, f.Value)
	}
	if f := editForm(field(t, "log.level"), "", false).Fields[0]; f.Value != "info" {
		t.Errorf("absent enum seeds the default, got %q", f.Value)
	}
	// Secrets never seed the current value.
	if f := editForm(field(t, "oidc.client_secret"), "hunter2", true).Fields[0]; f.Value != "" || !strings.Contains(f.Placeholder, "keep") {
		t.Errorf("secret seeded %q placeholder %q", f.Value, f.Placeholder)
	}
	if f := editForm(field(t, "dns.nameservers.split"), "", false).Fields[0]; !strings.Contains(f.Placeholder, "domain=") {
		t.Errorf("list map placeholder %q", f.Placeholder)
	}
	if f := editForm(field(t, "trusted_proxies"), "", false).Fields[0]; f.Value != "" {
		t.Errorf("empty list default seeds %q", f.Value)
	}
}

func TestValidatorMessages(t *testing.T) {
	cases := []struct {
		key, in, want string // want "" = valid
	}{
		{"node.expiry", "180d", ""},
		{"node.expiry", "0", ""},
		{"node.expiry", "1w", ""},
		{"node.expiry", "soon", "invalid duration"},
		{"node.ephemeral.inactivity_timeout", "10s", "below the minimum"},
		{"unix_socket_permission", "0770", ""},
		{"unix_socket_permission", "0789", "not octal"},
		{"trusted_proxies", "10.0.0.0/8, 192.168.0.0/16", ""},
		{"trusted_proxies", "10.0.0.0/8, nope", "not a CIDR"},
		{"oidc.extra_params", "domain_hint=example.com; prompt=login", ""},
		{"oidc.extra_params", "domain_hint", "expected key=value"},
		{"oidc.extra_params", "a=1; a=2", "twice"},
		{"policy.path", "~/policy.hujson", "does not expand ~"},
		{"policy.path", "/etc/headscale/policy.hujson", ""},
		{"server_url", "ftp://x", "not an http(s) URL"},
		{"database.sqlite.wal_autocheckpoint", "many", "not an integer"},
		{"oidc.client_secret", "", ""},
		{"log.level", "loud", "not one of"},
	}
	for _, c := range cases {
		err := validator(field(t, c.key))(c.in)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s %q: unexpected %v", c.key, c.in, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s %q: got %v, want %q", c.key, c.in, err, c.want)
		case err != nil && strings.HasPrefix(err.Error(), c.key+":"):
			t.Errorf("%s: message repeats the key: %v", c.key, err)
		}
	}
}

const sample = `# Server
server_url: https://hs.example.com
# Socket permission (octal, quoted)
unix_socket_permission: "0770"
node:
  # expiry comment
  expiry: 0
trusted_proxies: []
oidc:
  client_secret: hunter2
  extra_params:
    domain_hint: example.com
`

func stageOne(t *testing.T, key, in string) (string, bool) {
	t.Helper()
	d, err := hsconfig.Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	changed, err := stage(d, field(t, key), in)
	if err != nil {
		t.Fatalf("%s: %v", key, err)
	}
	return string(d.Bytes()), changed
}

func TestStageByType(t *testing.T) {
	cases := []struct{ key, in, want string }{
		{"node.expiry", "180d", "  # expiry comment\n  expiry: 180d\n"},
		{"unix_socket_permission", "0660", `unix_socket_permission: "0660"`},
		{"unix_socket_permission", "750", `unix_socket_permission: "0750"`},
		{"trusted_proxies", "10.0.0.0/8 192.168.0.0/16", "trusted_proxies: [10.0.0.0/8, 192.168.0.0/16]\n"},
		{"oidc.extra_params", "domain_hint=corp.example.com", "  extra_params:\n    domain_hint: corp.example.com\n"},
		{"oidc.client_secret", "s3cret", "client_secret: s3cret"},
		{"log.level", "debug", "log:\n  level: debug"},
		{"taildrop.enabled", "false", "taildrop:\n  enabled: false"},
	}
	for _, c := range cases {
		got, changed := stageOne(t, c.key, c.in)
		if !changed || !strings.Contains(got, c.want) {
			t.Errorf("%s=%q: changed=%v, want %q in\n%s", c.key, c.in, changed, c.want, got)
		}
		if !strings.Contains(got, "# Socket permission (octal, quoted)") {
			t.Errorf("%s: comments lost", c.key)
		}
	}
	// An empty secret keeps the current value.
	if got, changed := stageOne(t, "oidc.client_secret", ""); changed || !strings.Contains(got, "hunter2") {
		t.Error("empty secret input must keep the value")
	}
	// Same value: no change.
	if _, changed := stageOne(t, "node.expiry", "0"); changed {
		t.Error("unchanged value reported as a change")
	}
	d, _ := hsconfig.Parse([]byte(sample))
	if _, err := stage(d, field(t, "node.expiry"), "soon"); err == nil || !strings.Contains(err.Error(), "node.expiry") {
		t.Errorf("invalid input: %v", err)
	}
	d2, _ := hsconfig.Parse([]byte("dns:\n  nameservers:\n    split: {}\n"))
	if _, err := stage(d2, field(t, "dns.nameservers.split"), "corp.example.com=10.0.0.53, 10.0.0.54"); err != nil {
		t.Fatal(err)
	}
	if got := string(d2.Bytes()); !strings.Contains(got, "corp.example.com:\n        - 10.0.0.53\n        - 10.0.0.54") {
		t.Errorf("split map:\n%s", got)
	}
}

func TestRowsAndDisplay(t *testing.T) {
	base, _ := hsconfig.Parse([]byte(sample))
	doc, _ := hsconfig.Parse([]byte(sample))
	_, _ = stage(doc, field(t, "node.expiry"), "7d")
	rs := rowsFor(hsconfig.SecNodes, "0.30.0", doc, base)
	if len(rs) == 0 || rs[0].field.Key != "node.expiry" || !rs[0].modified || rs[0].value != "7d" {
		t.Fatalf("nodes rows: %+v", rs)
	}
	for _, r := range rs[1:] {
		if r.present || r.modified {
			t.Errorf("%s should be unset and unmodified", r.field.Key)
		}
	}
	oidc := rowsFor(hsconfig.SecOIDC, "0.30.0", doc, base)
	for _, r := range oidc {
		v := displayValue(icons.Unicode, r)
		if strings.Contains(v, "hunter2") {
			t.Fatal("secret displayed")
		}
		if r.field.Key == "oidc.client_secret" && v != "••••••••" {
			t.Errorf("masked = %q", v)
		}
		if r.field.Key == "oidc.extra_params" && v != "domain_hint=example.com" {
			t.Errorf("map display = %q", v)
		}
	}
	if mask(icons.ASCII, "x") != "********" || mask(icons.ASCII, "") != "" {
		t.Error("ascii mask")
	}
	if got := displayValue(icons.Unicode, row{field: field(t, "trusted_proxies"), present: true}); got != "[]" {
		t.Errorf("empty list display %q", got)
	}
	if got := displayValue(icons.Unicode, row{field: field(t, "oidc.client_secret"), present: true}); got != "(empty)" {
		t.Errorf("empty secret display %q", got)
	}

	// 0.29-era keys show up flagged in a 0.30 document.
	flat, _ := hsconfig.Parse([]byte("tls_cert_path: /x.pem\n"))
	tls := rowsFor(hsconfig.SecTLS, "0.30.0", flat, flat)
	if len(tls) == 0 || tls[0].field.Key != "tls_cert_path" || !tls[0].deprecated {
		t.Fatalf("tls rows: %+v", tls[:1])
	}
	// On 0.29 the flat keys are current and the nested ones do not exist.
	for _, r := range rowsFor(hsconfig.SecTLS, "0.29.6", flat, flat) {
		if strings.HasPrefix(r.field.Key, "tls.") || r.deprecated {
			t.Errorf("0.29 row %s deprecated=%v", r.field.Key, r.deprecated)
		}
	}
}

func TestLabels(t *testing.T) {
	for in, want := range map[any]string{"": "(empty)", "x": "x", nil: "(none)", true: "true", 5: "5"} {
		if got := defaultText(hsconfig.Field{Default: in}); got != want {
			t.Errorf("defaultText(%v) = %q", in, got)
		}
	}
	if defaultText(hsconfig.Field{Default: []string{}}) != "[]" || defaultText(hsconfig.Field{Default: []string{"a", "b"}}) != "a, b" ||
		defaultText(hsconfig.Field{Default: map[string]string{}}) != "{}" {
		t.Error("collection defaults")
	}
	if typeLabel(field(t, "unix_socket_permission")) != "quoted octal" || typeLabel(field(t, "node.expiry")) != "duration" ||
		typeLabel(field(t, "trusted_proxies")) != "list" || typeLabel(field(t, "oidc.extra_params")) != "map" {
		t.Error("type labels")
	}
	if effectLabel(0) != "applies on save" || effectLabel(2) != "needs restart" {
		t.Error("effect labels")
	}
}

func TestMaskLine(t *testing.T) {
	for in, want := range map[string]string{
		"  client_secret: hunter2":     "  client_secret: ••••••••",
		"    pass: bar":                "    pass: ••••••••",
		`  client_secret: ""`:          `  client_secret: ""`,
		"  client_secret_path: /x":     "  client_secret_path: /x",
		"#   client_secret: \"yours\"": "#   client_secret: \"yours\"",
		"  client_id: hs":              "  client_id: hs",
	} {
		if got := maskLine(icons.Unicode, in); got != want {
			t.Errorf("maskLine(%q) = %q, want %q", in, got, want)
		}
	}
}
