// Package dnsrec models headscale's DNS settings and extra DNS records:
// the typed `dns.*` block of config.yaml, inline `dns.extra_records`, and the
// hot-reloaded `dns.extra_records_path` file (JSON, HuJSON or YAML, kept in
// whatever format it already uses, with comments preserved).
package dnsrec

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/host/doctree"
	"github.com/tonhe/headsup/internal/host/hsconfig"
)

// Config keys.
const (
	KeyMagicDNS     = "dns.magic_dns"
	KeyBaseDomain   = "dns.base_domain"
	KeyOverride     = "dns.override_local_dns"
	KeyGlobal       = "dns.nameservers.global"
	KeySplit        = "dns.nameservers.split"
	KeySearch       = "dns.search_domains"
	KeyExtraRecords = "dns.extra_records"
	KeyRecordsPath  = "dns.extra_records_path"
)

// Types are the record types headscale (and Tailscale clients) accept.
var Types = []string{"A", "AAAA"}

// Record is one extra DNS record.
type Record struct {
	Name  string `json:"name" yaml:"name"`
	Type  string `json:"type" yaml:"type"`
	Value string `json:"value" yaml:"value"`
}

// Validate checks the name, that the type is A or AAAA, and that the value
// is an IP address of the matching family.
func (r Record) Validate() error {
	if strings.TrimSpace(r.Name) == "" {
		return errors.New("name is required")
	}
	if err := ValidName(r.Name); err != nil {
		return err
	}
	if !slices.Contains(Types, r.Type) {
		if slices.Contains(Types, strings.ToUpper(r.Type)) {
			return fmt.Errorf("type %q must be upper case (%s)", r.Type, strings.ToUpper(r.Type))
		}
		return fmt.Errorf("type must be A or AAAA, not %q", r.Type)
	}
	ip, err := netip.ParseAddr(r.Value)
	if err != nil {
		return fmt.Errorf("value %q is not an IP address", r.Value)
	}
	if ip.Zone() != "" {
		return fmt.Errorf("value %q must not carry a zone", r.Value)
	}
	switch {
	case r.Type == "A" && !ip.Is4():
		return fmt.Errorf("an A record needs an IPv4 address, not %s", r.Value)
	case r.Type == "AAAA" && (!ip.Is6() || ip.Is4In6()):
		return fmt.Errorf("an AAAA record needs an IPv6 address, not %s", r.Value)
	}
	return nil
}

func (r Record) String() string { return r.Name + " " + r.Type + " " + r.Value }

// ValidName checks a DNS name: dot-separated labels of 1-63 letters, digits,
// hyphens or underscores, at most 253 characters, optional trailing dot.
func ValidName(s string) error {
	n := strings.TrimSuffix(s, ".")
	if n == "" {
		return errors.New("name is required")
	}
	if len(n) > 253 {
		return fmt.Errorf("%q is longer than 253 characters", s)
	}
	for _, l := range strings.Split(n, ".") {
		if l == "" {
			return fmt.Errorf("%q has an empty label", s)
		}
		if len(l) > 63 {
			return fmt.Errorf("%q has a label longer than 63 characters", s)
		}
		for _, c := range l {
			ok := c == '-' || c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
			if !ok {
				return fmt.Errorf("%q is not a valid DNS name", s)
			}
		}
		if l[0] == '-' || l[len(l)-1] == '-' {
			return fmt.Errorf("%q has a label starting or ending with '-'", s)
		}
	}
	return nil
}

// ValidNameserver accepts an IP address or a DNS-over-HTTPS URL.
func ValidNameserver(s string) error {
	if _, err := netip.ParseAddr(s); err == nil {
		return nil
	}
	if u, err := url.Parse(s); err == nil && u.Scheme == "https" && u.Host != "" {
		return nil
	}
	return fmt.Errorf("nameserver %q is not an IP address or https:// URL", s)
}

// ValidateRecords checks every record and warns about exact duplicates.
func ValidateRecords(rs []Record) []host.Issue {
	var out []host.Issue
	seen := map[Record]bool{}
	for i, r := range rs {
		where := fmt.Sprintf("record %d", i+1)
		if r.Name != "" {
			where += " (" + r.Name + ")"
		}
		if err := r.Validate(); err != nil {
			out = append(out, host.Issue{Severity: host.SevError, Message: where + ": " + err.Error(), Source: "schema"})
			continue
		}
		k := Record{strings.ToLower(strings.TrimSuffix(r.Name, ".")), r.Type, r.Value}
		if seen[k] {
			out = append(out, host.Issue{Severity: host.SevWarn, Message: where + ": duplicate record", Source: "schema"})
		}
		seen[k] = true
	}
	return out
}

// CheckBaseDomain enforces headscale's rule that the MagicDNS base domain
// must differ from (and not contain) the server_url host.
func CheckBaseDomain(base, serverURL string) error {
	if base == "" || serverURL == "" {
		return nil
	}
	u, err := url.Parse(serverURL)
	if err != nil || u.Hostname() == "" {
		return nil
	}
	h := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	b := strings.ToLower(strings.Trim(base, "."))
	if h == b || strings.HasSuffix(h, "."+b) {
		return fmt.Errorf("dns.base_domain %q must differ from the server_url host %q (and not contain it)", base, h)
	}
	return nil
}

// CheckExclusive enforces that inline records and a records file are not
// both configured.
func CheckExclusive(hasInline bool, path string) error {
	if hasInline && path != "" {
		return errors.New("dns.extra_records and dns.extra_records_path cannot both be set")
	}
	return nil
}

// Config is the dns block of config.yaml.
type Config struct {
	MagicDNS         bool
	BaseDomain       string
	OverrideLocalDNS bool
	Global           []string
	Split            map[string][]string
	SplitOrder       []string // domains in file order
	Search           []string
	ExtraRecords     []Record
	ExtraRecordsPath string
	inlineSet        bool
}

// HasInline reports whether inline records are configured (non-empty).
func (c Config) HasInline() bool { return c.inlineSet && len(c.ExtraRecords) > 0 }

func boolOr(d *hsconfig.Doc, key string, def bool) bool {
	v, ok := d.Get(key)
	if !ok || v == "" {
		return def
	}
	switch strings.ToLower(v) {
	case "true", "yes", "on":
		return true
	}
	return false
}

// FromDoc reads the dns block. Missing keys take headscale's defaults.
func FromDoc(d *hsconfig.Doc) Config {
	c := Config{Split: map[string][]string{}}
	c.MagicDNS = boolOr(d, KeyMagicDNS, true)
	c.OverrideLocalDNS = boolOr(d, KeyOverride, true)
	c.BaseDomain, _ = d.Get(KeyBaseDomain)
	c.Global, _ = d.List(KeyGlobal)
	c.Search, _ = d.List(KeySearch)
	c.ExtraRecordsPath, _ = d.Get(KeyRecordsPath)
	if n, ok := d.Node(KeySplit); ok && n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			dom := n.Content[i].Value
			var servers []string
			v := n.Content[i+1]
			switch v.Kind {
			case yaml.SequenceNode:
				for _, s := range v.Content {
					servers = append(servers, s.Value)
				}
			case yaml.ScalarNode:
				if v.Tag != "!!null" && v.Value != "" {
					servers = []string{v.Value}
				}
			}
			c.Split[dom] = servers
			c.SplitOrder = append(c.SplitOrder, dom)
		}
	}
	if n, ok := d.Node(KeyExtraRecords); ok {
		c.inlineSet = true
		if n.Kind == yaml.SequenceNode {
			for _, e := range n.Content {
				var r Record
				if e.Decode(&r) == nil {
					c.ExtraRecords = append(c.ExtraRecords, r)
				}
			}
		}
	}
	return c
}

func issue(sev host.Severity, msg string) host.Issue {
	return host.Issue{Severity: sev, Message: msg, Source: "schema"}
}

// Validate checks the DNS settings the way headscale does on startup.
func (c Config) Validate(serverURL string) []host.Issue {
	var out []host.Issue
	if c.MagicDNS && c.BaseDomain == "" {
		out = append(out, issue(host.SevError, "dns.base_domain must be set when MagicDNS is on"))
	}
	if c.BaseDomain != "" {
		if err := ValidName(c.BaseDomain); err != nil {
			out = append(out, issue(host.SevError, "dns.base_domain: "+err.Error()))
		}
	}
	if err := CheckBaseDomain(c.BaseDomain, serverURL); err != nil {
		out = append(out, issue(host.SevError, err.Error()))
	}
	if c.OverrideLocalDNS && len(c.Global) == 0 {
		out = append(out, issue(host.SevError, "dns.override_local_dns needs at least one global nameserver"))
	}
	for _, ns := range c.Global {
		if err := ValidNameserver(ns); err != nil {
			out = append(out, issue(host.SevError, "dns.nameservers.global: "+err.Error()))
		}
	}
	doms := c.SplitOrder
	if len(doms) == 0 {
		for d := range c.Split {
			doms = append(doms, d)
		}
		slices.Sort(doms)
	}
	for _, d := range doms {
		if err := ValidName(d); err != nil {
			out = append(out, issue(host.SevError, "split DNS: "+err.Error()))
		}
		if len(c.Split[d]) == 0 {
			out = append(out, issue(host.SevError, fmt.Sprintf("split DNS domain %s has no nameservers", d)))
		}
		for _, ns := range c.Split[d] {
			if err := ValidNameserver(ns); err != nil {
				out = append(out, issue(host.SevError, "split DNS "+d+": "+err.Error()))
			}
		}
	}
	for _, s := range c.Search {
		if err := ValidName(s); err != nil {
			out = append(out, issue(host.SevError, "dns.search_domains: "+err.Error()))
		}
	}
	if err := CheckExclusive(len(c.ExtraRecords) > 0, c.ExtraRecordsPath); err != nil {
		out = append(out, issue(host.SevError, err.Error()))
	}
	out = append(out, ValidateRecords(c.ExtraRecords)...)
	return out
}

var recordFields = []string{"name", "type", "value"}

// SetInline writes rs as dns.extra_records (an empty list removes the key).
func SetInline(d *hsconfig.Doc, rs []Record) error {
	if len(rs) == 0 {
		return d.Delete(KeyExtraRecords)
	}
	rows := make([][]string, len(rs))
	for i, r := range rs {
		rows[i] = []string{r.Name, r.Type, r.Value}
	}
	return d.SetMapList(KeyExtraRecords, recordFields, rows)
}

// CheckRecordsPath checks a path for a new records file: absolute, with an
// extension headscale understands.
func CheckRecordsPath(p string) error {
	if !filepath.IsAbs(p) {
		return fmt.Errorf("%q must be an absolute path", p)
	}
	switch strings.ToLower(filepath.Ext(p)) {
	case ".json", ".hujson", ".yaml", ".yml":
		return nil
	}
	return fmt.Errorf("%q must end in .json, .hujson, .yaml or .yml", p)
}

// MoveInline moves the inline records of d into a new records file at path
// and points dns.extra_records_path at it. The caller writes both files as
// one change set.
func MoveInline(d *hsconfig.Doc, path string) (*File, error) {
	if err := CheckRecordsPath(path); err != nil {
		return nil, err
	}
	c := FromDoc(d)
	if c.ExtraRecordsPath != "" {
		return nil, fmt.Errorf("records already live in %s", c.ExtraRecordsPath)
	}
	f, err := NewFile(path, c.ExtraRecords)
	if err != nil {
		return nil, err
	}
	if err := d.Delete(KeyExtraRecords); err != nil {
		return nil, err
	}
	if err := d.Set(KeyRecordsPath, path); err != nil {
		return nil, err
	}
	return f, nil
}

// File is an extra-records file being edited.
type File struct {
	Path    string
	Format  doctree.Format
	Records []Record
	doc     *doctree.Doc
}

// NewFile returns an unsaved records file in the format path's extension
// picks, holding rs.
func NewFile(path string, rs []Record) (*File, error) {
	f := &File{Path: path, Format: doctree.FormatFor(path)}
	f.doc = doctree.Empty(f.Format, doctree.Arr{})
	for _, r := range rs {
		if err := f.Add(r); err != nil {
			return nil, err
		}
	}
	return f, nil
}

// ParseFile parses a records file, keeping its format. A ".json" file that
// uses comments or trailing commas is treated as HuJSON so they survive.
func ParseFile(path string, b []byte) (*File, error) {
	format := doctree.FormatFor(path)
	if strings.TrimSpace(string(b)) == "" {
		return NewFile(path, nil)
	}
	if format == doctree.JSON && doctree.Sniff(b) == doctree.HuJSON {
		format = doctree.HuJSON
	}
	doc, err := doctree.Parse(b, format)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	root := doc.Root()
	if !root.IsArray() && !root.IsNull() {
		return nil, fmt.Errorf("%s: records must be a list of {name, type, value}", path)
	}
	f := &File{Path: path, Format: format, doc: doc}
	for i := 0; i < root.Len(); i++ {
		e := root.At(i)
		var r Record
		if v, ok := e.Get("name"); ok {
			r.Name = v.Str()
		}
		if v, ok := e.Get("type"); ok {
			r.Type = v.Str()
		}
		if v, ok := e.Get("value"); ok {
			r.Value = v.Str()
		}
		f.Records = append(f.Records, r)
	}
	return f, nil
}

func recObj(r Record) doctree.Obj {
	return doctree.Obj{{K: "name", V: r.Name}, {K: "type", V: r.Type}, {K: "value", V: r.Value}}
}

// Add appends a record.
func (f *File) Add(r Record) error {
	if err := f.doc.Root().Append(recObj(r)); err != nil {
		return err
	}
	f.Records = append(f.Records, r)
	return nil
}

// Set replaces record i, editing only the fields that changed.
func (f *File) Set(i int, r Record) error {
	if i < 0 || i >= len(f.Records) {
		return fmt.Errorf("no record %d", i+1)
	}
	e := f.doc.Root().At(i)
	if !e.IsObject() {
		if err := f.doc.Root().SetAt(i, recObj(r)); err != nil {
			return err
		}
	} else {
		old := f.Records[i]
		for _, kv := range []struct{ k, o, n string }{{"name", old.Name, r.Name}, {"type", old.Type, r.Type}, {"value", old.Value, r.Value}} {
			if kv.o != kv.n {
				if err := e.Set(kv.k, kv.n); err != nil {
					return err
				}
			}
		}
	}
	f.Records[i] = r
	return nil
}

// Delete removes record i.
func (f *File) Delete(i int) error {
	if i < 0 || i >= len(f.Records) {
		return fmt.Errorf("no record %d", i+1)
	}
	if err := f.doc.Root().RemoveAt(i); err != nil {
		return err
	}
	f.Records = slices.Delete(f.Records, i, i+1)
	return nil
}

// Bytes serialises the file. A JSON or HuJSON file always ends in a newline.
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

// Schema validates the content of a records file of any supported format
// (the managed-file pipeline's schema check).
func Schema(b []byte) []host.Issue {
	if strings.TrimSpace(string(b)) == "" {
		return nil
	}
	path := "records.json"
	if doctree.Sniff(b) == doctree.YAML {
		path = "records.yaml"
	}
	f, err := ParseFile(path, b)
	if err != nil {
		return []host.Issue{issue(host.SevError, strings.TrimPrefix(err.Error(), path+": "))}
	}
	return ValidateRecords(f.Records)
}
