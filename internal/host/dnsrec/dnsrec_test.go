package dnsrec

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/host/doctree"
	"github.com/tonhe/headsup/internal/host/hsconfig"
	"gopkg.in/yaml.v3"
)

func errs(is []host.Issue) []string {
	var out []string
	for _, i := range is {
		if i.Severity == host.SevError {
			out = append(out, i.Message)
		}
	}
	return out
}

func TestRecordValidate(t *testing.T) {
	cases := []struct {
		name string
		r    Record
		want string // substring of the error, "" for valid
	}{
		{"a ok", Record{"grafana.tail.example.com", "A", "100.64.0.5"}, ""},
		{"aaaa ok", Record{"nas.tail.example.com", "AAAA", "fd7a:115c:a1e0::3"}, ""},
		{"trailing dot ok", Record{"nas.tail.example.com.", "A", "100.64.0.3"}, ""},
		{"type CNAME", Record{"a.example.com", "CNAME", "b.example.com"}, "type must be A or AAAA"},
		{"type lowercase", Record{"a.example.com", "a", "100.64.0.1"}, "upper case"},
		{"type empty", Record{"a.example.com", "", "100.64.0.1"}, "type must be A or AAAA"},
		{"A with v6", Record{"a.example.com", "A", "fd7a::1"}, "IPv4"},
		{"AAAA with v4", Record{"a.example.com", "AAAA", "100.64.0.1"}, "IPv6"},
		{"AAAA with mapped v4", Record{"a.example.com", "AAAA", "::ffff:100.64.0.1"}, "IPv6"},
		{"value not ip", Record{"a.example.com", "A", "example.com"}, "not an IP"},
		{"value zone", Record{"a.example.com", "AAAA", "fe80::1%eth0"}, "zone"},
		{"name empty", Record{"", "A", "100.64.0.1"}, "name is required"},
		{"name space", Record{"bad name.example.com", "A", "100.64.0.1"}, "not a valid DNS name"},
		{"name long label", Record{strings.Repeat("a", 64) + ".example.com", "A", "100.64.0.1"}, "63"},
		{"name empty label", Record{"a..example.com", "A", "100.64.0.1"}, "empty label"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.r.Validate()
			switch {
			case c.want == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case c.want != "" && err == nil:
				t.Fatalf("want error containing %q", c.want)
			case c.want != "" && !strings.Contains(err.Error(), c.want):
				t.Fatalf("error %q does not contain %q", err, c.want)
			}
		})
	}
}

func TestValidateRecordsDuplicates(t *testing.T) {
	is := ValidateRecords([]Record{
		{"a.example.com", "A", "100.64.0.1"},
		{"a.example.com", "A", "100.64.0.1"},
		{"a.example.com", "AAAA", "fd7a::1"},
		{"b", "MX", "x"},
	})
	if len(errs(is)) != 1 {
		t.Fatalf("want one error, got %+v", is)
	}
	warned := false
	for _, i := range is {
		if i.Severity == host.SevWarn && strings.Contains(i.Message, "duplicate") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("duplicate record should warn: %+v", is)
	}
}

func TestBaseDomainDiffersFromServerURL(t *testing.T) {
	cases := []struct {
		base, server string
		bad          bool
	}{
		{"tail.example.com", "https://hs.example.com", false},
		{"example.com", "https://hs.example.com", true},        // server under the base domain
		{"hs.example.com", "https://hs.example.com:443", true}, // same host
		{"HS.Example.com.", "https://hs.example.com", true},    // case and trailing dot
		{"tail.example.com", "", false},
		{"", "https://hs.example.com", false},
		{"ample.com", "https://hs.example.com", false}, // suffix, but not a label boundary
	}
	for _, c := range cases {
		err := CheckBaseDomain(c.base, c.server)
		if (err != nil) != c.bad {
			t.Errorf("CheckBaseDomain(%q, %q) = %v, want bad=%v", c.base, c.server, err, c.bad)
		}
	}
}

func TestExclusive(t *testing.T) {
	if CheckExclusive(true, "/var/lib/headscale/extra.json") == nil {
		t.Error("inline records and a records path must be exclusive")
	}
	if CheckExclusive(true, "") != nil || CheckExclusive(false, "/x.json") != nil || CheckExclusive(false, "") != nil {
		t.Error("one or none is fine")
	}
}

func TestConfigValidate(t *testing.T) {
	good := Config{MagicDNS: true, BaseDomain: "tail.example.com", OverrideLocalDNS: true,
		Global: []string{"1.1.1.1", "2606:4700:4700::1111", "https://dns.nextdns.io/abc123"},
		Split:  map[string][]string{"corp.example.com": {"10.0.0.53"}},
		Search: []string{"corp.example.com"}, ExtraRecordsPath: "/var/lib/headscale/extra.json"}
	if e := errs(good.Validate("https://hs.example.com")); len(e) != 0 {
		t.Fatalf("good config: %v", e)
	}
	bad := good
	bad.BaseDomain = "example.com"
	bad.Global = []string{"1.1.1.1", "dns.google"}
	bad.Split = map[string][]string{"corp.example.com": {}, "bad domain": {"10.0.0.53"}, "lab.example.com": {"nope"}}
	bad.Search = []string{"ok.example.com", "a b"}
	bad.ExtraRecords = []Record{{"x.example.com", "A", "fd7a::1"}}
	got := strings.Join(errs(bad.Validate("https://hs.example.com")), "\n")
	for _, w := range []string{"base_domain", "dns.google", "corp.example.com", "bad domain", "nope", "a b", "cannot both be set", "IPv4"} {
		if !strings.Contains(got, w) {
			t.Errorf("missing error mentioning %q in:\n%s", w, got)
		}
	}
	noBase := Config{MagicDNS: true}
	if len(errs(noBase.Validate(""))) == 0 {
		t.Error("MagicDNS without a base domain is an error")
	}
	noGlobal := Config{OverrideLocalDNS: true}
	if len(errs(noGlobal.Validate(""))) == 0 {
		t.Error("override_local_dns without global nameservers is an error")
	}
}

const cfg = `server_url: https://hs.example.com
dns:
  magic_dns: true
  base_domain: tail.example.com
  override_local_dns: false
  nameservers:
    global:
      - 1.1.1.1
    split:
      corp.example.com:
        - 10.0.0.53
  search_domains: [corp.example.com]
  # inline records
  extra_records:
    - name: grafana.tail.example.com
      type: A
      value: 100.64.0.5
    - {name: "nas.tail.example.com", type: "AAAA", value: "fd7a::3"}
`

func TestFromDocAndInline(t *testing.T) {
	d, err := hsconfig.Parse([]byte(cfg))
	if err != nil {
		t.Fatal(err)
	}
	c := FromDoc(d)
	if !c.MagicDNS || c.OverrideLocalDNS || c.BaseDomain != "tail.example.com" || len(c.Global) != 1 ||
		len(c.Split["corp.example.com"]) != 1 || len(c.Search) != 1 || len(c.ExtraRecords) != 2 || c.ExtraRecords[1].Type != "AAAA" {
		t.Fatalf("FromDoc: %+v", c)
	}
	if !c.HasInline() {
		t.Error("HasInline")
	}
	if err := SetInline(d, append(c.ExtraRecords, Record{"new.tail.example.com", "A", "100.64.0.9"})); err != nil {
		t.Fatal(err)
	}
	out := string(d.Bytes())
	if !strings.Contains(out, "# inline records") || !strings.Contains(out, "- name: new.tail.example.com") {
		t.Errorf("inline write lost structure:\n%s", out)
	}
	d2, err := hsconfig.Parse(d.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if rs := FromDoc(d2).ExtraRecords; len(rs) != 3 || rs[2].Value != "100.64.0.9" {
		t.Errorf("round trip: %+v", rs)
	}
	if err := SetInline(d2, nil); err != nil {
		t.Fatal(err)
	}
	if FromDoc(d2).HasInline() {
		t.Error("clearing inline records")
	}
}

func TestMoveInline(t *testing.T) {
	d, _ := hsconfig.Parse([]byte(cfg))
	f, err := MoveInline(d, "/var/lib/headscale/extra-records.json")
	if err != nil {
		t.Fatal(err)
	}
	c := FromDoc(d)
	if c.HasInline() || c.ExtraRecordsPath != "/var/lib/headscale/extra-records.json" {
		t.Fatalf("config after move: %+v", c)
	}
	if e := errs(c.Validate("https://hs.example.com")); len(e) != 0 {
		t.Errorf("config after move should validate: %v", e)
	}
	b, _ := f.Bytes()
	var recs []Record
	if err := json.Unmarshal(b, &recs); err != nil || len(recs) != 2 {
		t.Fatalf("moved file: %v %s", err, b)
	}
	if !strings.Contains(string(d.Bytes()), "# inline records") {
		t.Error("comments around the moved key should stay")
	}
	if _, err := MoveInline(d, "/var/lib/headscale/again.json"); err == nil {
		t.Error("moving when a path is already set should fail")
	}
	d3, _ := hsconfig.Parse([]byte(cfg))
	if _, err := MoveInline(d3, "relative.json"); err == nil {
		t.Error("relative path should be refused")
	}
	if _, err := MoveInline(d3, "/x/records.txt"); err == nil {
		t.Error("unknown extension should be refused")
	}
}

func TestFileKeepsFormat(t *testing.T) {
	jsonSrc := "[\n  {\"name\": \"a.example.com\", \"type\": \"A\", \"value\": \"100.64.0.1\"}\n]\n"
	yamlSrc := "# records\n- name: a.example.com\n  type: A\n  value: 100.64.0.1\n"
	hujsonSrc := "// records\n[\n  {\"name\": \"a.example.com\", \"type\": \"A\", \"value\": \"100.64.0.1\"}, // first\n]\n"
	for _, c := range []struct {
		path, src string
		format    doctree.Format
	}{
		{"/r.json", jsonSrc, doctree.JSON},
		{"/r.yaml", yamlSrc, doctree.YAML},
		{"/r.hujson", hujsonSrc, doctree.HuJSON},
		{"/r.json", hujsonSrc, doctree.HuJSON}, // comments in a .json file are kept
	} {
		t.Run(c.path+"/"+c.format.String(), func(t *testing.T) {
			f, err := ParseFile(c.path, []byte(c.src))
			if err != nil {
				t.Fatal(err)
			}
			if f.Format != c.format || len(f.Records) != 1 {
				t.Fatalf("format %v records %d", f.Format, len(f.Records))
			}
			if err := f.Add(Record{"b.example.com", "AAAA", "fd7a::2"}); err != nil {
				t.Fatal(err)
			}
			if err := f.Set(0, Record{"a.example.com", "A", "100.64.0.9"}); err != nil {
				t.Fatal(err)
			}
			b, err := f.Bytes()
			if err != nil {
				t.Fatal(err)
			}
			if e := errs(Schema(b)); len(e) != 0 {
				t.Fatalf("schema: %v\n%s", e, b)
			}
			again, err := ParseFile(c.path, b)
			if err != nil {
				t.Fatal(err)
			}
			if len(again.Records) != 2 || again.Records[0].Value != "100.64.0.9" || again.Records[1].Name != "b.example.com" {
				t.Fatalf("round trip %+v\n%s", again.Records, b)
			}
			if again.Format != c.format {
				t.Errorf("format changed to %v", again.Format)
			}
			if strings.HasPrefix(c.src, "#") && !strings.Contains(string(b), "# records") ||
				strings.HasPrefix(c.src, "//") && !strings.Contains(string(b), "// first") {
				t.Errorf("comments lost:\n%s", b)
			}
			if err := again.Delete(0); err != nil || len(again.Records) != 1 {
				t.Fatal("delete")
			}
			if err := again.Delete(5); err == nil {
				t.Error("delete out of range")
			}
			if err := again.Set(5, Record{}); err == nil {
				t.Error("set out of range")
			}
		})
	}
}

func TestNewAndEmptyFiles(t *testing.T) {
	f, err := NewFile("/var/lib/headscale/extra.yaml", []Record{{"a.example.com", "A", "100.64.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := f.Bytes()
	var recs []Record
	if err := yaml.Unmarshal(b, &recs); err != nil || len(recs) != 1 {
		t.Fatalf("new yaml file: %v\n%s", err, b)
	}
	e, err := ParseFile("/r.json", []byte("  \n"))
	if err != nil || len(e.Records) != 0 || e.Format != doctree.JSON {
		t.Fatalf("empty file: %v %+v", err, e)
	}
	if _, err := ParseFile("/r.json", []byte(`{"name": "x"}`)); err == nil {
		t.Error("an object is not a records list")
	}
	if _, err := ParseFile("/r.yaml", []byte("a: [")); err == nil {
		t.Error("broken yaml")
	}
}

func TestSchema(t *testing.T) {
	cases := []struct {
		src  string
		errs int
	}{
		{`[{"name":"a.ts.net","type":"A","value":"100.64.0.1"},{"name":"b","type":"AAAA","value":"fd7a::1"}]`, 0},
		{"- name: a\n  type: A\n  value: 100.64.0.1\n", 0},
		{`[{"name":"a","type":"CNAME","value":"b"}]`, 1},
		{`[{"name":"a","type":"A","value":"fd7a::1"}]`, 1},
		{`[{"type":"A","value":"100.64.0.1"}]`, 1},
		{"", 0},
		{"{", 1},
		{`{"a": 1}`, 1},
	}
	for _, c := range cases {
		if got := len(errs(Schema([]byte(c.src)))); got != c.errs {
			t.Errorf("Schema(%q) errors = %d, want %d", c.src, got, c.errs)
		}
	}
}
