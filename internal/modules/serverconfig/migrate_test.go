package serverconfig

import (
	"strings"
	"testing"

	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/host/hsconfig"
)

const flat029 = `server_url: https://hs.example.com
# TLS: certificate files
tls_cert_path: /etc/ssl/hs.pem
tls_key_path: /etc/ssl/hs.key

# ACME
acme_url: https://acme-v02.api.letsencrypt.org/directory
acme_email: "admin@example.com"
tls_letsencrypt_hostname: ""
tls_letsencrypt_cache_dir: /var/lib/headscale/cache
tls_letsencrypt_challenge_type: HTTP-01
tls_letsencrypt_listen: ":http"
grpc_listen_addr: 127.0.0.1:50443
grpc_allow_insecure: false
log:
  level: info
`

func TestMigrateFlatTLSTo030(t *testing.T) {
	out, moves, err := Migrate([]byte(flat029), "0.30.0")
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	d, err := hsconfig.Parse(out)
	if err != nil {
		t.Fatalf("migrated text does not parse: %v\n%s", err, got)
	}
	want := map[string]string{
		"tls.cert_path":           "/etc/ssl/hs.pem",
		"tls.key_path":            "/etc/ssl/hs.key",
		"tls.acme.url":            "https://acme-v02.api.letsencrypt.org/directory",
		"tls.acme.email":          "admin@example.com",
		"tls.acme.hostname":       "",
		"tls.acme.cache_dir":      "/var/lib/headscale/cache",
		"tls.acme.challenge_type": "HTTP-01",
		"tls.acme.http_listen":    ":http",
		"server_url":              "https://hs.example.com",
		"log.level":               "info",
	}
	for k, v := range want {
		if g, ok := d.Get(k); !ok || g != v {
			t.Errorf("%s = %q (%v), want %q", k, g, ok, v)
		}
	}
	for old := range hsconfig.Deprecations("0.30.0") {
		if d.Has(old) {
			t.Errorf("%s still present", old)
		}
	}
	if !strings.Contains(got, "# TLS: certificate files") || !strings.Contains(got, "# ACME") {
		t.Error("comments must survive")
	}
	if len(moves) != 10 {
		t.Errorf("moves = %d: %v", len(moves), moves)
	}
	var removed int
	for _, mv := range moves {
		if mv.To == "" {
			removed++
			if !strings.Contains(mv.String(), "removed") {
				t.Errorf("String %q", mv)
			}
		}
	}
	if removed != 2 {
		t.Errorf("removed = %d, want the two grpc keys", removed)
	}
	// The result validates for 0.30 with no deprecation warnings.
	for _, is := range hsconfig.ValidateFor(d, "0.30.0") {
		if is.Severity >= host.SevWarn {
			t.Errorf("issue after migration: %s", is.Message)
		}
	}
	// Idempotent.
	again, moves2, _ := Migrate(out, "0.30.0")
	if string(again) != got || len(moves2) != 0 {
		t.Error("migrating twice changes nothing")
	}
}

func TestMigrateKeepsConflictingValues(t *testing.T) {
	src := "tls_cert_path: /old.pem\ntls:\n  cert_path: /new.pem\n  key_path: \"\"\ntls_key_path: /k.key\nacme_email:\n"
	out, moves, err := Migrate([]byte(src), "0.30")
	if err != nil {
		t.Fatal(err)
	}
	d, _ := hsconfig.Parse(out)
	if v, _ := d.Get("tls.cert_path"); v != "/new.pem" || !d.Has("tls_cert_path") {
		t.Errorf("conflict must keep both: %s", out)
	}
	if v, _ := d.Get("tls.key_path"); v != "/k.key" || d.Has("tls_key_path") {
		t.Errorf("empty replacement takes the old value: %s", out)
	}
	if d.Has("acme_email") {
		t.Error("null deprecated key is dropped")
	}
	var skipped int
	for _, mv := range moves {
		if mv.Skipped != "" {
			skipped++
			if !strings.Contains(mv.String(), "left in place") {
				t.Errorf("String %q", mv)
			}
		}
	}
	if skipped != 1 {
		t.Errorf("skipped %d: %v", skipped, moves)
	}
}

func TestMigrateNothingOn029(t *testing.T) {
	out, moves, err := Migrate([]byte(flat029), "0.29.6")
	if err != nil || len(moves) != 0 || string(out) != flat029 {
		t.Errorf("0.29 has nothing to migrate: %v %v", moves, err)
	}
	d, _ := hsconfig.Parse([]byte(flat029))
	if len(deprecatedPresent(d, "0.30.0")) != 10 || deprecatedPresent(nil, "0.30") != nil {
		t.Error("deprecatedPresent")
	}
	if _, _, err := Migrate([]byte("a: [\n"), "0.30"); err == nil {
		t.Error("bad YAML")
	}
	if (Move{From: "a", To: "b"}).String() != "a -> b" {
		t.Error("move string")
	}
}
