package derpmap

import (
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/host/doctree"
)

func errMsgs(is []host.Issue) string {
	var out []string
	for _, i := range is {
		if i.Severity == host.SevError {
			out = append(out, i.Message)
		}
	}
	return strings.Join(out, "\n")
}

func region(id int, code string, nodes ...Node) Region {
	return Region{RegionID: id, RegionCode: code, RegionName: strings.ToUpper(code), Nodes: nodes}
}

func node(name, hostName string) Node { return Node{Name: name, HostName: hostName} }

func TestValidate(t *testing.T) {
	cases := []struct {
		name string
		m    Map
		want []string // substrings of error messages; nil means valid
	}{
		{"ok", Map{Regions: []Region{region(900, "lab", node("900a", "derp.example.com")), region(901, "home", node("901a", "derp2.example.com"))}}, nil},
		{"id zero", Map{Regions: []Region{region(0, "lab", node("a", "d"))}}, []string{"1-999"}},
		{"id too big", Map{Regions: []Region{region(1000, "lab", node("a", "d"))}}, []string{"1-999"}},
		{"duplicate code", Map{Regions: []Region{region(900, "lab", node("a", "d")), region(901, "LAB", node("b", "e"))}}, []string{"code \"LAB\" is also used by region 900"}},
		{"duplicate id", Map{Regions: []Region{region(900, "a", node("a", "d")), region(900, "b", node("b", "e"))}}, []string{"defined twice"}},
		{"no nodes", Map{Regions: []Region{region(900, "lab")}}, []string{"at least one node"}},
		{"no hostname", Map{Regions: []Region{region(900, "lab", node("a", ""))}}, []string{"hostname"}},
		{"stun port", Map{Regions: []Region{region(900, "lab", Node{Name: "a", HostName: "d", STUNPort: 70000})}}, []string{"STUN port"}},
		{"stun disabled ok", Map{Regions: []Region{region(900, "lab", Node{Name: "a", HostName: "d", STUNPort: -1})}}, nil},
		{"derp port", Map{Regions: []Region{region(900, "lab", Node{Name: "a", HostName: "d", DERPPort: -5})}}, []string{"DERP port"}},
		{"node region mismatch", Map{Regions: []Region{region(900, "lab", Node{Name: "a", HostName: "d", RegionID: 901})}}, []string{"region ID 901"}},
		{"bad ipv4", Map{Regions: []Region{region(900, "lab", Node{Name: "a", HostName: "d", IPv4: "fd7a::1"})}}, []string{"IPv4"}},
		{"bad ipv6", Map{Regions: []Region{region(900, "lab", Node{Name: "a", HostName: "d", IPv6: "1.2.3.4"})}}, []string{"IPv6"}},
		{"none ip ok", Map{Regions: []Region{region(900, "lab", Node{Name: "a", HostName: "d", IPv4: "none", IPv6: "none"})}}, nil},
		{"duplicate node name", Map{Regions: []Region{region(900, "a", node("x", "d")), region(901, "b", node("x", "e"))}}, []string{"node name \"x\""}},
		{"no regions", Map{}, []string{"no regions"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := errMsgs(Validate(c.m))
			if c.want == nil && got != "" {
				t.Fatalf("unexpected errors:\n%s", got)
			}
			for _, w := range c.want {
				if !strings.Contains(got, w) {
					t.Errorf("want an error containing %q, got:\n%s", w, got)
				}
			}
		})
	}
}

func TestValidateRegionInput(t *testing.T) {
	m := Map{Regions: []Region{region(900, "lab", node("900a", "d"))}}
	if err := m.CheckNewRegion(region(900, "x", node("b", "e")), 0); err == nil {
		t.Error("adding an existing region ID should fail")
	}
	if err := m.CheckNewRegion(region(901, "lab", node("b", "e")), 0); err == nil {
		t.Error("adding a duplicate code should fail")
	}
	if err := m.CheckNewRegion(region(900, "lab", node("900a", "d")), 900); err != nil {
		t.Errorf("editing a region in place: %v", err)
	}
	if err := m.CheckNewRegion(region(901, "new", node("b", "e")), 0); err != nil {
		t.Errorf("new region: %v", err)
	}
}

func TestEmbeddedCollision(t *testing.T) {
	m := Map{Regions: []Region{region(999, "lab", node("a", "d"))}}
	is := CheckEmbedded(m, 999)
	if len(is) != 1 || is[0].Severity != host.SevWarn {
		t.Errorf("embedded region collision should warn: %+v", is)
	}
	if len(CheckEmbedded(m, 998)) != 0 {
		t.Error("no collision")
	}
}

const yamlSrc = `# Lab relays
regions:
  900:
    regionid: 900
    regioncode: lab # the basement
    regionname: Lab
    nodes:
      - name: 900a
        regionid: 900
        hostname: derp.lab.example.com
        stunport: 3478
        derpport: 443
`

const jsonSrc = `{
  "Regions": {
    "900": {
      "RegionID": 900,
      "RegionCode": "lab",
      "RegionName": "Lab",
      "Nodes": [
        {"Name": "900a", "RegionID": 900, "HostName": "derp.lab.example.com", "STUNPort": 3478, "DERPPort": 443}
      ]
    }
  }
}
`

const hujsonSrc = `// Lab relays
{
  "Regions": {
    // basement
    "900": {
      "RegionID": 900,
      "RegionCode": "lab",
      "RegionName": "Lab",
      "Nodes": [
        {"Name": "900a", "RegionID": 900, "HostName": "derp.lab.example.com"},
      ],
    },
  },
}
`

func TestFileRoundTrip(t *testing.T) {
	for _, c := range []struct {
		path, src, comment string
		format             doctree.Format
	}{
		{"/etc/headscale/derp.yaml", yamlSrc, "# the basement", doctree.YAML},
		{"/etc/headscale/derp.json", jsonSrc, "", doctree.JSON},
		{"/etc/headscale/derp.hujson", hujsonSrc, "// basement", doctree.HuJSON},
	} {
		t.Run(c.format.String(), func(t *testing.T) {
			f, err := ParseFile(c.path, []byte(c.src))
			if err != nil {
				t.Fatal(err)
			}
			if f.Format != c.format || len(f.Map.Regions) != 1 {
				t.Fatalf("parsed %v %+v", f.Format, f.Map)
			}
			r := f.Map.Regions[0]
			if r.RegionID != 900 || r.RegionCode != "lab" || len(r.Nodes) != 1 || r.Nodes[0].HostName != "derp.lab.example.com" {
				t.Fatalf("region %+v", r)
			}
			// Edit the region name, add a node, add a region.
			r.RegionName = "Basement lab"
			r.Nodes = append(r.Nodes, Node{Name: "900b", RegionID: 900, HostName: "derp2.lab.example.com", IPv4: "198.51.100.7", STUNPort: -1})
			if err := f.PutRegion(r); err != nil {
				t.Fatal(err)
			}
			if err := f.PutRegion(region(901, "home", Node{Name: "901a", HostName: "derp.home.example.com", InsecureForTests: true})); err != nil {
				t.Fatal(err)
			}
			b, err := f.Bytes()
			if err != nil {
				t.Fatal(err)
			}
			if e := errMsgs(Schema(b)); e != "" {
				t.Fatalf("schema: %s\n%s", e, b)
			}
			if c.comment != "" && !strings.Contains(string(b), c.comment) {
				t.Errorf("comment %q lost:\n%s", c.comment, b)
			}
			again, err := ParseFile(c.path, b)
			if err != nil {
				t.Fatalf("%v\n%s", err, b)
			}
			if again.Format != c.format || len(again.Map.Regions) != 2 {
				t.Fatalf("round trip: %v %+v", again.Format, again.Map)
			}
			g := again.Map.Regions[0]
			if g.RegionName != "Basement lab" || len(g.Nodes) != 2 || g.Nodes[1].IPv4 != "198.51.100.7" || g.Nodes[1].STUNPort != -1 {
				t.Errorf("edited region: %+v", g)
			}
			if h := again.Map.Regions[1]; h.RegionID != 901 || !h.Nodes[0].InsecureForTests {
				t.Errorf("new region: %+v", h)
			}
			// Remove a node and a region.
			g.Nodes = g.Nodes[:1]
			if err := again.PutRegion(g); err != nil {
				t.Fatal(err)
			}
			if err := again.DeleteRegion(901); err != nil {
				t.Fatal(err)
			}
			if err := again.DeleteRegion(5); err == nil {
				t.Error("deleting a missing region should fail")
			}
			b2, _ := again.Bytes()
			last, err := ParseFile(c.path, b2)
			if err != nil || len(last.Map.Regions) != 1 || len(last.Map.Regions[0].Nodes) != 1 {
				t.Fatalf("after deletes: %v\n%s", err, b2)
			}
		})
	}
}

// The YAML form must load the way headscale loads it (yaml.v3, lower-case keys).
func TestYAMLMatchesHeadscaleShape(t *testing.T) {
	f := NewFile("/etc/headscale/derp.yaml")
	if err := f.PutRegion(region(900, "lab", Node{Name: "900a", HostName: "d.example.com", STUNPort: 3478})); err != nil {
		t.Fatal(err)
	}
	b, _ := f.Bytes()
	var m struct {
		Regions map[int]struct {
			RegionID   int    `yaml:"regionid"`
			RegionCode string `yaml:"regioncode"`
			Nodes      []struct {
				HostName string `yaml:"hostname"`
				STUNPort int    `yaml:"stunport"`
			} `yaml:"nodes"`
		} `yaml:"regions"`
	}
	if err := yaml.Unmarshal(b, &m); err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	if r := m.Regions[900]; r.RegionID != 900 || r.RegionCode != "lab" || r.Nodes[0].STUNPort != 3478 {
		t.Errorf("headscale shape: %+v\n%s", m, b)
	}
	j := NewFile("/etc/headscale/derp.json")
	_ = j.PutRegion(region(900, "lab", node("900a", "d.example.com")))
	jb, _ := j.Bytes()
	var tm struct {
		Regions map[string]struct {
			RegionID int
			Nodes    []struct{ HostName string }
		}
	}
	if err := json.Unmarshal(jb, &tm); err != nil || tm.Regions["900"].Nodes[0].HostName != "d.example.com" {
		t.Errorf("tailscale JSON shape: %v\n%s", err, jb)
	}
}

func TestSchema(t *testing.T) {
	cases := []struct {
		src  string
		errs bool
	}{
		{"regions:\n  900:\n    regionid: 900\n    regioncode: lab\n    nodes:\n      - name: 900a\n        regionid: 900\n        hostname: derp.example.com\n", false},
		{`{"Regions":{"901":{"RegionID":901,"Nodes":[{"Name":"a","HostName":"d.example.com"}]}}}`, false},
		{"regions: {}\n", true},
		{"regions:\n  1:\n    regionid: 1\n    nodes:\n      - name: x\n", true},
		{`{"Regions":{"901":{"RegionID":902,"Nodes":[{"Name":"a","HostName":"d"}]}}}`, true},
		{"{", true},
		{"[1]", true},
	}
	for _, c := range cases {
		if got := errMsgs(Schema([]byte(c.src))) != ""; got != c.errs {
			t.Errorf("Schema(%q) errors=%v, want %v: %s", c.src, got, c.errs, errMsgs(Schema([]byte(c.src))))
		}
	}
}

func TestParseLive(t *testing.T) {
	tail := `{"Regions":{"1":{"RegionID":1,"RegionCode":"nyc","RegionName":"New York City","Nodes":[{"Name":"1f","RegionID":1,"HostName":"derp1f.tailscale.com"}]},
"999":{"RegionID":999,"RegionCode":"headscale","RegionName":"Headscale Embedded DERP","Nodes":[{"Name":"999a","RegionID":999,"HostName":"hs.example.com"}]}}}`
	m, err := ParseLive([]byte(tail), "application/json")
	if err != nil || len(m.Regions) != 2 || m.Regions[0].RegionCode != "nyc" || m.Regions[1].RegionID != 999 {
		t.Fatalf("tailcfg shape: %v %+v", err, m)
	}
	wrapped := `{"derp_map":` + tail + `,"total_regions":2}`
	if m, err := ParseLive([]byte(wrapped), ""); err != nil || len(m.Regions) != 2 {
		t.Fatalf("wrapped shape: %v %+v", err, m)
	}
	for _, bad := range []string{"<html>nope</html>", "not json", `{"foo": 1}`, `[]`} {
		if _, err := ParseLive([]byte(bad), "text/html"); err == nil {
			t.Errorf("ParseLive(%q) should fail", bad)
		}
	}
}

func TestPreferences(t *testing.T) {
	extras := map[uint64]host.NodeExtras{
		1: {PreferredDERP: 1, DERPLatency: map[string]float64{"1-v4": 10, "2-v4": 40}},
		2: {PreferredDERP: 1, DERPLatency: map[string]float64{"1-v4": 20, "1-v6": 15}},
		3: {PreferredDERP: 2, DERPLatency: map[string]float64{"2-v4": 30}},
		4: {},
	}
	p := Preferences(extras)
	if p[1].Machines != 2 || p[2].Machines != 1 {
		t.Fatalf("machines: %+v", p)
	}
	// Region 1 latency: best per machine (10 and 15) -> median 12.5.
	if p[1].Samples != 2 || p[1].MedianMS != 12.5 || p[1].BestMS != 10 {
		t.Errorf("region 1: %+v", p[1])
	}
	if p[2].Samples != 2 || p[2].MedianMS != 35 {
		t.Errorf("region 2: %+v", p[2])
	}
	if _, ok := p[0]; ok {
		t.Error("machines without a preference are not counted")
	}
}
