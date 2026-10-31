package local

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"

	"github.com/tailscale/hujson"
	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/host/derpmap"
	"github.com/tonhe/headsup/internal/host/dnsrec"
	"github.com/tonhe/headsup/internal/host/hsconfig"
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

// RecordsSchema checks an extra-records file: a JSON/HuJSON or YAML list of
// {name, type (A|AAAA), value (IP of that family)}. The rules live in
// internal/host/dnsrec.
func RecordsSchema(b []byte) []host.Issue { return dnsrec.Schema(b) }

// DERPMapSchema checks a DERP map file (region IDs, codes, nodes, ports).
// The rules live in internal/host/derpmap.
func DERPMapSchema(b []byte) []host.Issue { return derpmap.Schema(b) }
