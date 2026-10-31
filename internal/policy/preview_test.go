package policy

import (
	"net/netip"
	"reflect"
	"strings"
	"testing"
)

func pm(id uint64, name, user string, ip string, tags ...string) Machine {
	return Machine{ID: id, Name: name, User: user, Tags: tags, IPs: []netip.Addr{netip.MustParseAddr(ip)}}
}

var previewMachines = []Machine{
	pm(1, "alice-laptop", "alice", "100.64.0.1"),
	pm(2, "alice-phone", "alice", "100.64.0.2"),
	pm(3, "bob-laptop", "bob", "100.64.0.3"),
	pm(4, "web-1", "ops", "100.64.0.4", "tag:web"),
	pm(5, "db-1", "ops", "100.64.0.5", "tag:db"),
	pm(6, "nas", "carol", "100.64.0.10"),
	pm(7, "ci-1", "ops", "100.64.0.7", "tag:ci"),
}

const previewPolicy = `{
  "groups": {"group:eng": ["alice@", "bob@"], "group:ops": ["carol@"]},
  "tagOwners": {"tag:web": ["group:ops"], "tag:db": ["group:ops"], "tag:ci": ["group:ops"], "tag:unused": []},
  "hosts": {"nas": "100.64.0.10", "lab": "10.0.0.0/24", "printer": "192.168.1.9"},
  "acls": [
    {"action": "accept", "src": ["group:eng"], "dst": ["tag:web:80,443"]},
    {"action": "accept", "proto": "tcp", "src": ["alice@"], "dst": ["nas:445", "lab:*"]},
    {"action": "accept", "src": ["tag:web"], "dst": ["tag:db:5432"]},
    {"action": "accept", "src": ["autogroup:member"], "dst": ["autogroup:self:*"]},
    {"action": "accept", "src": ["group:ops"], "dst": ["*:*"]},
    {"action": "accept", "src": ["100.64.0.7"], "dst": ["printer:9100"]},
    {"action": "drop", "src": ["*"], "dst": ["*:*"]}
  ],
  "grants": [
    {"src": ["bob@"], "dst": ["tag:db"], "ip": ["tcp:5432", "icmp:*"]},
    {"src": ["autogroup:tagged"], "dst": ["autogroup:internet"], "ip": ["*"]},
    {"src": ["group:eng"], "dst": ["autogroup:member"], "ip": ["22"], "via": ["tag:router"]},
    {"src": ["*"], "dst": ["*"], "app": {"tailscale.com/cap/x": [{}]}}
  ]
}`

// summary renders a preview as "dest ports" lines.
func summary(p Preview) []string {
	var out []string
	for _, a := range p.Access {
		out = append(out, a.Kind+" "+a.Dest+" "+strings.Join(a.Ports, ","))
	}
	return out
}

func TestPreview(t *testing.T) {
	d, err := Parse([]byte(previewPolicy))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		src   Subject
		from  int
		want  []string
		notes []string
	}{
		{
			name: "user via group, self, and host aliases",
			src:  Subject{Kind: SubjectUser, Name: "alice"},
			from: 2,
			want: []string{
				"machine alice-laptop *",
				"machine alice-phone *",
				"machine bob-laptop 22",
				"machine nas 22,tcp:445",
				"machine web-1 443,80",
				"address lab tcp:*",
			},
			notes: []string{"via routing (tag:router)"},
		},
		{
			name: "user with a grant and protocols",
			src:  Subject{Kind: SubjectUser, Name: "bob@"},
			from: 1,
			want: []string{
				"machine alice-laptop 22",
				"machine alice-phone 22",
				"machine bob-laptop *",
				"machine db-1 icmp:*,tcp:5432",
				"machine nas 22",
				"machine web-1 443,80",
			},
		},
		{
			name: "tag source",
			src:  Subject{Kind: SubjectTag, Name: "tag:web"},
			from: 1,
			want: []string{"machine db-1 5432", "internet autogroup:internet *"},
		},
		{
			name: "tagged machine by IP",
			src:  Subject{Kind: SubjectMachine, ID: 7, Name: "ci-1"},
			from: 1,
			want: []string{"address printer 9100", "internet autogroup:internet *"},
		},
		{
			name: "group with star",
			src:  Subject{Kind: SubjectUser, Name: "carol"},
			from: 1,
			want: []string{
				"machine alice-laptop *", "machine alice-phone *", "machine bob-laptop *", "machine ci-1 *",
				"machine db-1 *", "machine nas *", "machine web-1 *", "any * *",
			},
		},
		{
			name: "untagged machine source uses its owner",
			src:  Subject{Kind: SubjectMachine, ID: 3, Name: "bob-laptop"},
			from: 1,
			want: []string{
				"machine alice-laptop 22",
				"machine alice-phone 22",
				"machine bob-laptop *",
				"machine db-1 icmp:*,tcp:5432",
				"machine nas 22",
				"machine web-1 443,80",
			},
		},
		{
			name: "unknown user reaches nothing but self",
			src:  Subject{Kind: SubjectUser, Name: "dave"},
			want: nil,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := d.Preview(previewMachines, c.src)
			if len(p.From) != c.from {
				t.Errorf("from = %d machines, want %d", len(p.From), c.from)
			}
			if got := summary(p); !reflect.DeepEqual(got, c.want) {
				t.Errorf("access:\n got %q\nwant %q", got, c.want)
			}
			all := strings.Join(p.Notes, "\n")
			for _, n := range append(c.notes, `action "drop" is not evaluated`, "application capability grant") {
				if !strings.Contains(all, n) {
					t.Errorf("notes missing %q:\n%s", n, all)
				}
			}
		})
	}
}

func TestPreviewDetails(t *testing.T) {
	d, err := Parse([]byte(`{
  "acls": [
    {"action": "accept", "src": ["autogroup:self", "autogroup:admin"], "dst": ["*:*"]},
    {"action": "accept", "src": ["alice"], "dst": ["[fd7a::1]:22", "fd7a::/64:80", "bob:443", "autogroup:nonroot:22", "100.64.0.1:8080"]}
  ]
}`))
	if err != nil {
		t.Fatal(err)
	}
	ms := []Machine{pm(1, "a", "alice", "100.64.0.1"), pm(2, "b", "bob", "100.64.0.2"), {ID: 3, Name: "v6", User: "carol", IPs: []netip.Addr{netip.MustParseAddr("fd7a::1")}}}
	p := d.Preview(ms, Subject{Kind: SubjectUser, Name: "alice"})
	want := []string{"machine a 8080", "machine b 443", "machine v6 22,80", "address fd7a::/64 80"}
	if got := summary(p); !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	notes := strings.Join(p.Notes, "\n")
	for _, n := range []string{"autogroup:self is only valid as a destination", "autogroup:admin is not evaluated", "destination autogroup:nonroot"} {
		if !strings.Contains(notes, n) {
			t.Errorf("missing note %q in %s", n, notes)
		}
	}
	if p.Access[0].Machine == nil || p.Access[0].ID != 1 || !reflect.DeepEqual(p.Access[0].Lines, []int{4}) {
		t.Errorf("machine access = %+v", p.Access[0])
	}
	var nilDoc *Document
	if got := nilDoc.Preview(ms, Subject{}); len(got.Access) != 0 {
		t.Error("nil document reaches nothing")
	}
}

func TestSubjects(t *testing.T) {
	d, err := Parse([]byte(previewPolicy))
	if err != nil {
		t.Fatal(err)
	}
	var labels []string
	for _, s := range d.Subjects(previewMachines[:4]) {
		labels = append(labels, s.Kind.String()+" "+s.Label())
	}
	want := []string{"user alice@", "user bob@", "user carol@",
		"tag tag:ci", "tag tag:db", "tag tag:unused", "tag tag:web",
		"machine alice-laptop", "machine alice-phone", "machine bob-laptop", "machine web-1"}
	if !reflect.DeepEqual(labels, want) {
		t.Errorf("got %q\nwant %q", labels, want)
	}
	if PreviewNote == "" || !strings.Contains(PreviewNote, "Best-effort") {
		t.Error("preview must be labelled best-effort")
	}
}
