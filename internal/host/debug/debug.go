// Package debug reads headscale's metrics_listen_addr: /metrics and the
// internal /debug/* endpoints. Their formats are unversioned, so every
// parser is defensive and returns host.ErrUnsupportedShape instead of
// guessing; it never panics.
package debug

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	"github.com/tonhe/headsup/internal/host"
)

const maxBody = 8 << 20

// Client implements host.DebugClient over HTTP.
type Client struct {
	base   string
	hc     *http.Client
	pingHC *http.Client
	now    func() time.Time

	mu      sync.Mutex
	probed  time.Time
	avail   bool
	probeOK time.Duration
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient sets the HTTP client (tests, custom dialers).
func WithHTTPClient(hc *http.Client) Option { return func(c *Client) { c.hc = hc } }

// WithBaseURL overrides the base URL derived from the address.
func WithBaseURL(u string) Option { return func(c *Client) { c.base = strings.TrimRight(u, "/") } }

// New returns a client for addr (metrics_listen_addr, e.g. "127.0.0.1:9090").
// A wildcard host is reached via loopback.
func New(addr string, opts ...Option) *Client {
	c := &Client{hc: &http.Client{Timeout: 10 * time.Second}, now: time.Now, probeOK: 30 * time.Second}
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		h, p := addr[:i], addr[i+1:]
		switch strings.Trim(h, "[]") {
		case "", "0.0.0.0":
			h = "127.0.0.1"
		case "::":
			h = "[::1]"
		}
		c.base = "http://" + h + ":" + p
	}
	for _, o := range opts {
		o(c)
	}
	ph := *c.hc
	ph.Timeout = 40 * time.Second // headscale waits up to 30s for a pong
	c.pingHC = &ph
	return c
}

var _ host.DebugClient = (*Client)(nil)

// Available probes /metrics (cached for 30s).
func (c *Client) Available() bool {
	if c.base == "" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.probed.IsZero() && c.now().Sub(c.probed) < c.probeOK {
		return c.avail
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, status, _, err := c.get(ctx, c.hc, "/metrics", "")
	c.avail = err == nil && status == http.StatusOK
	c.probed = c.now()
	return c.avail
}

func (c *Client) get(ctx context.Context, hc *http.Client, path, accept string) ([]byte, int, string, error) {
	if c.base == "" {
		return nil, 0, "", host.ErrNoHost
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return nil, 0, "", err
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, 0, "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	return b, resp.StatusCode, resp.Header.Get("Content-Type"), err
}

// getJSON asks for JSON both ways (query and Accept header).
func (c *Client) getJSON(ctx context.Context, path string) ([]byte, string, error) {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	b, status, ct, err := c.get(ctx, c.hc, path+sep+"format=json", "application/json")
	if err != nil {
		return nil, "", err
	}
	switch status {
	case http.StatusOK:
		return b, ct, nil
	case http.StatusNotFound:
		return nil, ct, fmt.Errorf("%w: %s not served", host.ErrUnsupportedShape, path)
	case http.StatusForbidden:
		return nil, ct, fmt.Errorf("debug endpoint %s refused access (headscale only serves debug to loopback/private addresses)", path)
	}
	return nil, ct, fmt.Errorf("%s: HTTP %d", path, status)
}

func looksHTML(b []byte, ct string) bool {
	t := strings.TrimSpace(strings.ToLower(string(b[:min(len(b), 512)])))
	return strings.Contains(ct, "html") || strings.HasPrefix(t, "<!doctype") || strings.HasPrefix(t, "<html")
}

// --- registration cache -----------------------------------------------------

// CacheSummary is what headscale 0.29.6 and main expose at
// /debug/registration-cache: counters only, no entries.
type CacheSummary struct {
	Type       string `json:"type"`
	Expiration string `json:"expiration"`
	MaxEntries int    `json:"max_entries"`
	CurrentLen int    `json:"current_len"`
	Status     string `json:"status"`
}

// RegistrationCacheSummary returns the cache counters.
func (c *Client) RegistrationCacheSummary(ctx context.Context) (CacheSummary, error) {
	b, ct, err := c.getJSON(ctx, "/debug/registration-cache")
	if err != nil {
		return CacheSummary{}, err
	}
	return ParseCacheSummary(b, ct)
}

// ParseCacheSummary parses the summary object.
func ParseCacheSummary(b []byte, ct string) (CacheSummary, error) {
	var m map[string]json.RawMessage
	if looksHTML(b, ct) || json.Unmarshal(b, &m) != nil {
		return CacheSummary{}, host.ErrUnsupportedShape
	}
	if _, ok := m["current_len"]; !ok {
		return CacheSummary{}, host.ErrUnsupportedShape
	}
	var s CacheSummary
	if json.Unmarshal(b, &s) != nil {
		return CacheSummary{}, host.ErrUnsupportedShape
	}
	return s, nil
}

// RegistrationCache returns pending registrations. Released headscale
// versions (0.29.6, main as of 2026-10) only expose counters, which yields
// ErrUnsupportedShape so the pending-queue auto-fill turns itself off.
func (c *Client) RegistrationCache(ctx context.Context) ([]host.RegistrationEntry, error) {
	b, ct, err := c.getJSON(ctx, "/debug/registration-cache")
	if err != nil {
		return nil, err
	}
	return ParseRegistrationCache(b, ct)
}

// ParseRegistrationCache accepts a JSON array of entries, or an object
// holding one under "entries"/"items"/"registrations"/"cache" (as a list
// or an auth-ID-keyed map).
func ParseRegistrationCache(b []byte, ct string) (out []host.RegistrationEntry, err error) {
	defer func() {
		if r := recover(); r != nil {
			out, err = nil, host.ErrUnsupportedShape
		}
	}()
	if looksHTML(b, ct) {
		return nil, host.ErrUnsupportedShape
	}
	var v any
	if json.Unmarshal(b, &v) != nil {
		return nil, host.ErrUnsupportedShape
	}
	var list []any
	switch x := v.(type) {
	case []any:
		list = x
	case map[string]any:
		for _, k := range []string{"entries", "items", "registrations", "cache", "pending"} {
			switch e := x[k].(type) {
			case []any:
				list = e
			case map[string]any:
				for id, val := range e {
					if obj, ok := val.(map[string]any); ok {
						if _, has := obj["auth_id"]; !has {
							obj["auth_id"] = id
						}
						list = append(list, obj)
					}
				}
			}
			if list != nil {
				break
			}
		}
		if list == nil {
			if _, summary := x["current_len"]; summary {
				return nil, fmt.Errorf("%w: registration cache exposes counters only (%v entries)", host.ErrUnsupportedShape, x["current_len"])
			}
			return nil, host.ErrUnsupportedShape
		}
	default:
		return nil, host.ErrUnsupportedShape
	}
	out = []host.RegistrationEntry{}
	for _, it := range list {
		obj, ok := it.(map[string]any)
		if !ok {
			return nil, host.ErrUnsupportedShape
		}
		e := host.RegistrationEntry{
			AuthID:   firstString(obj, "auth_id", "authId", "AuthID", "id", "key", "registration_id"),
			Hostname: firstString(obj, "hostname", "Hostname", "name", "node_name"),
		}
		if node, ok := obj["node"].(map[string]any); ok && e.Hostname == "" {
			e.Hostname = firstString(node, "hostname", "Hostname", "name", "given_name")
		}
		for _, k := range []string{"requested_at", "created_at", "registered_at", "time", "created"} {
			if s, ok := obj[k].(string); ok {
				if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
					e.RequestedAt = t
					break
				}
			}
		}
		if e.AuthID == "" {
			return nil, host.ErrUnsupportedShape
		}
		out = append(out, e)
	}
	slices.SortFunc(out, func(a, b host.RegistrationEntry) int { return a.RequestedAt.Compare(b.RequestedAt) })
	return out, nil
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		switch v := m[k].(type) {
		case string:
			if v != "" {
				return v
			}
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64)
		}
	}
	return ""
}

// --- DERP map ---------------------------------------------------------------

// DERPMap fetches the server's current DERP map from /debug/derp. The body
// is returned raw with its content type; internal/host/derpmap parses it.
func (c *Client) DERPMap(ctx context.Context) ([]byte, string, error) {
	return c.getJSON(ctx, "/debug/derp")
}

// --- routes -----------------------------------------------------------------

// Routes returns the primary/HA state per prefix.
func (c *Client) Routes(ctx context.Context) ([]host.RouteState, error) {
	b, ct, err := c.getJSON(ctx, "/debug/routes")
	if err != nil {
		return nil, err
	}
	return ParseRoutes(b, ct)
}

// ParseRoutes parses types.DebugRoutes JSON
// ({"available_routes":{"<id>":["<prefix>"]},"primary_routes":{"<prefix>":<id>}})
// or the text form ("<prefix>: <id>" per line).
func ParseRoutes(b []byte, ct string) (out []host.RouteState, err error) {
	defer func() {
		if r := recover(); r != nil {
			out, err = nil, host.ErrUnsupportedShape
		}
	}()
	if looksHTML(b, ct) {
		return nil, host.ErrUnsupportedShape
	}
	t := strings.TrimSpace(string(b))
	if strings.HasPrefix(t, "{") {
		return parseRoutesJSON(b)
	}
	return parseRoutesText(t)
}

func parseRoutesJSON(b []byte) ([]host.RouteState, error) {
	var raw struct {
		Available map[string][]string `json:"available_routes"`
		Primary   map[string]uint64   `json:"primary_routes"`
	}
	var probe map[string]json.RawMessage
	if json.Unmarshal(b, &probe) != nil {
		return nil, host.ErrUnsupportedShape
	}
	if _, ok := probe["primary_routes"]; !ok {
		return nil, host.ErrUnsupportedShape
	}
	if json.Unmarshal(b, &raw) != nil {
		return nil, host.ErrUnsupportedShape
	}
	byPrefix := map[netip.Prefix]*host.RouteState{}
	get := func(p netip.Prefix) *host.RouteState {
		if r, ok := byPrefix[p]; ok {
			return r
		}
		r := &host.RouteState{Prefix: p}
		byPrefix[p] = r
		return r
	}
	for ps, id := range raw.Primary {
		p, err := netip.ParsePrefix(ps)
		if err != nil {
			return nil, host.ErrUnsupportedShape
		}
		get(p).Primary = id
	}
	for ids, prefixes := range raw.Available {
		id, err := strconv.ParseUint(ids, 10, 64)
		if err != nil {
			return nil, host.ErrUnsupportedShape
		}
		for _, ps := range prefixes {
			p, err := netip.ParsePrefix(ps)
			if err != nil {
				return nil, host.ErrUnsupportedShape
			}
			r := get(p)
			if !slices.Contains(r.Candidates, id) {
				r.Candidates = append(r.Candidates, id)
			}
		}
	}
	return sortRoutes(byPrefix), nil
}

func parseRoutesText(t string) ([]host.RouteState, error) {
	byPrefix := map[netip.Prefix]*host.RouteState{}
	for _, l := range strings.Split(t, "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		ps, ids, ok := strings.Cut(l, ": ")
		if !ok {
			return nil, host.ErrUnsupportedShape
		}
		p, err := netip.ParsePrefix(strings.TrimSpace(ps))
		if err != nil {
			return nil, host.ErrUnsupportedShape
		}
		id, err := strconv.ParseUint(strings.TrimSpace(ids), 10, 64)
		if err != nil {
			return nil, host.ErrUnsupportedShape
		}
		byPrefix[p] = &host.RouteState{Prefix: p, Primary: id, Candidates: []uint64{id}}
	}
	return sortRoutes(byPrefix), nil
}

func sortRoutes(m map[netip.Prefix]*host.RouteState) []host.RouteState {
	out := make([]host.RouteState, 0, len(m))
	for _, r := range m {
		slices.Sort(r.Candidates)
		// Primary first among candidates.
		if i := slices.Index(r.Candidates, r.Primary); i > 0 {
			r.Candidates = append([]uint64{r.Primary}, slices.Delete(r.Candidates, i, i+1)...)
		}
		out = append(out, *r)
	}
	slices.SortFunc(out, func(a, b host.RouteState) int { return a.Prefix.Compare(b.Prefix) })
	return out
}

// --- ping -------------------------------------------------------------------

// ErrPingFailed means the node did not answer.
var ErrPingFailed = errors.New("ping failed")

var (
	pongRE    = regexp.MustCompile(`Node (\d+) responded in ([0-9.]+(?:ns|us|µs|ms|s|m|h)(?:[0-9.]+(?:ns|us|µs|ms|s))*)`)
	timeoutRE = regexp.MustCompile(`Node (\d+) did not respond\.\s*([^<]*)`)
	pingErrRE = regexp.MustCompile(`(No node specified\.|Node "[^"<]*" not found\.|Node \d+ is not connected\.)`)
)

// Ping asks headscale to ping a node (GET /debug/ping?node=<id>, HTML in
// 0.29.6 and main) and returns the round trip.
func (c *Client) Ping(ctx context.Context, nodeID uint64) (time.Duration, error) {
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	b, status, _, err := c.get(ctx, c.pingHC, "/debug/ping?node="+strconv.FormatUint(nodeID, 10), "")
	if err != nil {
		return 0, err
	}
	if status == http.StatusNotFound {
		return 0, fmt.Errorf("%w: /debug/ping not served", host.ErrUnsupportedShape)
	}
	if status != http.StatusOK {
		return 0, fmt.Errorf("/debug/ping: HTTP %d", status)
	}
	return ParsePing(b, nodeID)
}

// ParsePing extracts the result from the ping page.
func ParsePing(b []byte, nodeID uint64) (time.Duration, error) {
	s := string(b)
	if m := pongRE.FindStringSubmatch(s); m != nil {
		if id, _ := strconv.ParseUint(m[1], 10, 64); id == nodeID {
			d, err := time.ParseDuration(strings.ReplaceAll(m[2], "us", "µs"))
			if err == nil {
				return d, nil
			}
		}
		return 0, host.ErrUnsupportedShape
	}
	if m := timeoutRE.FindStringSubmatch(s); m != nil {
		return 0, fmt.Errorf("%w: node %s did not respond %s", ErrPingFailed, m[1], strings.TrimSpace(m[2]))
	}
	if m := pingErrRE.FindStringSubmatch(s); m != nil {
		return 0, fmt.Errorf("%w: %s", ErrPingFailed, strings.ReplaceAll(m[1], "&#34;", `"`))
	}
	return 0, host.ErrUnsupportedShape
}

// --- metrics ----------------------------------------------------------------

// Metrics scrapes /metrics.
func (c *Client) Metrics(ctx context.Context) (host.MetricsSample, error) {
	b, status, _, err := c.get(ctx, c.hc, "/metrics", "text/plain")
	if err != nil {
		return host.MetricsSample{}, err
	}
	if status != http.StatusOK {
		return host.MetricsSample{}, fmt.Errorf("/metrics: HTTP %d", status)
	}
	s, err := ParseMetrics(b)
	s.At = c.now()
	return s, err
}

// ParseMetrics reads the series of interest from Prometheus text format.
func ParseMetrics(b []byte) (s host.MetricsSample, err error) {
	defer func() {
		if r := recover(); r != nil {
			s, err = host.MetricsSample{}, host.ErrUnsupportedShape
		}
	}()
	p := expfmt.NewTextParser(model.UTF8Validation)
	fams, perr := p.TextToMetricFamilies(strings.NewReader(string(b)))
	if perr != nil {
		return host.MetricsSample{}, fmt.Errorf("%w: %v", host.ErrUnsupportedShape, perr)
	}
	sum := func(name string) (float64, bool) {
		f, ok := fams[name]
		if !ok {
			return 0, false
		}
		var t float64
		for _, m := range f.GetMetric() {
			switch {
			case m.Gauge != nil:
				t += m.GetGauge().GetValue()
			case m.Counter != nil:
				t += m.GetCounter().GetValue()
			case m.Untyped != nil:
				t += m.GetUntyped().GetValue()
			}
		}
		return t, true
	}
	var found bool
	var ok bool
	if s.Nodes, ok = sum("headscale_nodestore_nodes"); ok {
		found = true
	}
	if s.MapResponsesTotal, ok = sum("headscale_mapresponse_sent_total"); ok {
		found = true
	}
	if s.HeapBytes, ok = sum("go_memstats_heap_inuse_bytes"); ok {
		found = true
	}
	if s.Goroutines, ok = sum("go_goroutines"); ok {
		found = true
	}
	if !found {
		return s, host.ErrUnsupportedShape
	}
	return s, nil
}
