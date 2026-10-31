package doctree

import (
	"strings"
	"testing"

	"github.com/tailscale/hujson"
	"gopkg.in/yaml.v3"
)

func mustParse(t *testing.T, s string, f Format) *Doc {
	t.Helper()
	d, err := Parse([]byte(s), f)
	if err != nil {
		t.Fatalf("parse %s: %v", f, err)
	}
	return d
}

func out(t *testing.T, d *Doc) string {
	t.Helper()
	b, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestFormatFor(t *testing.T) {
	cases := map[string]Format{"a.yaml": YAML, "a.YML": YAML, "a.hujson": HuJSON, "a.json": JSON, "a": JSON}
	for p, want := range cases {
		if got := FormatFor(p); got != want {
			t.Errorf("FormatFor(%q) = %v, want %v", p, got, want)
		}
	}
	if Sniff([]byte(`[{"a":1}]`)) != JSON || Sniff([]byte("// c\n[1,]")) != HuJSON || Sniff([]byte("- a: 1\n")) != YAML {
		t.Error("Sniff misclassified")
	}
	if YAML.String() != "yaml" {
		t.Error("String")
	}
}

func TestJSONAppendKeepsLayout(t *testing.T) {
	src := "[\n  {\"name\": \"a\", \"type\": \"A\", \"value\": \"1.2.3.4\"},\n  {\"name\": \"b\", \"type\": \"A\", \"value\": \"1.2.3.5\"}\n]\n"
	d := mustParse(t, src, JSON)
	if err := d.Root().Append(Obj{{"name", "c"}, {"type", "AAAA"}, {"value", "fd7a::1"}}); err != nil {
		t.Fatal(err)
	}
	got := out(t, d)
	want := "[\n  {\"name\": \"a\", \"type\": \"A\", \"value\": \"1.2.3.4\"},\n  {\"name\": \"b\", \"type\": \"A\", \"value\": \"1.2.3.5\"},\n  {\"name\": \"c\", \"type\": \"AAAA\", \"value\": \"fd7a::1\"}\n]\n"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	if err := d.Root().RemoveAt(2); err != nil {
		t.Fatal(err)
	}
	if out(t, d) != src {
		t.Errorf("remove did not restore:\n%s", out(t, d))
	}
}

func TestHuJSONCommentsAndTrailingComma(t *testing.T) {
	src := "// records\n[\n  // grafana box\n  {\"name\": \"a\", \"type\": \"A\", \"value\": \"1.2.3.4\"},\n  {\"name\": \"b\", \"type\": \"A\", \"value\": \"1.2.3.5\"},\n]\n"
	d := mustParse(t, src, HuJSON)
	r := d.Root()
	if err := r.At(1).Set("value", "9.9.9.9"); err != nil {
		t.Fatal(err)
	}
	if err := r.Append(Obj{{"name", "c"}, {"type", "A"}, {"value", "1.1.1.1"}}); err != nil {
		t.Fatal(err)
	}
	got := out(t, d)
	for _, w := range []string{"// records", "// grafana box", `"value": "9.9.9.9"`, `{"name": "c", "type": "A", "value": "1.1.1.1"},` + "\n]"} {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in\n%s", w, got)
		}
	}
	if _, err := hujson.Parse([]byte(got)); err != nil {
		t.Fatalf("output does not parse: %v\n%s", err, got)
	}
	if err := r.RemoveAt(0); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out(t, d), "grafana box") {
		t.Error("removing an element should drop its leading comment")
	}
}

func TestJSONNoTrailingCommaAfterRemoveLast(t *testing.T) {
	d := mustParse(t, `[1, 2, 3]`, JSON)
	if err := d.Root().RemoveAt(2); err != nil {
		t.Fatal(err)
	}
	if got := out(t, d); got != `[1, 2]` {
		t.Errorf("got %q", got)
	}
	if err := d.Root().Append(4); err != nil {
		t.Fatal(err)
	}
	if got := out(t, d); got != `[1, 2, 4]` {
		t.Errorf("compact append: got %q", got)
	}
}

func TestEmptyDocs(t *testing.T) {
	d := Empty(JSON, Arr{})
	if err := d.Root().Append(Obj{{"name", "a"}, {"type", "A"}, {"value", "1.2.3.4"}}); err != nil {
		t.Fatal(err)
	}
	if got := out(t, d); got != "[\n  {\"name\": \"a\", \"type\": \"A\", \"value\": \"1.2.3.4\"}\n]\n" {
		t.Errorf("json: %q", got)
	}
	o := Empty(JSON, Obj{})
	if err := o.Root().Set("Regions", Obj{{"900", Obj{{"RegionID", 900}, {"RegionCode", "lab"}, {"Nodes", Arr{Obj{{"Name", "900a"}, {"HostName", "d"}}}}}}}); err != nil {
		t.Fatal(err)
	}
	got := out(t, o)
	if !strings.Contains(got, "\n  \"Regions\": {\n    \"900\": {\n      \"RegionID\": 900,") {
		t.Errorf("nested object layout:\n%s", got)
	}
	if _, err := hujson.Parse([]byte(got)); err != nil {
		t.Fatal(err)
	}

	y := Empty(YAML, Arr{})
	if got := out(t, y); strings.TrimSpace(got) != "[]" {
		t.Errorf("empty yaml: %q", got)
	}
	if err := y.Root().Append(Obj{{"name", "a"}, {"type", "A"}, {"value", "1.2.3.4"}}); err != nil {
		t.Fatal(err)
	}
	if got := out(t, y); got != "- name: a\n  type: A\n  value: 1.2.3.4\n" {
		t.Errorf("yaml append: %q", got)
	}
}

func TestYAMLEdits(t *testing.T) {
	src := "# DERP map\nregions:\n  900:\n    regionid: 900 # lab\n    regioncode: lab\n    nodes:\n      - name: 900a\n        hostname: derp.example.com\n"
	d := mustParse(t, src, YAML)
	regs, ok := d.Root().Get("Regions")
	if !ok || regs.Len() != 1 {
		t.Fatal("regions lookup")
	}
	r, _ := regs.Get("900")
	if id, ok := r.Get("RegionID"); !ok || func() int { v, _ := id.Int(); return v }() != 900 {
		t.Fatal("regionid")
	}
	if err := r.Set("regioncode", "lab2"); err != nil {
		t.Fatal(err)
	}
	if err := regs.Set("901", Obj{{"regionid", 901}, {"nodes", Arr{}}}); err != nil {
		t.Fatal(err)
	}
	got := out(t, d)
	for _, w := range []string{"# DERP map", "regionid: 900 # lab", "regioncode: lab2", "\n  901:\n"} {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in\n%s", w, got)
		}
	}
	var back struct {
		Regions map[int]struct {
			RegionID int `yaml:"regionid"`
		} `yaml:"regions"`
	}
	if err := yaml.Unmarshal([]byte(got), &back); err != nil || back.Regions[901].RegionID != 901 {
		t.Fatalf("round trip: %v %+v", err, back)
	}
	if !regs.Delete("900") || regs.Delete("nope") {
		t.Error("delete")
	}
	if regs.Len() != 1 || regs.Keys()[0] != "901" {
		t.Errorf("keys %v", regs.Keys())
	}
}

func TestScalarsAndErrors(t *testing.T) {
	d := mustParse(t, `{"a": "x", "n": 3, "f": 2.0, "b": true, "z": null, "arr": [1]}`, JSON)
	r := d.Root()
	if v, _ := r.Get("A"); v.Str() != "x" {
		t.Error("case-insensitive get")
	}
	if v, _ := r.Get("n"); func() int { i, _ := v.Int(); return i }() != 3 {
		t.Error("int")
	}
	if v, _ := r.Get("f"); func() int { i, _ := v.Int(); return i }() != 2 {
		t.Error("float int")
	}
	if v, _ := r.Get("b"); !v.Bool() {
		t.Error("bool")
	}
	if v, _ := r.Get("z"); !v.IsNull() || v.Str() != "" {
		t.Error("null")
	}
	if v, _ := r.Get("arr"); !v.IsArray() || v.At(0).Str() != "1" || v.At(5).Valid() {
		t.Error("array")
	}
	if _, ok := r.Get("missing"); ok {
		t.Error("missing")
	}
	v, _ := r.Get("a")
	if err := v.Set("x", 1); err == nil {
		t.Error("set on scalar should fail")
	}
	if err := v.Append(1); err == nil {
		t.Error("append on scalar should fail")
	}
	if err := r.SetAt(0, 1); err == nil {
		t.Error("SetAt on object should fail")
	}
	z, _ := r.Get("z")
	if err := z.Append("q"); err != nil || !z.IsArray() {
		t.Errorf("null becomes array: %v", err)
	}
	if !r.Delete("arr") || r.Len() != 5 {
		t.Error("delete member")
	}
	if _, err := Parse([]byte(`[1,]`), JSON); err == nil {
		t.Error("JSON with trailing comma should be refused")
	}
	if _, err := Parse([]byte("# only a comment\n"), YAML); err == nil {
		t.Error("empty YAML should be an error")
	}
	if _, err := Parse([]byte("{"), HuJSON); err == nil {
		t.Error("bad hujson")
	}
}

func TestSetAtKeepsComment(t *testing.T) {
	src := "[\n  // keep me\n  {\"name\": \"a\", \"type\": \"A\", \"value\": \"1.2.3.4\"}\n]"
	d := mustParse(t, src, HuJSON)
	if err := d.Root().SetAt(0, Obj{{"name", "b"}, {"type", "A"}, {"value", "5.6.7.8"}}); err != nil {
		t.Fatal(err)
	}
	got := out(t, d)
	if got != "[\n  // keep me\n  {\"name\": \"b\", \"type\": \"A\", \"value\": \"5.6.7.8\"}\n]" {
		t.Errorf("got %q", got)
	}
	y := mustParse(t, "- name: a # first\n  type: A\n  value: 1.2.3.4\n", YAML)
	if err := y.Root().At(0).Set("value", "5.6.7.8"); err != nil {
		t.Fatal(err)
	}
	if err := y.Root().SetAt(0, Obj{{"name", "c"}}); err != nil {
		t.Fatal(err)
	}
	if err := y.Root().RemoveAt(0); err != nil || y.Root().Len() != 0 {
		t.Error("yaml remove")
	}
	if err := y.Root().RemoveAt(3); err == nil {
		t.Error("out of range")
	}
}
