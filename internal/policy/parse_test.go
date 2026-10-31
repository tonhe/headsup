package policy

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tonhe/headsup/internal/api/fake"
)

func TestEquivalent(t *testing.T) {
	if !Equivalent("{\n  // c\n  \"a\": [1,],\n}\n", `{"a":[1]}`) {
		t.Error("comments, whitespace and trailing commas must not matter")
	}
	if Equivalent(`{"a":1}`, `{"a":2}`) || Equivalent("{", "}") {
		t.Error("different or broken policies are not equivalent")
	}
	if !Equivalent("{", "{") {
		t.Error("identical text is equivalent")
	}
}

func readFixture(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseAllSections(t *testing.T) {
	raw := readFixture(t, "full.hujson")
	d, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if d.Raw != string(raw) {
		t.Fatal("Raw must be preserved byte-for-byte")
	}
	if len(d.Groups) != 2 || d.Groups[0].Name != "group:admins" || !slices.Equal(d.Groups[1].Members, []string{"bob@"}) {
		t.Errorf("groups: %+v", d.Groups)
	}
	if d.Groups[0].Line != 6 {
		t.Errorf("group line = %d, want 6", d.Groups[0].Line)
	}
	if len(d.TagOwners) != 2 || d.TagOwners[1].Tag != "tag:ci" || len(d.TagOwners[1].Owners) != 2 {
		t.Errorf("tagOwners: %+v", d.TagOwners)
	}
	if len(d.Hosts) != 2 || d.Hosts[0].Addr != "100.64.0.10" {
		t.Errorf("hosts: %+v", d.Hosts)
	}
	if len(d.ACLs) != 2 || d.ACLs[1].Proto != "tcp" || d.ACLs[0].Line != 19 {
		t.Errorf("acls: %+v", d.ACLs)
	}
	if len(d.Grants) != 2 || len(d.Grants[1].App) == 0 || !slices.Equal(d.Grants[0].IP, []string{"tcp:22", "tcp:443"}) {
		t.Errorf("grants: %+v", d.Grants)
	}
	if len(d.SSH) != 1 || d.SSH[0].CheckPeriod != "12h" || len(d.SSH[0].Users) != 2 {
		t.Errorf("ssh: %+v", d.SSH)
	}
	aa := d.AutoApprovers
	if len(aa.Routes) != 2 || aa.Routes[1].Prefix != "192.168.0.0/16" || !slices.Equal(aa.ExitNode, []string{"tag:server"}) {
		t.Errorf("autoApprovers: %+v", aa)
	}
	if len(d.Tests) != 1 || d.Tests[0].Src != "bob@" || len(d.Tests[0].Deny) != 1 {
		t.Errorf("tests: %+v", d.Tests)
	}
	if !slices.Equal(d.Other, []string{"nodeAttrs"}) {
		t.Errorf("other: %v", d.Other)
	}
}

func TestRoundTripByteForByte(t *testing.T) {
	inputs := map[string][]byte{
		"full":      readFixture(t, "full.hujson"),
		"demo":      []byte(fake.DemoSeed().Policy),
		"crlf":      []byte("{\r\n  // c\r\n  \"acls\": [],\r\n}\r\n"),
		"tabs":      []byte("{\n\t\"groups\":\t{\"group:a\": [\"x@\",],},\n}"),
		"no-nl-eof": []byte(`{"acls":[]}`),
		"comments":  []byte("/* head */ { /* in */ } // tail\n"),
	}
	for name, in := range inputs {
		t.Run(name, func(t *testing.T) {
			out, err := RoundTrip(in)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(out, in) {
				t.Fatalf("round trip changed the bytes:\n--- in\n%q\n--- out\n%q", in, out)
			}
			if _, err := Parse(in); err != nil {
				t.Fatalf("parse: %v", err)
			}
		})
	}
}

func TestParseErrorsHavePositions(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		line, col int
		msg       string
	}{
		{"syntax", "{\n  \"acls\": [\n    {\"action\": \"accept\" \"src\": []},\n  ],\n}", 3, 25, "invalid character"},
		{"not-object", "\n  [1, 2]", 2, 3, "must be a JSON object"},
		{"groups-type", "{\n  \"groups\": [],\n}", 2, 13, `"groups" must be an object`},
		{"acl-entry-type", "{\"acls\": [\n  \"x\"]}", 2, 3, `each entry of "acls"`},
		{"members-type", "{\"groups\": {\n  \"group:a\": \"bob@\"}}", 2, 14, "expected a list"},
		{"acl-field-type", "{\"acls\": [\n  {\"src\": \"bob@\"}]}", 2, 3, `"src"`},
		{"acls-not-list", "{\"acls\": {}}", 1, 10, "must be a list"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.raw))
			var pe *ParseError
			if !errors.As(err, &pe) {
				t.Fatalf("want *ParseError, got %v", err)
			}
			if pe.Line != tt.line || pe.Col != tt.col {
				t.Errorf("position = %d:%d, want %d:%d (%v)", pe.Line, pe.Col, tt.line, tt.col, pe)
			}
			if !strings.Contains(pe.Error(), tt.msg) {
				t.Errorf("message %q does not mention %q", pe.Error(), tt.msg)
			}
		})
	}
	if (&ParseError{Msg: "x"}).Error() != "x" {
		t.Error("position-less error should be just the message")
	}
	if _, err := RoundTrip([]byte("{")); err == nil {
		t.Error("RoundTrip must reject broken input")
	}
}

func TestCheckFindsUndefinedNames(t *testing.T) {
	if err := Check(readFixture(t, "full.hujson")); err != nil {
		t.Fatalf("full fixture should pass: %v", err)
	}
	bad := "{\n  \"groups\": {\"group:a\": [\"x@\"]},\n  \"acls\": [\n    {\"action\": \"accept\", \"src\": [\"group:b\"], \"dst\": [\"*:*\"]},\n  ],\n}"
	var pe *ParseError
	if err := Check([]byte(bad)); !errors.As(err, &pe) || pe.Line != 4 || !strings.Contains(pe.Msg, "group:b") {
		t.Fatalf("want undefined group:b at line 4, got %v", err)
	}
	badDst := `{"acls": [{"action": "accept", "src": ["*"], "dst": ["tag:nope:22"]}]}`
	if err := Check([]byte(badDst)); err == nil || !strings.Contains(err.Error(), "tag:nope") {
		t.Fatalf("want undefined tag:nope, got %v", err)
	}
	badSSH := `{"ssh": [{"action": "accept", "src": ["group:x"], "dst": ["autogroup:self"], "users": ["root"]}]}`
	if err := Check([]byte(badSSH)); err == nil {
		t.Fatal("want undefined group in ssh")
	}
	if err := Check([]byte("{")); err == nil {
		t.Fatal("broken syntax must fail")
	}
}

func TestWhereUsed(t *testing.T) {
	d, err := Parse(readFixture(t, "full.hujson"))
	if err != nil {
		t.Fatal(err)
	}
	roles := func(refs []Ref) []string {
		var out []string
		for _, r := range refs {
			out = append(out, r.Section+"."+r.Role)
		}
		return out
	}
	got := roles(d.WhereUsed("tag:server"))
	want := []string{"tagOwners.definition", "grants.dst", "ssh.dst", "autoApprovers.approver", "autoApprovers.approver", "tests.dst"}
	if !slices.Equal(got, want) {
		t.Errorf("tag:server used in %v, want %v", got, want)
	}
	got = roles(d.WhereUsed("group:admins"))
	want = []string{"groups.definition", "tagOwners.owner", "tagOwners.owner", "acls.src", "ssh.src", "autoApprovers.approver"}
	if !slices.Equal(got, want) {
		t.Errorf("group:admins used in %v, want %v", got, want)
	}
	if got := roles(d.WhereUsed("nas")); !slices.Equal(got, []string{"hosts.definition", "acls.dst"}) {
		t.Errorf("nas used in %v", got)
	}
	if got := roles(d.WhereUsed("bob@")); !slices.Equal(got, []string{"groups.member", "tests.src"}) {
		t.Errorf("bob@ used in %v", got)
	}
	if got := roles(d.WhereUsed("autogroup:nonroot")); !slices.Equal(got, []string{"ssh.users"}) {
		t.Errorf("autogroup:nonroot used in %v", got)
	}
	if got := d.WhereUsed("autogroup:member"); len(got) != 1 || got[0].Section != "grants" || !strings.Contains(got[0].Summary, "+app") {
		t.Errorf("autogroup:member: %+v", got)
	}
	if (*Document)(nil).WhereUsed("x") != nil || d.WhereUsed("") != nil {
		t.Error("nil document or empty name must return nothing")
	}
	names := d.Names()
	if !slices.Equal(names["group"], []string{"group:admins", "group:devs"}) {
		t.Errorf("groups: %v", names["group"])
	}
	if !slices.Contains(names["tag"], "tag:ci") || !slices.Contains(names["user"], "alice@") || !slices.Contains(names["autogroup"], "autogroup:self") {
		t.Errorf("names: %v", names)
	}
	if !slices.Equal(names["host"], []string{"nas", "lab"}) {
		t.Errorf("hosts: %v", names["host"])
	}
}

func TestAliasOfAndSummaries(t *testing.T) {
	for in, want := range map[string]string{
		"tag:web:80,443": "tag:web", "[fd7a::1]:22": "fd7a::1", "*:*": "*", "nas": "nas",
		"autogroup:self:*": "autogroup:self", "10.0.0.0/24:*": "10.0.0.0/24",
	} {
		if got := AliasOf(in); got != want {
			t.Errorf("AliasOf(%q) = %q, want %q", in, got, want)
		}
	}
	if s := (ACL{Src: []string{"a"}, Dst: []string{"b:1"}}).Summary(); s != "accept a -> b:1" {
		t.Errorf("acl summary %q", s)
	}
	if s := (SSHRule{Src: []string{"a"}, Dst: []string{"b"}, Users: []string{"root"}}).Summary(); s != "ssh accept a -> b as root" {
		t.Errorf("ssh summary %q", s)
	}
	if s := (Test{Src: "a", Accept: []string{"b:1"}, Deny: []string{"c:2"}}).Summary(); s != "test a accept b:1 deny c:2" {
		t.Errorf("test summary %q", s)
	}
	if KindOf("*") != "" || KindOf("x@") != "user" || KindOf("nas") != "host" {
		t.Error("KindOf")
	}
}

// FuzzParse checks Parse never panics and that every input hujson accepts
// round-trips byte-for-byte.
func FuzzParse(f *testing.F) {
	f.Add(readFixture(f, "full.hujson"))
	f.Add([]byte(fake.DemoSeed().Policy))
	f.Add([]byte(`{"acls":[{"src":["*"],"dst":["*:*"]},],}`))
	f.Add([]byte("// x\n{\"groups\":{\"group:a\":[\"b@\",],},}"))
	f.Add([]byte(`{"autoApprovers":{"routes":{"10.0.0.0/8":["tag:a"]},"exitNode":["tag:b"]}}`))
	f.Add([]byte(`[`))
	f.Fuzz(func(t *testing.T, in []byte) {
		d, err := Parse(in)
		if err == nil && d.Raw != string(in) {
			t.Fatal("Raw not preserved")
		}
		out, err := RoundTrip(in)
		if err == nil && !bytes.Equal(out, in) {
			t.Fatalf("round trip changed bytes: %q -> %q", in, out)
		}
		_ = Check(in)
		if d != nil {
			_ = d.Names()
			_ = d.WhereUsed("tag:a")
		}
	})
}
