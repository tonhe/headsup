// Package derpmap models DERP map files (the files listed in `derp.paths`)
// and the live map served at /debug/derp. Files keep their format: YAML
// with headscale's lower-case keys, or Tailscale's JSON / HuJSON with
// PascalCase keys. Edits change only the fields that differ, so comments
// survive.
package derpmap

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/host/doctree"
)

// Node is one DERP server in a region.
type Node struct {
	Name             string
	RegionID         int // 0 inherits the region's ID
	HostName         string
	IPv4             string // "" resolves HostName; "none" disables IPv4
	IPv6             string
	STUNPort         int // 0 is 3478; -1 disables STUN
	DERPPort         int // 0 is 443
	STUNOnly         bool
	InsecureForTests bool
}

// Region is one DERP region.
type Region struct {
	RegionID   int
	RegionCode string
	RegionName string
	Nodes      []Node
	MapKey     int // the key the region is stored under; 0 when unknown
}

// Map is a DERP map, regions sorted by ID.
type Map struct {
	Regions            []Region
	OmitDefaultRegions bool
}

// Region returns the region with id.
func (m Map) Region(id int) (Region, bool) {
	for _, r := range m.Regions {
		if r.RegionID == id {
			return r, true
		}
	}
	return Region{}, false
}

func issue(sev host.Severity, msg string) host.Issue {
	return host.Issue{Severity: sev, Message: msg, Source: "schema"}
}

func checkIP(fam int, s string) error {
	if s == "" || s == "none" {
		return nil
	}
	ip, err := netip.ParseAddr(s)
	if err != nil || (fam == 4 && !ip.Is4()) || (fam == 6 && (!ip.Is6() || ip.Is4In6())) {
		return fmt.Errorf("%q is not an IPv%d address", s, fam)
	}
	return nil
}

// regionIssues checks one region on its own.
func regionIssues(r Region) []host.Issue {
	var out []host.Issue
	where := fmt.Sprintf("region %d", r.RegionID)
	if r.RegionID < 1 || r.RegionID > 999 {
		out = append(out, issue(host.SevError, fmt.Sprintf("%s: a custom region ID must be 1-999", where)))
	}
	if r.MapKey != 0 && r.MapKey != r.RegionID {
		out = append(out, issue(host.SevError, fmt.Sprintf("%s is stored under key %d; the key and RegionID must match", where, r.MapKey)))
	}
	if strings.TrimSpace(r.RegionCode) == "" {
		out = append(out, issue(host.SevWarn, where+": no region code; clients show the code in `tailscale netcheck`"))
	}
	if len(r.Nodes) == 0 {
		out = append(out, issue(host.SevError, where+" needs at least one node"))
	}
	for i, n := range r.Nodes {
		nw := fmt.Sprintf("%s node %d", where, i+1)
		if n.Name != "" {
			nw = fmt.Sprintf("%s node %q", where, n.Name)
		}
		if strings.TrimSpace(n.HostName) == "" {
			out = append(out, issue(host.SevError, nw+": a hostname is required"))
		}
		if n.RegionID != 0 && n.RegionID != r.RegionID {
			out = append(out, issue(host.SevError, fmt.Sprintf("%s has region ID %d but sits in region %d", nw, n.RegionID, r.RegionID)))
		}
		if n.STUNPort < -1 || n.STUNPort > 65535 {
			out = append(out, issue(host.SevError, fmt.Sprintf("%s: STUN port %d must be -1 (off), 0 (3478) or 1-65535", nw, n.STUNPort)))
		}
		if n.DERPPort < 0 || n.DERPPort > 65535 {
			out = append(out, issue(host.SevError, fmt.Sprintf("%s: DERP port %d must be 0 (443) or 1-65535", nw, n.DERPPort)))
		}
		if err := checkIP(4, n.IPv4); err != nil {
			out = append(out, issue(host.SevError, nw+": "+err.Error()))
		}
		if err := checkIP(6, n.IPv6); err != nil {
			out = append(out, issue(host.SevError, nw+": "+err.Error()))
		}
		if n.Name == "" {
			out = append(out, issue(host.SevWarn, nw+": no name"))
		}
	}
	return out
}

// Validate checks a map: custom region IDs 1-999, unique region IDs and
// codes, unique node names, valid ports and addresses, and at least one
// node per region.
func Validate(m Map) []host.Issue {
	if len(m.Regions) == 0 {
		return []host.Issue{issue(host.SevError, "DERP map has no regions")}
	}
	var out []host.Issue
	ids := map[int]bool{}
	codes := map[string]int{}
	names := map[string]int{}
	for _, r := range m.Regions {
		out = append(out, regionIssues(r)...)
		if ids[r.RegionID] {
			out = append(out, issue(host.SevError, fmt.Sprintf("region %d is defined twice", r.RegionID)))
		}
		ids[r.RegionID] = true
		if c := strings.ToLower(r.RegionCode); c != "" {
			if other, dup := codes[c]; dup && other != r.RegionID {
				out = append(out, issue(host.SevError, fmt.Sprintf("region %d: code %q is also used by region %d", r.RegionID, r.RegionCode, other)))
			} else {
				codes[c] = r.RegionID
			}
		}
		for _, n := range r.Nodes {
			if n.Name == "" {
				continue
			}
			if other, dup := names[n.Name]; dup {
				out = append(out, issue(host.SevError, fmt.Sprintf("region %d: node name %q is also used in region %d", r.RegionID, n.Name, other)))
			} else {
				names[n.Name] = r.RegionID
			}
		}
	}
	return out
}

// CheckNewRegion validates r as an addition to m, or as the edit of the
// region with ID editing (0 for a new region).
func (m Map) CheckNewRegion(r Region, editing int) error {
	for _, is := range regionIssues(r) {
		if is.Severity == host.SevError {
			return errors.New(is.Message)
		}
	}
	for _, o := range m.Regions {
		if o.RegionID == editing {
			continue
		}
		if o.RegionID == r.RegionID {
			return fmt.Errorf("region %d already exists", r.RegionID)
		}
		if r.RegionCode != "" && strings.EqualFold(o.RegionCode, r.RegionCode) {
			return fmt.Errorf("code %q is already used by region %d", r.RegionCode, o.RegionID)
		}
	}
	return nil
}

// CheckEmbedded warns when the embedded DERP server's region ID is also in
// the map; headscale lets the embedded region win.
func CheckEmbedded(m Map, embeddedID int) []host.Issue {
	if _, ok := m.Region(embeddedID); ok {
		return []host.Issue{issue(host.SevWarn, fmt.Sprintf("region %d is also the embedded DERP server's region; the embedded server replaces it", embeddedID))}
	}
	return nil
}

// --- files ------------------------------------------------------------------

// File is a DERP map file being edited.
type File struct {
	Path   string
	Format doctree.Format
	Map    Map
	doc    *doctree.Doc
}

// NewFile returns an unsaved, empty map in the format path's extension picks.
func NewFile(path string) *File {
	f := &File{Path: path, Format: doctree.FormatFor(path)}
	f.doc = doctree.Empty(f.Format, doctree.Obj{})
	return f
}

// ParseFile parses a DERP map file, keeping its format.
func ParseFile(path string, b []byte) (*File, error) {
	if strings.TrimSpace(string(b)) == "" {
		return NewFile(path), nil
	}
	format := doctree.FormatFor(path)
	if format == doctree.JSON && doctree.Sniff(b) == doctree.HuJSON {
		format = doctree.HuJSON
	}
	doc, err := doctree.Parse(b, format)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if !doc.Root().IsObject() {
		return nil, fmt.Errorf("%s: a DERP map must be an object with regions", path)
	}
	f := &File{Path: path, Format: format, doc: doc}
	f.reload()
	return f, nil
}

func (f *File) reload() {
	f.Map = readMap(f.doc.Root())
}

func readMap(root doctree.Node) Map {
	var m Map
	if v, ok := root.Get("OmitDefaultRegions"); ok {
		m.OmitDefaultRegions = v.Bool()
	}
	regs, ok := root.Get("Regions")
	if !ok {
		return m
	}
	for _, k := range regs.Keys() {
		rn, _ := regs.Get(k)
		r := readRegion(rn)
		r.MapKey, _ = strconv.Atoi(k)
		if r.RegionID == 0 {
			r.RegionID = r.MapKey
		}
		m.Regions = append(m.Regions, r)
	}
	sort.SliceStable(m.Regions, func(i, j int) bool { return m.Regions[i].RegionID < m.Regions[j].RegionID })
	return m
}

func str(n doctree.Node, k string) string {
	v, _ := n.Get(k)
	return v.Str()
}

func num(n doctree.Node, k string) int {
	v, _ := n.Get(k)
	i, _ := v.Int()
	return i
}

func flag(n doctree.Node, k string) bool {
	v, _ := n.Get(k)
	return v.Bool()
}

func readRegion(n doctree.Node) Region {
	r := Region{RegionID: num(n, "RegionID"), RegionCode: str(n, "RegionCode"), RegionName: str(n, "RegionName")}
	if ns, ok := n.Get("Nodes"); ok {
		for i := 0; i < ns.Len(); i++ {
			r.Nodes = append(r.Nodes, readNode(ns.At(i)))
		}
	}
	return r
}

func readNode(n doctree.Node) Node {
	return Node{
		Name: str(n, "Name"), RegionID: num(n, "RegionID"), HostName: str(n, "HostName"),
		IPv4: str(n, "IPv4"), IPv6: str(n, "IPv6"), STUNPort: num(n, "STUNPort"), DERPPort: num(n, "DERPPort"),
		STUNOnly: flag(n, "STUNOnly"), InsecureForTests: flag(n, "InsecureForTests"),
	}
}

// key spells a Tailscale JSON key the way the file's format expects.
func (f *File) key(k string) string {
	if f.Format == doctree.YAML {
		return strings.ToLower(k)
	}
	return k
}

type field struct {
	k string
	v any // string, int or bool; the zero value means "omit"
}

func zero(v any) bool {
	switch x := v.(type) {
	case string:
		return x == ""
	case int:
		return x == 0
	case bool:
		return !x
	}
	return v == nil
}

func nodeFields(n Node) []field {
	return []field{{"Name", n.Name}, {"RegionID", n.RegionID}, {"HostName", n.HostName}, {"IPv4", n.IPv4}, {"IPv6", n.IPv6},
		{"STUNPort", n.STUNPort}, {"DERPPort", n.DERPPort}, {"STUNOnly", n.STUNOnly}, {"InsecureForTests", n.InsecureForTests}}
}

func (f *File) obj(fs []field) doctree.Obj {
	var o doctree.Obj
	for _, x := range fs {
		if !zero(x.v) {
			o = append(o, doctree.KV{K: f.key(x.k), V: x.v})
		}
	}
	return o
}

func (f *File) regionObj(r Region) doctree.Obj {
	o := f.obj([]field{{"RegionID", r.RegionID}, {"RegionCode", r.RegionCode}, {"RegionName", r.RegionName}})
	nodes := doctree.Arr{}
	for _, n := range r.Nodes {
		nodes = append(nodes, f.obj(nodeFields(n)))
	}
	return append(o, doctree.KV{K: f.key("Nodes"), V: nodes})
}

// update sets each field that differs from the document, deleting fields
// whose new value is the zero value.
func (f *File) update(n doctree.Node, fs []field) error {
	for _, x := range fs {
		cur, has := n.Get(x.k)
		if zero(x.v) {
			if has {
				n.Delete(x.k)
			}
			continue
		}
		if has && cur.Str() == fmt.Sprint(x.v) {
			continue
		}
		if err := n.Set(f.key(x.k), x.v); err != nil {
			return err
		}
	}
	return nil
}

func (f *File) regions() (doctree.Node, error) {
	root := f.doc.Root()
	if regs, ok := root.Get("Regions"); ok && !regs.IsNull() {
		return regs, nil
	}
	if err := root.Set(f.key("Regions"), doctree.Obj{}); err != nil {
		return doctree.Node{}, err
	}
	regs, _ := root.Get("Regions")
	return regs, nil
}

// PutRegion adds r or updates the region with the same ID in place.
func (f *File) PutRegion(r Region) error {
	regs, err := f.regions()
	if err != nil {
		return err
	}
	key := strconv.Itoa(r.RegionID)
	rn, ok := regs.Get(key)
	if !ok || !rn.IsObject() {
		err := regs.Set(key, f.regionObj(r))
		f.reload()
		return err
	}
	if err := f.update(rn, []field{{"RegionID", r.RegionID}, {"RegionCode", r.RegionCode}, {"RegionName", r.RegionName}}); err != nil {
		return err
	}
	ns, ok := rn.Get("Nodes")
	if !ok || ns.IsNull() {
		if err := rn.Set(f.key("Nodes"), doctree.Arr{}); err != nil {
			return err
		}
		ns, _ = rn.Get("Nodes")
	}
	for i, n := range r.Nodes {
		switch {
		case i >= ns.Len():
			err = ns.Append(f.obj(nodeFields(n)))
		case ns.At(i).IsObject():
			err = f.update(ns.At(i), nodeFields(n))
		default:
			err = ns.SetAt(i, f.obj(nodeFields(n)))
		}
		if err != nil {
			return err
		}
	}
	for ns.Len() > len(r.Nodes) {
		if err := ns.RemoveAt(ns.Len() - 1); err != nil {
			return err
		}
	}
	f.reload()
	return nil
}

// ReplaceRegion stores r in place of the region with ID old (which may be
// r's own ID, or 0 to add).
func (f *File) ReplaceRegion(old int, r Region) error {
	if old != 0 && old != r.RegionID {
		if err := f.DeleteRegion(old); err != nil {
			return err
		}
	}
	return f.PutRegion(r)
}

// DeleteRegion removes the region with id.
func (f *File) DeleteRegion(id int) error {
	regs, err := f.regions()
	if err != nil {
		return err
	}
	for _, k := range regs.Keys() {
		rn, _ := regs.Get(k)
		kid, _ := strconv.Atoi(k)
		if kid == id || num(rn, "RegionID") == id {
			regs.Delete(k)
			f.reload()
			return nil
		}
	}
	return fmt.Errorf("region %d is not in %s", id, f.Path)
}

// Bytes serialises the file, ending in a newline.
func (f *File) Bytes() ([]byte, error) {
	b, err := f.doc.Bytes()
	if err != nil {
		return nil, err
	}
	if len(b) > 0 && b[len(b)-1] != '\n' {
		b = append(b, '\n')
	}
	return b, nil
}

// Schema validates the content of a DERP map file of any supported format
// (the managed-file pipeline's schema check).
func Schema(b []byte) []host.Issue {
	path := "derp.json"
	if doctree.Sniff(b) == doctree.YAML {
		path = "derp.yaml"
	}
	f, err := ParseFile(path, b)
	if err != nil {
		return []host.Issue{issue(host.SevError, strings.TrimPrefix(err.Error(), path+": "))}
	}
	return Validate(f.Map)
}

// --- live map -------------------------------------------------------------

type liveNode struct {
	Name, HostName, IPv4, IPv6 string
	RegionID, STUNPort         int
	DERPPort                   int
	STUNOnly, InsecureForTests bool
}

type liveRegion struct {
	RegionID               int
	RegionCode, RegionName string
	Nodes                  []liveNode
}

// ParseLive parses the server's current DERP map from /debug/derp: a
// tailcfg.DERPMap ({"Regions": {...}}), possibly wrapped in an object. It
// returns host.ErrUnsupportedShape for anything else.
func ParseLive(b []byte, contentType string) (m Map, err error) {
	defer func() {
		if r := recover(); r != nil {
			m, err = Map{}, host.ErrUnsupportedShape
		}
	}()
	t := bytes.TrimSpace(b)
	if strings.Contains(strings.ToLower(contentType), "html") || bytes.HasPrefix(t, []byte("<")) {
		return Map{}, host.ErrUnsupportedShape
	}
	var top map[string]json.RawMessage
	if json.Unmarshal(t, &top) != nil {
		return Map{}, host.ErrUnsupportedShape
	}
	raw, ok := findRegions(top)
	if !ok {
		for _, k := range []string{"derp_map", "derpmap", "DERPMap", "derpMap", "map"} {
			var inner map[string]json.RawMessage
			if v, has := top[k]; has && json.Unmarshal(v, &inner) == nil {
				if raw, ok = findRegions(inner); ok {
					break
				}
			}
		}
	}
	if !ok {
		return Map{}, host.ErrUnsupportedShape
	}
	var regs map[string]liveRegion
	if json.Unmarshal(raw, &regs) != nil {
		return Map{}, host.ErrUnsupportedShape
	}
	for k, r := range regs {
		reg := Region{RegionID: r.RegionID, RegionCode: r.RegionCode, RegionName: r.RegionName}
		reg.MapKey, _ = strconv.Atoi(k)
		if reg.RegionID == 0 {
			reg.RegionID = reg.MapKey
		}
		for _, n := range r.Nodes {
			reg.Nodes = append(reg.Nodes, Node{Name: n.Name, RegionID: n.RegionID, HostName: n.HostName, IPv4: n.IPv4, IPv6: n.IPv6,
				STUNPort: n.STUNPort, DERPPort: n.DERPPort, STUNOnly: n.STUNOnly, InsecureForTests: n.InsecureForTests})
		}
		m.Regions = append(m.Regions, reg)
	}
	sort.Slice(m.Regions, func(i, j int) bool { return m.Regions[i].RegionID < m.Regions[j].RegionID })
	return m, nil
}

func findRegions(top map[string]json.RawMessage) (json.RawMessage, bool) {
	for k, v := range top {
		if strings.EqualFold(k, "Regions") {
			return v, true
		}
	}
	return nil, false
}

// Pref summarises which machines prefer a region and their latency to it.
type Pref struct {
	Machines int     // machines whose preferred (home) DERP is this region
	Samples  int     // machines that reported a latency to this region
	MedianMS float64 // median of each machine's best latency
	BestMS   float64
}

// Preferences aggregates per-region preference and latency from NodeExtras.
// DERPLatency keys are "<regionID>-v4" / "<regionID>-v6"; a machine's best
// family counts once.
func Preferences(extras map[uint64]host.NodeExtras) map[int]Pref {
	out := map[int]Pref{}
	lat := map[int][]float64{}
	for _, x := range extras {
		if x.PreferredDERP > 0 {
			p := out[x.PreferredDERP]
			p.Machines++
			out[x.PreferredDERP] = p
		}
		best := map[int]float64{}
		for k, ms := range x.DERPLatency {
			id, err := strconv.Atoi(strings.SplitN(k, "-", 2)[0])
			if err != nil || ms <= 0 {
				continue
			}
			if b, ok := best[id]; !ok || ms < b {
				best[id] = ms
			}
		}
		for id, ms := range best {
			lat[id] = append(lat[id], ms)
		}
	}
	for id, xs := range lat {
		slices.Sort(xs)
		p := out[id]
		p.Samples = len(xs)
		p.BestMS = xs[0]
		if len(xs)%2 == 1 {
			p.MedianMS = xs[len(xs)/2]
		} else {
			p.MedianMS = (xs[len(xs)/2-1] + xs[len(xs)/2]) / 2
		}
		out[id] = p
	}
	return out
}
