// Package store is the app's cached snapshot of server state. It is only
// read and written from the Bubble Tea update loop, so it needs no locking;
// fetching happens in commands that return a Snapshot to apply.
package store

import (
	"context"
	"sort"
	"time"

	"github.com/tonhe/headsup/internal/api"
	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/host/debug"
)

// Slice identifies a part of the snapshot for targeted refresh.
type Slice uint8

const (
	SliceNodes Slice = 1 << iota
	SliceUsers
	SlicePreAuthKeys
	SliceAPIKeys
	SlicePolicy
	SliceHost
	SliceAll = SliceNodes | SliceUsers | SlicePreAuthKeys | SliceAPIKeys | SliceHost
)

// Snapshot is the result of one fetch.
type Snapshot struct {
	Slices        Slice
	Nodes         []api.Node
	Users         []api.User
	PreAuthKeys   []api.PreAuthKey
	APIKeys       []api.APIKey
	Policy        *api.Policy
	Extras        map[uint64]host.NodeExtras
	Routes        []host.RouteState
	Registrations []host.RegistrationEntry
	// RegCount is the server's count of pending logins when it can count
	// them but not list them (real headscale's debug cache); -1 if unknown.
	RegCount int
	Metrics  *host.MetricsSample
	Service  *host.ServiceStatus
	Errs     map[Slice]error
	At       time.Time
}

// UpdatedMsg is broadcast to modules after the store applied new data.
type UpdatedMsg struct{ Slices Slice }

// Store holds the latest snapshot plus history for sparklines.
type Store struct {
	Info          api.ServerInfo
	Nodes         []api.Node
	Users         []api.User
	PreAuthKeys   []api.PreAuthKey
	APIKeys       []api.APIKey
	Policy        *api.Policy
	Extras        map[uint64]host.NodeExtras
	Routes        []host.RouteState
	Registrations []host.RegistrationEntry
	RegCount      int                 // pending logins the server counted but could not list; 0 if none or unknown
	Service       *host.ServiceStatus // nil when there is no service manager or it could not be read
	Metrics       Ring
	OnlineHistory Ring
	LastErr       error
	UpdatedAt     time.Time
	Loaded        Slice
	Stale         bool
}

// New returns an empty store.
func New() *Store {
	return &Store{Metrics: NewRing(120), OnlineHistory: NewRing(120)}
}

// Apply merges a snapshot. Slices that errored keep their previous data.
func (s *Store) Apply(sn Snapshot) {
	ok := func(sl Slice) bool { return sn.Slices&sl != 0 && sn.Errs[sl] == nil }
	if ok(SliceNodes) {
		s.Nodes = sn.Nodes
		sort.SliceStable(s.Nodes, func(i, j int) bool { return s.Nodes[i].ID < s.Nodes[j].ID })
		online := 0
		for _, n := range s.Nodes {
			if n.Online {
				online++
			}
		}
		s.OnlineHistory.Push(float64(online))
		s.Loaded |= SliceNodes
	}
	if ok(SliceUsers) {
		s.Users = sn.Users
		s.Loaded |= SliceUsers
	}
	if ok(SlicePreAuthKeys) {
		s.PreAuthKeys = sn.PreAuthKeys
		s.Loaded |= SlicePreAuthKeys
	}
	if ok(SliceAPIKeys) {
		s.APIKeys = sn.APIKeys
		s.Loaded |= SliceAPIKeys
	}
	if ok(SlicePolicy) {
		s.Policy = sn.Policy
		s.Loaded |= SlicePolicy
	}
	if sn.Slices&SliceHost != 0 {
		if sn.Extras != nil {
			s.Extras = sn.Extras
		}
		if sn.Routes != nil {
			s.Routes = sn.Routes
		}
		s.Registrations = sn.Registrations
		s.RegCount = max(0, sn.RegCount)
		if sn.Metrics != nil {
			s.Metrics.Push(sn.Metrics.MapResponsesTotal)
		}
		s.Service = sn.Service
	}
	s.LastErr = nil
	for _, e := range sn.Errs {
		if e != nil {
			s.LastErr = e
			break
		}
	}
	s.Stale = s.LastErr != nil && api.IsKind(s.LastErr, api.KindUnreachable)
	s.UpdatedAt = sn.At
}

// Node returns the node with id.
func (s *Store) Node(id uint64) (api.Node, bool) {
	for _, n := range s.Nodes {
		if n.ID == id {
			return n, true
		}
	}
	return api.Node{}, false
}

// NodesOf returns nodes owned by user name.
func (s *Store) NodesOf(user string) []api.Node {
	var out []api.Node
	for _, n := range s.Nodes {
		if n.User.Name == user {
			out = append(out, n)
		}
	}
	return out
}

// Extra returns host enrichment for a node, if any.
func (s *Store) Extra(id uint64) (host.NodeExtras, bool) {
	e, ok := s.Extras[id]
	return e, ok
}

// PrimaryFor reports whether node id is the primary for prefix, from host data.
func (s *Store) IsPrimary(id uint64, p string) (primary, known bool) {
	for _, r := range s.Routes {
		if r.Prefix.String() == p {
			return r.Primary == id, true
		}
	}
	return false, false
}

// Counts summarises the tailnet for the dashboard.
type Counts struct {
	Nodes, Online, Users               int
	PendingRoutes, ExitNodes           int
	ExpiringNodes, ExpiredNodes        int
	ExpiringKeys, PendingRegistrations int
}

// Counts computes summary counts as of now.
func (s *Store) Counts(now time.Time, warn time.Duration) Counts {
	c := Counts{Nodes: len(s.Nodes), Users: len(s.Users), PendingRegistrations: max(len(s.Registrations), s.RegCount)}
	for _, n := range s.Nodes {
		if n.Online {
			c.Online++
		}
		c.PendingRoutes += len(n.PendingRoutes())
		if n.IsExitNode() {
			c.ExitNodes++
		}
		if n.Expired(now) {
			c.ExpiredNodes++
		} else if n.ExpiresSoon(now, warn) {
			c.ExpiringNodes++
		}
	}
	for _, k := range s.PreAuthKeys {
		if k.State(now) == api.KeyActive && !k.Expiration.IsZero() && k.Expiration.Sub(now) < warn {
			c.ExpiringKeys++
		}
	}
	for _, k := range s.APIKeys {
		if !k.Expiration.IsZero() && k.Expiration.After(now) && k.Expiration.Sub(now) < warn {
			c.ExpiringKeys++
		}
	}
	return c
}

// Fetch loads the requested slices. It is safe to call from a tea.Cmd.
func Fetch(ctx context.Context, b api.Backend, h host.Host, slices Slice) Snapshot {
	sn := Snapshot{Slices: slices, Errs: map[Slice]error{}, At: time.Now(), RegCount: -1}
	call := func(sl Slice, f func(context.Context) error) {
		if slices&sl == 0 {
			return
		}
		c, cancel := api.WithTimeout(ctx)
		defer cancel()
		if err := f(c); err != nil {
			sn.Errs[sl] = err
		}
	}
	call(SliceNodes, func(c context.Context) (err error) { sn.Nodes, err = b.ListNodes(c, api.NodeFilter{}); return })
	call(SliceUsers, func(c context.Context) (err error) { sn.Users, err = b.ListUsers(c, api.UserFilter{}); return })
	call(SlicePreAuthKeys, func(c context.Context) (err error) {
		sn.PreAuthKeys, err = b.ListPreAuthKeys(c, api.KeyFilter{})
		return
	})
	call(SliceAPIKeys, func(c context.Context) (err error) { sn.APIKeys, err = b.ListAPIKeys(c); return })
	call(SlicePolicy, func(c context.Context) error {
		p, err := b.GetPolicy(c)
		if err == nil {
			sn.Policy = &p
		}
		return err
	})
	if h != nil && slices&SliceHost != 0 {
		c, cancel := api.WithTimeout(ctx)
		defer cancel()
		if slices&SliceNodes != 0 && sn.Errs[SliceNodes] == nil {
			ids := make([]uint64, len(sn.Nodes))
			for i, n := range sn.Nodes {
				ids[i] = n.ID
			}
			if ex, err := h.NodeExtras(c, ids); err == nil {
				sn.Extras = ex
			}
		}
		if d := h.Debug(); d != nil && d.Available() {
			if rs, err := d.Routes(c); err == nil {
				sn.Routes = rs
			}
			if regs, err := d.RegistrationCache(c); err == nil {
				sn.Registrations = regs
			} else if cs, ok := d.(regCounter); ok {
				// Real headscale exposes only the cache size, not its entries.
				if sum, err := cs.RegistrationCacheSummary(c); err == nil {
					sn.RegCount = sum.CurrentLen
				}
			}
		}
		if m, err := h.Metrics(c); err == nil {
			sn.Metrics = &m
		}
		if sm := h.Service(); sm != nil {
			if st, err := sm.Status(c); err == nil {
				sn.Service = &st
			}
		}
	}
	return sn
}

// regCounter is implemented by debug clients that can count, but not list,
// pending registrations.
type regCounter interface {
	RegistrationCacheSummary(context.Context) (debug.CacheSummary, error)
}

// Ring is a fixed-size float history for sparklines.
type Ring struct {
	buf  []float64
	next int
	full bool
}

// NewRing makes a ring of capacity n.
func NewRing(n int) Ring { return Ring{buf: make([]float64, n)} }

// Push appends v, evicting the oldest when full.
func (r *Ring) Push(v float64) {
	if len(r.buf) == 0 {
		return
	}
	r.buf[r.next] = v
	r.next = (r.next + 1) % len(r.buf)
	if r.next == 0 {
		r.full = true
	}
}

// Values returns samples oldest-first.
func (r Ring) Values() []float64 {
	if !r.full {
		return append([]float64(nil), r.buf[:r.next]...)
	}
	return append(append([]float64(nil), r.buf[r.next:]...), r.buf[:r.next]...)
}

// Rates converts a monotonically increasing counter history to per-sample deltas.
func Rates(vals []float64) []float64 {
	if len(vals) < 2 {
		return nil
	}
	out := make([]float64, len(vals)-1)
	for i := 1; i < len(vals); i++ {
		d := vals[i] - vals[i-1]
		if d < 0 {
			d = 0
		}
		out[i-1] = d
	}
	return out
}
