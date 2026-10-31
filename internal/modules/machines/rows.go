package machines

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/tonhe/headsup/internal/api"
	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/icons"
	"github.com/tonhe/headsup/internal/store"
	"github.com/tonhe/headsup/internal/ui/badge"
	"github.com/tonhe/headsup/internal/ui/humanize"
	"github.com/tonhe/headsup/internal/ui/styles"
	"github.com/tonhe/headsup/internal/ui/table"
)

// columns returns the machine table columns. Host-only columns are included
// only when host enrichment is available.
func columns(hostMode bool) []table.Column {
	cols := []table.Column{
		{Key: "status", Title: "", Width: 1, Priority: 0},
		{Key: "name", Title: "NAME", Width: 18, MinWidth: 10, Flex: true, Priority: 0},
		{Key: "user", Title: "USER", Width: 10, Priority: 2},
		{Key: "ip", Title: "IPV4", Width: 15, Priority: 1},
		{Key: "tags", Title: "TAGS", Width: 18, Flex: true, Priority: 4},
		{Key: "seen", Title: "LAST SEEN", Width: 9, Priority: 3},
		{Key: "expiry", Title: "EXPIRY", Width: 8, Priority: 5},
		{Key: "routes", Title: "ROUTES", Width: 7, Priority: 6},
	}
	if hostMode {
		cols = append(cols,
			table.Column{Key: "os", Title: "OS", Width: 8, Priority: 7},
			table.Column{Key: "client", Title: "CLIENT", Width: 8, Priority: 8},
			table.Column{Key: "derp", Title: "DERP", Width: 9, Priority: 9},
		)
	}
	return cols
}

// row adapts a node to the table and filter.
type row struct {
	n     api.Node
	extra *host.NodeExtras
	s     *styles.Styles
	ic    icons.Set
	now   time.Time
	warn  time.Duration
	text  string // FilterText, computed once per rebuild
	name  string // the name sort key
}

func newRows(st *store.Store, s *styles.Styles, ic icons.Set, now time.Time, warn time.Duration) []row {
	out := make([]row, 0, len(st.Nodes))
	for _, n := range st.Nodes {
		r := row{n: n, s: s, ic: ic, now: now, warn: warn}
		if e, ok := st.Extra(n.ID); ok {
			e := e
			r.extra = &e
		}
		r.text = r.filterText()
		r.name = strings.ToLower(n.DisplayName())
		out = append(out, r)
	}
	return out
}

func (r row) Key() string { return strconv.FormatUint(r.n.ID, 10) }

func (r row) statusGlyph() string {
	switch r.n.Status(r.now) {
	case api.StatusOnline:
		return r.s.Online.Render(r.ic.Online)
	case api.StatusIdle:
		return r.s.Idle.Render(r.ic.Idle)
	case api.StatusExpired:
		return r.s.Expired.Render(r.ic.Expired)
	default:
		return r.s.Offline.Render(r.ic.Offline)
	}
}

func (r row) Cell(col string) string {
	n := r.n
	switch col {
	case "status":
		return r.statusGlyph()
	case "name":
		name := n.DisplayName()
		if n.IsExitNode() {
			name += " " + r.s.Accent.Render(r.ic.Exit)
		}
		return name
	case "user":
		return n.User.Name
	case "ip":
		if v4 := firstV4(n); v4 != "" {
			return r.s.IP.Render(v4)
		}
		return r.s.Muted.Render("-")
	case "tags":
		if len(n.Tags) == 0 {
			return r.s.Muted.Render("-")
		}
		return badge.Tags(r.s, n.Tags, 40)
	case "seen":
		if n.Online {
			return r.s.Online.Render("online")
		}
		return humanize.Ago(n.LastSeen, r.now)
	case "expiry":
		switch {
		case n.ExpiryDisabled():
			return r.s.Muted.Render("never")
		case n.Expired(r.now):
			return r.s.Expired.Render("expired")
		case n.ExpiresSoon(r.now, r.warn):
			// The warning glyph marks "soon" without relying on colour (T171).
			return r.s.Expiring.Render(r.ic.Warn + " " + humanize.Short(n.Expiry.Sub(r.now)))
		default:
			return humanize.Short(n.Expiry.Sub(r.now))
		}
	case "routes":
		pend := len(n.PendingRoutes())
		total := len(n.AvailableRoutes)
		switch {
		case total == 0:
			return r.s.Muted.Render("-")
		case pend > 0:
			return r.s.Pending.Render(fmt.Sprintf("%s %d/%d", r.ic.Pending, total-pend, total))
		default:
			return r.s.OK.Render(fmt.Sprintf("%s %d", r.ic.Check, total))
		}
	case "os":
		if r.extra == nil {
			return ""
		}
		return r.extra.OS
	case "client":
		if r.extra == nil {
			return ""
		}
		return shortVersion(r.extra.ClientVersion)
	case "derp":
		if r.extra == nil {
			return ""
		}
		return derpLabel(*r.extra)
	}
	return ""
}

func (r row) SortValue(col string) string {
	n := r.n
	switch col {
	case "status":
		return strconv.Itoa(3 - int(n.Status(r.now)))
	case "name":
		if r.name != "" {
			return r.name
		}
		return strings.ToLower(n.DisplayName())
	case "user":
		return n.User.Name
	case "ip":
		if len(n.IPAddresses) > 0 {
			b := n.IPAddresses[0].As16()
			return fmt.Sprintf("%x", b)
		}
		return ""
	case "tags":
		return strings.Join(n.Tags, ",")
	case "seen":
		if n.Online {
			return "9999999999"
		}
		return fmt.Sprintf("%010d", n.LastSeen.Unix())
	case "expiry":
		if n.ExpiryDisabled() {
			return "9999999999"
		}
		return fmt.Sprintf("%010d", n.Expiry.Unix())
	case "routes":
		return fmt.Sprintf("%03d", len(n.PendingRoutes())*100+len(n.AvailableRoutes))
	default:
		return r.Cell(col)
	}
}

// FilterText is what free-text search matches: name, hostname, IPs, user, tags.
func (r row) FilterText() string {
	if r.text != "" {
		return r.text
	}
	return r.filterText()
}

func (r row) filterText() string {
	n := r.n
	parts := []string{n.GivenName, n.Name, n.User.Name}
	for _, ip := range n.IPAddresses {
		parts = append(parts, ip.String())
	}
	parts = append(parts, n.Tags...)
	if r.extra != nil {
		parts = append(parts, r.extra.OS, r.extra.Hostname)
	}
	return strings.ToLower(strings.Join(parts, " "))
}

func (r row) FilterField(key string) []string {
	n := r.n
	switch key {
	case "tag":
		out := make([]string, len(n.Tags))
		for i, t := range n.Tags {
			out[i] = strings.TrimPrefix(t, "tag:")
		}
		return append(out, n.Tags...)
	case "user":
		return []string{n.User.Name}
	case "name":
		return []string{n.GivenName, n.Name}
	case "ip":
		var out []string
		for _, ip := range n.IPAddresses {
			out = append(out, ip.String())
		}
		return out
	case "route":
		var out []string
		for _, p := range n.AvailableRoutes {
			out = append(out, p.String())
		}
		return out
	case "os":
		if r.extra != nil {
			return []string{strings.ToLower(r.extra.OS)}
		}
	case "id":
		return []string{r.Key()}
	}
	return nil
}

func (r row) FilterIs(state string) bool {
	n := r.n
	switch state {
	case "online":
		return n.Online
	case "offline":
		return !n.Online
	case "idle":
		return n.Status(r.now) == api.StatusIdle
	case "expired":
		return n.Expired(r.now)
	case "expiring":
		return n.ExpiresSoon(r.now, r.warn)
	case "exit":
		return n.IsExitNode()
	case "pending":
		return len(n.PendingRoutes()) > 0
	case "tagged":
		return len(n.Tags) > 0
	}
	return false
}

func firstV4(n api.Node) string {
	for _, ip := range n.IPAddresses {
		if ip.Is4() {
			return ip.String()
		}
	}
	return ""
}

func shortVersion(v string) string {
	if i := strings.IndexByte(v, '-'); i > 0 {
		return v[:i]
	}
	return v
}

func derpLabel(e host.NodeExtras) string {
	if e.PreferredDERP == 0 {
		return ""
	}
	best := ""
	bestMS := 0.0
	for k, ms := range e.DERPLatency {
		if best == "" || ms < bestMS {
			best, bestMS = k, ms
		}
	}
	if best != "" {
		return fmt.Sprintf("%d %.0fms", e.PreferredDERP, bestMS)
	}
	return strconv.Itoa(e.PreferredDERP)
}
