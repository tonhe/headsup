package hsconfig

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/host/files"
)

var update = flag.Bool("update", false, "rewrite golden files")

var fixtures = []string{"v029-example", "v030-example", "hand-written"}

func load(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "hsconfig", name+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func comments(b []byte) []string {
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if t := strings.TrimSpace(l); strings.HasPrefix(t, "#") {
			out = append(out, t)
		} else if i := strings.Index(l, " #"); i >= 0 && !strings.Contains(l[:i], "\"") {
			out = append(out, strings.TrimSpace(l[i:]))
		}
	}
	return out
}

func TestRoundTripIdentity(t *testing.T) {
	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			src := load(t, name)
			d, err := Parse(src)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(d.Bytes(), src) {
				t.Fatal("Bytes() differs from input without edits")
			}
		})
	}
}

func TestSingleKeyEditChangesOneLine(t *testing.T) {
	tests := []struct {
		file, key, value string
	}{
		{"v029-example", "log.level", "debug"},
		{"v029-example", "server_url", "https://hs.example.com:443"},
		{"v029-example", "grpc_listen_addr", "0.0.0.0:50443"},
		{"v029-example", "tls_letsencrypt_hostname", "hs.example.com"},
		{"v029-example", "derp.server.region_id", "901"},
		{"v029-example", "unix_socket_permission", "0700"},
		{"v030-example", "dns.base_domain", "tail.example.org"},
		{"v030-example", "node.ephemeral.inactivity_timeout", "1h"},
		{"v030-example", "tls.acme.email", "ops@example.com"},
		{"v030-example", "policy.path", "/etc/headscale/policy.hujson"},
		{"v030-example", "derp.server.enabled", "true"},
		{"v030-example", "database.sqlite.write_ahead_log", "false"},
		{"hand-written", "log.level", "debug"},
		{"hand-written", "server_url", "https://hs2.lab.example.net"},
		{"hand-written", "metrics_listen_addr", "127.0.0.1:9091"},
		{"hand-written", "derp.server.region_code", "lab2"},
		{"hand-written", "dns.base_domain", "ts2.lab.example.net"},
	}
	for _, tt := range tests {
		t.Run(tt.file+"/"+tt.key, func(t *testing.T) {
			src := load(t, tt.file)
			d, err := Parse(src)
			if err != nil {
				t.Fatal(err)
			}
			if err := d.Set(tt.key, tt.value); err != nil {
				t.Fatal(err)
			}
			out := d.Bytes()
			add, rem := files.Changed(files.Diff(src, out))
			if add != 1 || rem != 1 {
				t.Fatalf("diff +%d -%d, want +1 -1\n%s", add, rem, out)
			}
			if !slices.Equal(comments(src), comments(out)) {
				t.Fatal("comments changed")
			}
			d2, err := Parse(out)
			if err != nil {
				t.Fatal(err)
			}
			{
				if got, _ := d2.Get(tt.key); got != tt.value {
					t.Fatalf("Get(%s) = %q, want %q", tt.key, got, tt.value)
				}
			}
		})
	}
}

func TestQuotingStyleKept(t *testing.T) {
	tests := []struct {
		src, key, value, want string
	}{
		{"a: 'x'\n", "a", "y", "a: 'y'\n"},
		{"a: \"x\" # keep\n", "a", "y", "a: \"y\" # keep\n"},
		{"a: x\n", "a", "it's: odd", "a: \"it's: odd\"\n"},
		{"unix_socket_permission: 0770\n", "unix_socket_permission", "0770", "unix_socket_permission: \"0770\"\n"},
		{"unix_socket_permission: '0770'\n", "unix_socket_permission", "0700", "unix_socket_permission: \"0700\"\n"},
		{"dns:\n  base_domain: a.com\n", "dns.base_domain", "true", "dns:\n  base_domain: \"true\"\n"},
		{"dns:\n  magic_dns: false\n", "dns.magic_dns", "true", "dns:\n  magic_dns: true\n"},
		{"key:\nother: 1\n", "key", "v", "key: v\nother: 1\n"},
		{"key: # c\nother: 1\n", "key", "v", "key: v # c\nother: 1\n"},
	}
	for _, tt := range tests {
		d, err := Parse([]byte(tt.src))
		if err != nil {
			t.Fatal(err)
		}
		if err := d.Set(tt.key, tt.value); err != nil {
			t.Fatal(err)
		}
		if got := string(d.Bytes()); got != tt.want {
			t.Errorf("Set(%s) on %q = %q, want %q", tt.key, tt.src, got, tt.want)
		}
	}
}

func TestInsertDeleteList(t *testing.T) {
	tests := []struct {
		name string
		src  string
		edit func(d *Doc) error
		want string
	}{
		{"insert nested under existing", "log:\n  level: info\n\npolicy:\n  mode: file\n",
			func(d *Doc) error { return d.Set("log.format", "json") },
			"log:\n  level: info\n  format: json\n\npolicy:\n  mode: file\n"},
		{"insert new section next to related key", "tls_cert_path: \"\"\n# other\nlog:\n  level: info\n",
			func(d *Doc) error { return d.Set("tls.acme.email", "a@b.c") },
			"tls_cert_path: \"\"\ntls:\n  acme:\n    email: a@b.c\n# other\nlog:\n  level: info\n"},
		{"insert into empty flow map", "dns:\n  split: {}\n  x: 1\n",
			func(d *Doc) error {
				return d.SetListPath([]string{"dns", "split", "corp.example"}, []string{"10.0.0.1"})
			},
			"dns:\n  split:\n    corp.example:\n      - 10.0.0.1\n  x: 1\n"},
		{"set block list", "dns:\n  global:\n    - 1.1.1.1\n    - 1.0.0.1\n  x: 1\n",
			func(d *Doc) error { return d.SetList("dns.global", []string{"9.9.9.9"}) },
			"dns:\n  global:\n    - 9.9.9.9\n  x: 1\n"},
		{"set flow list", "derp:\n  paths: []\n",
			func(d *Doc) error { return d.SetList("derp.paths", []string{"/a.yaml", "/b.yaml"}) },
			"derp:\n  paths: [/a.yaml, /b.yaml]\n"},
		{"delete key with block", "a: 1\nb:\n  - x\n  - y\n# keep\nc: 3\n",
			func(d *Doc) error { return d.Delete("b") },
			"a: 1\n# keep\nc: 3\n"},
		{"delete missing", "a: 1\n", func(d *Doc) error { return d.Delete("zz") }, "a: 1\n"},
		{"empty doc", "", func(d *Doc) error { return d.Set("log.level", "info") }, "log:\n  level: info\n"},
		{"insert root key at end", "a: 1\n", func(d *Doc) error { return d.Set("zzz", "2") }, "a: 1\nzzz: 2\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := Parse([]byte(tt.src))
			if err != nil {
				t.Fatal(err)
			}
			if err := tt.edit(d); err != nil {
				t.Fatal(err)
			}
			if got := string(d.Bytes()); got != tt.want {
				t.Fatalf("got:\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}
}

func TestFlowEditRefused(t *testing.T) {
	d, _ := Parse([]byte("a: {b: 1}\n"))
	if err := d.Set("a.c", "2"); err != ErrFlowStyle {
		t.Fatalf("err = %v", err)
	}
}

// TestGolden applies a fixed edit script to every fixture and compares with
// testdata/hsconfig/<name>.edited.golden (go test -update to rewrite).
func TestGolden(t *testing.T) {
	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			src := load(t, name)
			d, err := Parse(src)
			if err != nil {
				t.Fatal(err)
			}
			steps := []func() error{
				func() error { return d.Set("log.level", "debug") },
				func() error { return d.Set("unix_socket_permission", "0770") },
				func() error { return d.SetList("dns.nameservers.global", []string{"9.9.9.9", "149.112.112.112"}) },
				func() error { return d.Set("dns.extra_records_path", "/etc/headscale/extra-records.json") },
				func() error { return d.Set("derp.server.ipv4", "203.0.113.7") },
				func() error { return d.Delete("disable_check_updates") },
			}
			for i, s := range steps {
				if err := s(); err != nil {
					t.Fatalf("step %d: %v", i, err)
				}
			}
			out := d.Bytes()
			for _, c := range comments(src) {
				if !strings.Contains(string(out), c) {
					t.Errorf("comment lost: %s", c)
				}
			}
			golden := filepath.Join("..", "..", "..", "testdata", "hsconfig", name+".edited.golden")
			if *update {
				if err := os.WriteFile(golden, out, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (run with -update)", err)
			}
			if !bytes.Equal(out, want) {
				t.Fatalf("output differs from %s", golden)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	for _, name := range fixtures {
		d, err := Parse(load(t, name))
		if err != nil {
			t.Fatal(err)
		}
		for _, is := range Validate(d) {
			if is.Severity == host.SevError {
				t.Errorf("%s: unexpected error %+v", name, is)
			}
		}
	}
	tests := []struct {
		name, src, want string
	}{
		{"enum", "log:\n  level: loud\n", "not one of"},
		{"duration", "node:\n  ephemeral:\n    inactivity_timeout: soon\n", "invalid duration"},
		{"bool", "dns:\n  magic_dns: maybe\n", "not a boolean"},
		{"socket perm unquoted", "unix_socket_permission: 0770\n", "quoted"},
		{"base domain", "server_url: https://hs.example.com\ndns:\n  base_domain: example.com\n", "must not contain"},
		{"records exclusive", "dns:\n  extra_records:\n    - name: a.b\n      type: A\n      value: 100.64.0.1\n  extra_records_path: /x.json\n", "cannot both"},
		{"prefix outside cgnat", "prefixes:\n  v4: 10.0.0.0/8\n", "within"},
		{"url", "server_url: hs.example.com\n", "URL"},
		{"oidc secret exclusive", "oidc:\n  client_secret: x\n  client_secret_path: /y\n", "mutually exclusive"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := Parse([]byte(tt.src))
			if err != nil {
				t.Fatal(err)
			}
			is := Validate(d)
			found := false
			for _, i := range is {
				if i.Severity == host.SevError && strings.Contains(i.Message, tt.want) && i.Line > 0 {
					found = true
				}
			}
			if !found {
				t.Fatalf("issues = %+v, want error containing %q", is, tt.want)
			}
		})
	}
}

func TestCatalogVersions(t *testing.T) {
	has := func(fs []Field, k string) bool {
		return slices.ContainsFunc(fs, func(x Field) bool { return x.Key == k })
	}
	c29, c30 := Catalog("0.29.6"), Catalog("v0.30.0")
	tests := []struct {
		key        string
		in29, in30 bool
	}{
		{"grpc_listen_addr", true, false},
		{"tls_cert_path", true, false},
		{"tls.cert_path", false, true},
		{"tls.acme.challenge_type", false, true},
		{"server_url", true, true},
		{"oidc.client_secret", true, true},
	}
	for _, tt := range tests {
		if has(c29, tt.key) != tt.in29 || has(c30, tt.key) != tt.in30 {
			t.Errorf("%s: in 0.29=%v in 0.30=%v", tt.key, has(c29, tt.key), has(c30, tt.key))
		}
	}
	dep := Deprecations("0.30")
	if dep["tls_cert_path"] != "tls.cert_path" || dep["tls_letsencrypt_hostname"] != "tls.acme.hostname" {
		t.Errorf("deprecations = %v", dep)
	}
	if r, ok := dep["grpc_listen_addr"]; !ok || r != "" {
		t.Error("grpc_listen_addr should be removed in 0.30")
	}
	if len(Deprecations("0.29")) != 0 {
		t.Error("no deprecations expected for 0.29")
	}
	for _, x := range All() {
		if x.Effect != host.EffectRestart {
			t.Errorf("%s effect %v", x.Key, x.Effect)
		}
		if x.Default != nil && x.Type != TypeList && x.Type != TypeMap {
			if err := CheckValue(x, toString(x.Default)); err != nil && toString(x.Default) != "" {
				t.Errorf("default of %s invalid: %v", x.Key, err)
			}
		}
	}
	if f, _ := Lookup("oidc.client_secret"); f.Type != TypeSecret {
		t.Error("client_secret must be Secret")
	}
	if f, _ := Lookup("unix_socket_permission"); f.Type != TypeOctal {
		t.Error("unix_socket_permission must be Octal")
	}
	for _, k := range []string{"server_url", "listen_addr", "database.type", "noise.private_key_path", "prefixes.v4"} {
		if f, _ := Lookup(k); f.Danger != host.DangerCritical {
			t.Errorf("%s should be critical", k)
		}
	}
	if f, _ := Lookup("log.level"); f.Danger != host.DangerConfirm {
		t.Error("log.level should be confirm")
	}
}

func toString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case int:
		return strconv.Itoa(x)
	}
	return ""
}

func TestDanger(t *testing.T) {
	base := "server_url: https://a.example.com\nlog:\n  level: info\n"
	tests := []struct {
		next string
		want host.Danger
	}{
		{base, host.DangerNone},
		{strings.Replace(base, "info", "debug", 1), host.DangerConfirm},
		{strings.Replace(base, "a.example", "b.example", 1), host.DangerCritical},
		{base + "database:\n  type: postgres\n", host.DangerCritical},
	}
	for _, tt := range tests {
		if got := Danger([]byte(base), []byte(tt.next)); got != tt.want {
			t.Errorf("Danger(%q) = %v, want %v", tt.next, got, tt.want)
		}
	}
}
