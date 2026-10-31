package hsconfig

import (
	"strings"
	"testing"
)

func TestSetMapReplacesFlowEmptyKeepingComments(t *testing.T) {
	src := "dns:\n  nameservers:\n    # split resolvers\n    split: {}\n      # foo.bar.com:\n      #   - 1.1.1.1\n  search_domains: []\n"
	d, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	err = d.SetMap("dns.nameservers.split", []MapEntry{
		{Key: "corp.example.com", List: true, Values: []string{"10.0.0.53", "10.0.0.54"}},
		{Key: "lab.example.com", List: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := string(d.Bytes())
	for _, want := range []string{"# split resolvers", "# foo.bar.com:", "corp.example.com:\n        - 10.0.0.53\n        - 10.0.0.54", "lab.example.com: []", "search_domains: []"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	m, ok := d.Map("dns.nameservers.split")
	if !ok || len(m) != 2 || m[0].Key != "corp.example.com" || len(m[0].Values) != 2 || !m[1].List {
		t.Fatalf("Map = %+v %v", m, ok)
	}
}

func TestSetMapScalarsAndReplaceBlock(t *testing.T) {
	src := "oidc:\n  issuer: https://idp.example.com\n  extra_params:\n    domain_hint: example.com\n    prompt: login\n  scope: [openid]\n"
	d, _ := Parse([]byte(src))
	if err := d.SetMap("oidc.extra_params", []MapEntry{{Key: "domain_hint", Value: "corp.example.com"}, {Key: "odd", Value: "a: b"}}); err != nil {
		t.Fatal(err)
	}
	got := string(d.Bytes())
	if !strings.Contains(got, "    domain_hint: corp.example.com\n    odd: \"a: b\"\n  scope: [openid]") {
		t.Fatalf("unexpected:\n%s", got)
	}
	if strings.Contains(got, "prompt") {
		t.Error("old entries must be replaced")
	}
	if err := d.SetMap("oidc.extra_params", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(d.Bytes()), "extra_params: {}") {
		t.Errorf("empty map:\n%s", d.Bytes())
	}
	if _, ok := d.Map("oidc.issuer"); ok {
		t.Error("a scalar is not a map")
	}
	if _, ok := d.Map("nope"); ok {
		t.Error("missing key")
	}
}

func TestSetMapInsertsMissing(t *testing.T) {
	d, _ := Parse([]byte("oidc:\n  issuer: https://idp.example.com\n"))
	if err := d.SetMap("oidc.extra_params", []MapEntry{{Key: "k", Value: "v"}}); err != nil {
		t.Fatal(err)
	}
	if got := string(d.Bytes()); !strings.Contains(got, "  extra_params:\n    k: v") {
		t.Fatalf("insert:\n%s", got)
	}
	d2, _ := Parse([]byte("a: {x: 1}\n"))
	if err := d2.SetMap("a.x", nil); err != ErrFlowStyle {
		t.Errorf("flow parent: %v", err)
	}
	d3, _ := Parse([]byte("k:\n"))
	if err := d3.SetMap("k", nil); err != nil {
		t.Fatal(err)
	}
	if m, ok := d3.Map("k"); !ok || len(m) != 0 {
		t.Errorf("null map %v %v", m, ok)
	}
}
