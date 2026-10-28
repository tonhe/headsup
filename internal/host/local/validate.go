package local

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"regexp"
	"strconv"
	"strings"

	"github.com/tailscale/hujson"
	"github.com/tonhe/fathom/internal/host"
	"github.com/tonhe/fathom/internal/host/hsconfig"
	"gopkg.in/yaml.v3"
)

var yamlLineRE = regexp.MustCompile(`line (\d+)`)

func schemaErr(msg string, line int) []host.Issue {
	return []host.Issue{{Severity: host.SevError, Message: msg, Line: line, Source: "schema"}}
}

func lineOf(err error) int {
	if m := yamlLineRE.FindStringSubmatch(err.Error()); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	return 0
}

// ConfigSchema parses config.yaml and runs the typed catalog checks.
func ConfigSchema(b []byte, version string) []host.Issue {
	d, err := hsconfig.Parse(b)
	if err != nil {
		return schemaErr("YAML: "+err.Error(), lineOf(err))
	}
	return hsconfig.ValidateFor(d, version)
}

// PolicySchema checks that a policy is a HuJSON object.
func PolicySchema(b []byte) []host.Issue {
	v, err := hujson.Parse(b)
	if err != nil {
		return schemaErr("HuJSON: "+err.Error(), lineOf(err))
	}
	v.Standardize()
	var top map[string]json.RawMessage
	if err := json.Unmarshal(v.Pack(), &top); err != nil {
		return schemaErr("policy must be a JSON object", 1)
	}
	var out []host.Issue
	known := map[string]bool{"groups": true, "hosts": true, "tagOwners": true, "acls": true, "grants": true, "ssh": true,
		"autoApprovers": true, "tests": true, "sshTests": true, "nodeAttrs": true, "postures": true, "ipsets": true}
	for k := range top {
		if !known[k] {
			out = append(out, host.Issue{Severity: host.SevWarn, Message: fmt.Sprintf("unknown top-level policy key %q", k), Source: "schema"})
		}
	}
	return out
}

type record struct {
	Name  string `json:"name" yaml:"name"`
	Type  string `json:"type" yaml:"type"`
	Value string `json:"value" yaml:"value"`
}

// RecordsSchema checks an extra-records file: a JSON/HuJSON or YAML list of
// {name, type (A|AAAA), value (IP of that family)}.
func RecordsSchema(b []byte) []host.Issue {
	var recs []record
	if strings.TrimSpace(string(b)) == "" {
		return nil
	}
	if std, err := hujson.Standardize(append([]byte(nil), b...)); err == nil {
		if err := json.Unmarshal(std, &recs); err != nil {
			return schemaErr("records must be a list of {name, type, value}: "+err.Error(), 0)
		}
	} else if err := yaml.Unmarshal(b, &recs); err != nil {
		return schemaErr("records file is neither JSON nor YAML: "+err.Error(), lineOf(err))
	}
	var out []host.Issue
	for i, r := range recs {
		where := fmt.Sprintf("record %d", i+1)
		if r.Name != "" {
			where += " (" + r.Name + ")"
		}
		if r.Name == "" {
			out = append(out, host.Issue{Severity: host.SevError, Message: where + ": name is required", Source: "schema"})
		}
		ip, err := netip.ParseAddr(r.Value)
		switch {
		case r.Type != "A" && r.Type != "AAAA":
			out = append(out, host.Issue{Severity: host.SevError, Message: where + ": type must be A or AAAA", Source: "schema"})
		case err != nil:
			out = append(out, host.Issue{Severity: host.SevError, Message: where + ": value is not an IP address", Source: "schema"})
		case r.Type == "A" && !ip.Is4(), r.Type == "AAAA" && (!ip.Is6() || ip.Is4In6()):
			out = append(out, host.Issue{Severity: host.SevError, Message: where + ": value does not match the record type", Source: "schema"})
		}
	}
	return out
}

// DERPMapSchema checks a DERP map file has regions with nodes.
func DERPMapSchema(b []byte) []host.Issue {
	var m struct {
		Regions map[string]struct {
			RegionID int `json:"regionid" yaml:"regionid"`
			Nodes    []struct {
				Name     string `json:"name" yaml:"name"`
				HostName string `json:"hostname" yaml:"hostname"`
			} `json:"nodes" yaml:"nodes"`
		} `json:"regions" yaml:"regions"`
	}
	if std, err := hujson.Standardize(append([]byte(nil), b...)); err == nil {
		// Tailscale JSON uses RegionID/Nodes/HostName; match case-insensitively.
		if err := json.Unmarshal(std, &m); err != nil {
			return schemaErr("DERP map: "+err.Error(), 0)
		}
	} else if err := yaml.Unmarshal(b, &m); err != nil {
		return schemaErr("DERP map: "+err.Error(), lineOf(err))
	}
	if len(m.Regions) == 0 {
		return schemaErr("DERP map has no regions", 0)
	}
	var out []host.Issue
	for k, r := range m.Regions {
		if r.RegionID < 1 || r.RegionID > 999 {
			out = append(out, host.Issue{Severity: host.SevWarn, Message: fmt.Sprintf("region %s: regionid %d is outside 1-999", k, r.RegionID), Source: "schema"})
		}
		if len(r.Nodes) == 0 {
			out = append(out, host.Issue{Severity: host.SevError, Message: fmt.Sprintf("region %s has no nodes", k), Source: "schema"})
		}
		for _, n := range r.Nodes {
			if n.HostName == "" {
				out = append(out, host.Issue{Severity: host.SevError, Message: fmt.Sprintf("region %s node %q has no hostname", k, n.Name), Source: "schema"})
			}
		}
	}
	return out
}
