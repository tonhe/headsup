package hsconfig

import (
	"strings"
	"testing"
)

func TestSetMapList(t *testing.T) {
	src := "dns:\n  magic_dns: true\n  # records\n  extra_records_path: \"\"\n"
	d, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	rows := [][]string{{"a.example.com", "A", "100.64.0.1"}, {"b.example.com", "AAAA", "fd7a::1"}, {"123", "A"}}
	if err := d.SetMapList("dns.extra_records", []string{"name", "type", "value"}, rows); err != nil {
		t.Fatal(err)
	}
	got := string(d.Bytes())
	want := "dns:\n  magic_dns: true\n  # records\n  extra_records_path: \"\"\n  extra_records:\n    - name: a.example.com\n      type: A\n      value: 100.64.0.1\n" +
		"    - name: b.example.com\n      type: AAAA\n      value: fd7a::1\n    - name: \"123\"\n      type: A\n"
	if got != want {
		t.Fatalf("insert:\n%s\nwant\n%s", got, want)
	}
	if err := d.SetMapList("dns.extra_records", []string{"name", "type", "value"}, rows[:1]); err != nil {
		t.Fatal(err)
	}
	if n, _ := d.Node("dns.extra_records"); len(n.Content) != 1 {
		t.Fatalf("replace: %s", d.Bytes())
	}
	if err := d.SetMapList("dns.extra_records", nil, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(d.Bytes()), "extra_records: []") {
		t.Fatalf("empty: %s", d.Bytes())
	}
	f, _ := Parse([]byte("dns: {extra_records: []}\n"))
	if err := f.SetMapList("dns.extra_records", []string{"name"}, [][]string{{"x"}}); err != ErrFlowStyle {
		t.Errorf("flow parent: %v", err)
	}
}
