// Package watch diffs successive store snapshots into events: a machine came
// online or went offline, a key entered its expiry warning window, a machine
// advertised a new route. headscale has no admin event stream, so these are
// worked out from polling (research R12). The first observation is the
// baseline and emits nothing.
package watch

import (
	"fmt"
	"net/netip"
	"sort"
	"time"

	"github.com/tonhe/headsup/internal/api"
	"github.com/tonhe/headsup/internal/store"
)

// Kind names an event type. The values are the words used in [notify].events.
type Kind string

const (
	NodeOnline      Kind = "node_online"
	NodeOffline     Kind = "node_offline"
	KeyExpiring     Kind = "key_expiring"
	RouteAdvertised Kind = "route_advertised"
)

// Kinds lists every event kind.
var Kinds = []Kind{NodeOnline, NodeOffline, KeyExpiring, RouteAdvertised}

// Event is one observed change.
type Event struct {
	Kind    Kind      `json:"kind"`
	At      time.Time `json:"at"`
	Subject string    `json:"subject"`          // "nas-01", "pre-auth key 7", "nas-01 10.0.0.0/24"
	ID      string    `json:"id,omitempty"`     // machine or key ID
	Detail  string    `json:"detail,omitempty"` // "expires in 3d", "machine", ...
}

// Text is a one-line, human description.
func (e Event) Text() string {
	switch e.Kind {
	case NodeOnline:
		return e.Subject + " came online"
	case NodeOffline:
		return e.Subject + " went offline"
	case KeyExpiring:
		if e.Detail != "" {
			return e.Subject + " " + e.Detail
		}
		return e.Subject + " expires soon"
	case RouteAdvertised:
		return e.Subject + " advertised " + e.Detail
	}
	return e.Subject
}

// EventsMsg is broadcast to modules when a refresh produced events.
type EventsMsg struct{ Events []Event }

// Watcher remembers the previous state. The zero value is not usable; use New.
type Watcher struct {
	warn time.Duration

	nodesSeen bool
	online    map[uint64]bool
	routes    map[uint64]map[netip.Prefix]bool

	keysSeen bool
	expiring map[string]bool // key subject → was inside the warning window
}

// New returns a watcher that warns about keys expiring within warn.
func New(warn time.Duration) *Watcher {
	if warn <= 0 {
		warn = 7 * 24 * time.Hour
	}
	return &Watcher{warn: warn}
}

// Observe diffs st against the previous observation. slices says which parts
// of st were refreshed; the others keep their previous baseline.
func (w *Watcher) Observe(st *store.Store, slices store.Slice, now time.Time) []Event {
	var out []Event
	if slices&store.SliceNodes != 0 && st.Loaded&store.SliceNodes != 0 {
		out = append(out, w.nodes(st.Nodes, now)...)
	}
	keySlices := store.SliceNodes | store.SlicePreAuthKeys | store.SliceAPIKeys
	if slices&keySlices != 0 && st.Loaded&store.SliceNodes != 0 {
		out = append(out, w.keys(st, now)...)
	}
	return out
}

func (w *Watcher) nodes(nodes []api.Node, now time.Time) []Event {
	online := make(map[uint64]bool, len(nodes))
	routes := make(map[uint64]map[netip.Prefix]bool, len(nodes))
	var out []Event
	for _, n := range nodes {
		online[n.ID] = n.Online
		rs := make(map[netip.Prefix]bool, len(n.AvailableRoutes))
		for _, p := range n.AvailableRoutes {
			rs[p] = true
		}
		routes[n.ID] = rs
		if !w.nodesSeen {
			continue
		}
		id := fmt.Sprint(n.ID)
		was, known := w.online[n.ID]
		switch {
		case n.Online && (!known || !was):
			out = append(out, Event{Kind: NodeOnline, At: now, Subject: n.DisplayName(), ID: id})
		case !n.Online && known && was:
			out = append(out, Event{Kind: NodeOffline, At: now, Subject: n.DisplayName(), ID: id})
		}
		prev := w.routes[n.ID]
		var added []netip.Prefix
		for p := range rs {
			if !prev[p] {
				added = append(added, p)
			}
		}
		sort.Slice(added, func(i, j int) bool { return added[i].String() < added[j].String() })
		for _, p := range added {
			out = append(out, Event{Kind: RouteAdvertised, At: now, Subject: n.DisplayName(), ID: id, Detail: p.String()})
		}
	}
	w.online, w.routes, w.nodesSeen = online, routes, true
	return out
}

// keyState is one thing that expires.
type keyState struct {
	subject, id string
	at          time.Time
}

func (w *Watcher) keys(st *store.Store, now time.Time) []Event {
	var ks []keyState
	for _, n := range st.Nodes {
		if !n.ExpiryDisabled() {
			ks = append(ks, keyState{subject: "machine " + n.DisplayName(), id: fmt.Sprint(n.ID), at: *n.Expiry})
		}
	}
	for _, k := range st.PreAuthKeys {
		if k.State(now) == api.KeyActive && !k.Expiration.IsZero() {
			ks = append(ks, keyState{subject: fmt.Sprintf("pre-auth key %d", k.ID), id: fmt.Sprint(k.ID), at: k.Expiration})
		}
	}
	for _, k := range st.APIKeys {
		if !k.Expiration.IsZero() {
			ks = append(ks, keyState{subject: "API key " + k.Prefix, id: fmt.Sprint(k.ID), at: k.Expiration})
		}
	}
	exp := make(map[string]bool, len(ks))
	var out []Event
	for _, k := range ks {
		left := k.at.Sub(now)
		if left <= 0 || left >= w.warn {
			continue
		}
		exp[k.subject] = true
		if w.keysSeen && !w.expiring[k.subject] {
			out = append(out, Event{Kind: KeyExpiring, At: now, Subject: k.subject, ID: k.id, Detail: "expires in " + approx(left)})
		}
	}
	w.expiring, w.keysSeen = exp, true
	return out
}

// approx renders d as whole days, hours or minutes.
func approx(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	case d >= time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	}
	return fmt.Sprintf("%dm", max(1, int(d/time.Minute)))
}

// Filter keeps the events whose kind is in kinds. An empty kinds keeps none.
func Filter(evs []Event, kinds []string) []Event {
	want := map[string]bool{}
	for _, k := range kinds {
		want[k] = true
	}
	var out []Event
	for _, e := range evs {
		if want[string(e.Kind)] {
			out = append(out, e)
		}
	}
	return out
}
