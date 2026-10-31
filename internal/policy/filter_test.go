package policy

import (
	"errors"
	"testing"
)

func TestParseFilter(t *testing.T) {
	rules, err := ParseFilter(readFixture(t, "filter.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 2 {
		t.Fatalf("got %d rules", len(rules))
	}
	r := rules[1]
	if r.SrcIPs[0] != "100.64.0.2/32" || r.DstPorts[0].Ports != "22" || r.DstPorts[1].Ports != "80-443" {
		t.Errorf("rule: %+v", r)
	}
	if rules[0].DstPorts[0].Ports != "*" || rules[0].ProtoString() != "tcp,udp,icmp" {
		t.Errorf("rule 0: %+v %s", rules[0], rules[0].ProtoString())
	}
	if r.ProtoString() != "tcp,99" {
		t.Errorf("protos %q", r.ProtoString())
	}

	wrapped, err := ParseFilter([]byte(`{"filter": [{"srcips": ["*"], "CapGrant": [{}]}]}`))
	if err != nil || len(wrapped) != 1 || wrapped[0].SrcIPs[0] != "*" || !wrapped[0].CapGrant {
		t.Fatalf("wrapped: %+v %v", wrapped, err)
	}
	if rs, err := ParseFilter([]byte(`{"rules": []}`)); err != nil || len(rs) != 0 {
		t.Fatalf("empty rules: %v %v", rs, err)
	}

	for _, bad := range []string{`<html>`, `{"x": 1}`, `[{"SrcIPs": 5}]`, `[{"DstPorts": "x"}]`, `[{"IPProto": ["tcp"]}]`, `"str"`} {
		if _, err := ParseFilter([]byte(bad)); !errors.Is(err, ErrFilterShape) {
			t.Errorf("%s: want ErrFilterShape, got %v", bad, err)
		}
	}
}
