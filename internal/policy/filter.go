package policy

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrFilterShape means /debug/filter returned something we do not recognise.
var ErrFilterShape = errors.New("unrecognised /debug/filter format")

// FilterRule is one rule of the packet filter headscale compiled from the
// policy (tailcfg.FilterRule): who may send, to which addresses and ports.
type FilterRule struct {
	SrcIPs   []string
	DstPorts []FilterDst
	Protos   []int // IANA protocol numbers; empty means TCP, UDP, and ICMP
	CapGrant bool  // an app-capability grant rather than a packet rule
}

// FilterDst is one destination of a filter rule.
type FilterDst struct {
	IP    string
	Ports string // "*", "22", "80-443"
}

// ParseFilter reads the JSON served at /debug/filter: a list of
// tailcfg.FilterRule, or an object holding one under "filter" or "rules".
// It never panics; anything unrecognised is ErrFilterShape.
func ParseFilter(b []byte) (rules []FilterRule, err error) {
	defer func() {
		if r := recover(); r != nil {
			rules, err = nil, fmt.Errorf("%w: %v", ErrFilterShape, r)
		}
	}()
	var raw []map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		var obj map[string]json.RawMessage
		if json.Unmarshal(b, &obj) != nil {
			return nil, fmt.Errorf("%w: not JSON", ErrFilterShape)
		}
		inner, ok := obj["filter"]
		if !ok {
			inner, ok = obj["rules"]
		}
		if !ok || json.Unmarshal(inner, &raw) != nil {
			return nil, fmt.Errorf("%w: no rule list", ErrFilterShape)
		}
	}
	out := make([]FilterRule, 0, len(raw))
	for _, m := range raw {
		var r FilterRule
		if v, ok := field(m, "SrcIPs"); ok && json.Unmarshal(v, &r.SrcIPs) != nil {
			return nil, fmt.Errorf("%w: SrcIPs", ErrFilterShape)
		}
		if v, ok := field(m, "IPProto"); ok && json.Unmarshal(v, &r.Protos) != nil {
			return nil, fmt.Errorf("%w: IPProto", ErrFilterShape)
		}
		if _, ok := field(m, "CapGrant"); ok {
			r.CapGrant = true
		}
		if v, ok := field(m, "DstPorts"); ok {
			var dsts []struct {
				IP    string
				Ports struct{ First, Last int }
			}
			if json.Unmarshal(v, &dsts) != nil {
				return nil, fmt.Errorf("%w: DstPorts", ErrFilterShape)
			}
			for _, d := range dsts {
				r.DstPorts = append(r.DstPorts, FilterDst{IP: d.IP, Ports: portRange(d.Ports.First, d.Ports.Last)})
			}
		}
		out = append(out, r)
	}
	return out, nil
}

// field looks up a key case-insensitively, as encoding/json would.
func field(m map[string]json.RawMessage, k string) (json.RawMessage, bool) {
	if v, ok := m[k]; ok {
		return v, true
	}
	for kk, v := range m {
		if strings.EqualFold(kk, k) {
			return v, true
		}
	}
	return nil, false
}

func portRange(first, last int) string {
	switch {
	case first == 0 && last == 65535:
		return "*"
	case first == last:
		return strconv.Itoa(first)
	}
	return fmt.Sprintf("%d-%d", first, last)
}

var protoNames = map[int]string{1: "icmp", 2: "igmp", 6: "tcp", 17: "udp", 58: "icmpv6", 132: "sctp", 47: "gre", 50: "esp", 51: "ah"}

// ProtoString names the rule's protocols ("tcp,udp,icmp" by default).
func (r FilterRule) ProtoString() string {
	if len(r.Protos) == 0 {
		return "tcp,udp,icmp"
	}
	parts := make([]string, len(r.Protos))
	for i, p := range r.Protos {
		if n, ok := protoNames[p]; ok {
			parts[i] = n
		} else {
			parts[i] = strconv.Itoa(p)
		}
	}
	return strings.Join(parts, ",")
}
