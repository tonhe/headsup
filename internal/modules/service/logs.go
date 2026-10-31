package service

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/icons"
	"github.com/tonhe/headsup/internal/store"
	"github.com/tonhe/headsup/internal/ui/kv"
	"github.com/tonhe/headsup/internal/ui/styles"
)

// RingSize is how many log lines the viewer keeps.
const RingSize = 10_000

// Ring is a fixed-capacity buffer of log entries addressed by a monotonic
// sequence number; pushing into a full ring drops the oldest entry.
type Ring struct {
	buf   []host.LogEntry
	start uint64 // sequence number of the oldest entry
	n     int
}

// NewRing returns a ring holding at most size entries.
func NewRing(size int) *Ring { return &Ring{buf: make([]host.LogEntry, max(1, size))} }

// Push appends e and returns its sequence number.
func (r *Ring) Push(e host.LogEntry) uint64 {
	seq := r.start + uint64(r.n)
	r.buf[seq%uint64(len(r.buf))] = e
	if r.n < len(r.buf) {
		r.n++
	} else {
		r.start++
	}
	return seq
}

// Len is the number of entries held.
func (r *Ring) Len() int { return r.n }

// Cap is the ring's capacity.
func (r *Ring) Cap() int { return len(r.buf) }

// First is the sequence number of the oldest entry held.
func (r *Ring) First() uint64 { return r.start }

// At returns the entry with sequence number seq, if still held.
func (r *Ring) At(seq uint64) (host.LogEntry, bool) {
	if seq < r.start || seq >= r.start+uint64(r.n) {
		return host.LogEntry{}, false
	}
	return r.buf[seq%uint64(len(r.buf))], true
}

// Viewer is the log viewer's state: a ring of every line received, plus the
// sequence numbers of the lines that pass the level filter and search. Only
// the visible window is ever rendered, so 10k lines cost no more to draw
// than 30.
type Viewer struct {
	ring     *Ring
	minLevel host.LogLevel
	query    string
	re       *regexp.Regexp
	reErr    error
	vis      []uint64
	cursor   int // index into vis
	top      int // first rendered index into vis
	follow   bool
	paused   bool
	held     []host.LogEntry
	expanded bool
}

// NewViewer returns a following viewer with a ring of size lines.
func NewViewer(size int) *Viewer { return &Viewer{ring: NewRing(size), follow: true} }

// Levels the filter cycles through.
var filterLevels = []host.LogLevel{host.LevelTrace, host.LevelDebug, host.LevelInfo, host.LevelWarn, host.LevelError}

// Append adds entries. While paused they are held back, not dropped.
func (v *Viewer) Append(es []host.LogEntry) {
	if v.paused {
		v.held = append(v.held, es...)
		if over := len(v.held) - v.ring.Cap(); over > 0 {
			v.held = slices.Delete(v.held, 0, over)
		}
		return
	}
	for _, e := range es {
		seq := v.ring.Push(e)
		if v.match(e) {
			v.vis = append(v.vis, seq)
		}
	}
	v.trim()
	if v.follow {
		v.cursor = max(0, len(v.vis)-1)
	}
}

// trim drops filtered sequence numbers whose lines left the ring.
func (v *Viewer) trim() {
	first := v.ring.First()
	i := sort.Search(len(v.vis), func(i int) bool { return v.vis[i] >= first })
	if i == 0 {
		return
	}
	v.vis = v.vis[i:]
	v.cursor = max(0, v.cursor-i)
	v.top = max(0, v.top-i)
}

func (v *Viewer) match(e host.LogEntry) bool {
	if e.Level < v.minLevel {
		return false
	}
	return v.re == nil || v.re.MatchString(plainLine(e))
}

// rebuild recomputes the filtered list, keeping the cursor near its line.
func (v *Viewer) rebuild() {
	var keep uint64
	hadCursor := v.cursor < len(v.vis)
	if hadCursor {
		keep = v.vis[v.cursor]
	}
	v.vis = v.vis[:0]
	for seq := v.ring.First(); ; seq++ {
		e, ok := v.ring.At(seq)
		if !ok {
			break
		}
		if v.match(e) {
			v.vis = append(v.vis, seq)
		}
	}
	switch {
	case v.follow || !hadCursor:
		v.cursor = max(0, len(v.vis)-1)
	default:
		i := sort.Search(len(v.vis), func(i int) bool { return v.vis[i] >= keep })
		v.cursor = min(i, max(0, len(v.vis)-1))
	}
}

// CycleLevel raises the minimum level, wrapping back to everything.
func (v *Viewer) CycleLevel() {
	i := slices.Index(filterLevels, v.minLevel)
	v.minLevel = filterLevels[(i+1)%len(filterLevels)]
	v.rebuild()
}

// SetMinLevel sets the minimum level shown.
func (v *Viewer) SetMinLevel(l host.LogLevel) { v.minLevel = l; v.rebuild() }

// MinLevel is the current minimum level.
func (v *Viewer) MinLevel() host.LogLevel { return v.minLevel }

// SetSearch filters to lines matching q, a case-insensitive regular
// expression. An invalid expression is matched literally and reported by
// SearchErr.
func (v *Viewer) SetSearch(q string) {
	v.query, v.re, v.reErr = q, nil, nil
	if q != "" {
		re, err := regexp.Compile("(?i)" + q)
		if err != nil {
			re, v.reErr = regexp.MustCompile("(?i)"+regexp.QuoteMeta(q)), err
		}
		v.re = re
	}
	v.rebuild()
}

// Search is the active search text.
func (v *Viewer) Search() string { return v.query }

// SearchErr is non-nil when the search is not a valid regular expression.
func (v *Viewer) SearchErr() error { return v.reErr }

// SetFollow pins the view to the newest line (or releases it).
func (v *Viewer) SetFollow(on bool) {
	v.follow = on
	if on {
		v.cursor = max(0, len(v.vis)-1)
	}
}

// Following reports whether the view is pinned to the newest line.
func (v *Viewer) Following() bool { return v.follow }

// SetPaused freezes the view; resuming releases every held line.
func (v *Viewer) SetPaused(on bool) {
	if v.paused == on {
		return
	}
	v.paused = on
	if !on {
		held := v.held
		v.held = nil
		v.Append(held)
	}
}

// Paused reports whether the view is frozen.
func (v *Viewer) Paused() bool { return v.paused }

// Held is how many lines that pass the filter arrived while paused.
func (v *Viewer) Held() int {
	n := 0
	for _, e := range v.held {
		if v.match(e) {
			n++
		}
	}
	return n
}

// Len is the number of lines that pass the filter.
func (v *Viewer) Len() int { return len(v.vis) }

// Total is the number of lines in the ring.
func (v *Viewer) Total() int { return v.ring.Len() }

// Move moves the cursor by d lines; moving up releases follow.
func (v *Viewer) Move(d int) {
	if len(v.vis) == 0 {
		return
	}
	v.cursor = min(max(0, v.cursor+d), len(v.vis)-1)
	if d < 0 {
		v.follow = false
	}
}

// Home moves to the oldest line.
func (v *Viewer) Home() { v.follow = false; v.cursor = 0 }

// Cursor returns the entry under the cursor.
func (v *Viewer) Cursor() (host.LogEntry, bool) {
	if v.cursor >= len(v.vis) {
		return host.LogEntry{}, false
	}
	return v.ring.At(v.vis[v.cursor])
}

// NextError moves the cursor to the next line at Error or above in
// direction dir (+1 or -1), wrapping. It reports whether one was found.
//
// From the newest line, "next" has nothing ahead of it, so it goes to the
// most recent error instead of wrapping to the oldest one.
func (v *Viewer) NextError(dir int) bool {
	n := len(v.vis)
	if dir > 0 && n > 0 && v.cursor == n-1 {
		dir = -1
	}
	for i := 1; i <= n; i++ {
		j := ((v.cursor+dir*i)%n + n) % n
		if e, ok := v.ring.At(v.vis[j]); ok && e.Level >= host.LevelError {
			v.cursor, v.follow = j, false
			return true
		}
	}
	return false
}

// ToggleExpanded shows or hides the cursor line's structured fields.
func (v *Viewer) ToggleExpanded() { v.expanded = !v.expanded }

// Expanded reports whether the detail pane is open.
func (v *Viewer) Expanded() bool { return v.expanded }

// Window returns the entries to draw in h rows and the cursor's row in it.
func (v *Viewer) Window(h int) ([]host.LogEntry, int) {
	if h <= 0 || len(v.vis) == 0 {
		return nil, -1
	}
	switch {
	case v.follow:
		v.top = max(0, len(v.vis)-h)
	case v.cursor < v.top:
		v.top = v.cursor
	case v.cursor >= v.top+h:
		v.top = v.cursor - h + 1
	}
	v.top = min(v.top, max(0, len(v.vis)-h))
	end := min(len(v.vis), v.top+h)
	out := make([]host.LogEntry, 0, end-v.top)
	for _, seq := range v.vis[v.top:end] {
		e, _ := v.ring.At(seq)
		out = append(out, e)
	}
	return out, v.cursor - v.top
}

// --- rendering --------------------------------------------------------------

func sortedKeys(m map[string]string) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func fieldText(v string) string {
	if v == "" || strings.ContainsAny(v, " \t\"") {
		return strconv.Quote(v)
	}
	return v
}

// plainLine is the searchable text of an entry: level, message and fields.
func plainLine(e host.LogEntry) string {
	var b strings.Builder
	b.WriteString(e.Level.String())
	b.WriteByte(' ')
	b.WriteString(e.Msg)
	for _, k := range sortedKeys(e.Fields) {
		b.WriteString(" " + k + "=" + fieldText(e.Fields[k]))
	}
	return b.String()
}

func levelStyle(s *styles.Styles, l host.LogLevel) lipgloss.Style {
	switch {
	case l >= host.LevelError:
		return s.Danger
	case l == host.LevelWarn:
		return s.Warn
	case l == host.LevelInfo:
		return s.Accent
	}
	return s.Muted
}

func stamp(t time.Time, loc *time.Location) string {
	if t.IsZero() {
		return "--:--:--"
	}
	return t.In(loc).Format("15:04:05")
}

// renderLine draws one entry in w cells. The cursor line is drawn plain in
// the cursor style so the highlight spans the whole row.
func renderLine(s *styles.Styles, ic icons.Set, e host.LogEntry, loc *time.Location, w int, cursor bool) string {
	if cursor {
		plain := stamp(e.Time, loc) + " " + plainLine(e)
		return s.RowCursor.Width(w).MaxWidth(w).Render(ansi.Truncate(plain, w, ic.Ellipsis))
	}
	var b strings.Builder
	b.WriteString(s.Muted.Render(stamp(e.Time, loc)))
	b.WriteByte(' ')
	b.WriteString(levelStyle(s, e.Level).Render(e.Level.String()))
	b.WriteByte(' ')
	b.WriteString(s.Text.Render(e.Msg))
	for _, k := range sortedKeys(e.Fields) {
		b.WriteString(" " + s.Muted.Render(k+"=") + s.Text.Render(fieldText(e.Fields[k])))
	}
	return ansi.Truncate(b.String(), w, ic.Ellipsis)
}

// nodeRefs are the machines a line mentions: parsed node IDs, then any field
// or message token that is a known machine's IP address or name.
func nodeRefs(e host.LogEntry, st *store.Store) []uint64 {
	out := slices.Clone(e.NodeRefs)
	if st == nil || len(st.Nodes) == 0 {
		return out
	}
	idx := map[string]uint64{}
	for _, n := range st.Nodes {
		for _, ip := range n.IPAddresses {
			idx[ip.String()] = n.ID
		}
		if n.Name != "" {
			idx[n.Name] = n.ID
		}
		if n.GivenName != "" {
			idx[n.GivenName] = n.ID
		}
	}
	add := func(tok string) {
		tok = strings.Trim(tok, `"',;:()[]`)
		if host, _, ok := strings.Cut(tok, ":"); ok && strings.Count(tok, ":") == 1 {
			tok = host // ip:port
		}
		if id, ok := idx[tok]; ok && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	for _, k := range sortedKeys(e.Fields) {
		add(e.Fields[k])
	}
	for _, tok := range strings.Fields(e.Msg) {
		add(tok)
	}
	return out
}

// detailPairs lists the cursor line's full structure.
func detailPairs(s *styles.Styles, e host.LogEntry, st *store.Store, loc *time.Location) []kv.Pair {
	ts := "-"
	if !e.Time.IsZero() {
		ts = e.Time.In(loc).Format(time.RFC3339)
	}
	pairs := []kv.Pair{
		{Key: "Time", Value: ts},
		{Key: "Level", Value: levelStyle(s, e.Level).Render(e.Level.String())},
		{Key: "Message", Value: e.Msg},
	}
	for _, k := range sortedKeys(e.Fields) {
		pairs = append(pairs, kv.Pair{Key: k, Value: e.Fields[k]})
	}
	for _, id := range nodeRefs(e, st) {
		name := fmt.Sprintf("node %d", id)
		if n, ok := st.Node(id); ok {
			name = n.DisplayName()
		}
		pairs = append(pairs, kv.Pair{Key: "Machine", Value: s.Link.Render(name) + s.Muted.Render(fmt.Sprintf(" (#%d, m to open)", id))})
	}
	return pairs
}

// --- streaming --------------------------------------------------------------

// logMsg carries a batch of entries from stream id.
type logMsg struct {
	id      int
	entries []host.LogEntry
	closed  bool
}

// logErrMsg reports that stream id could not start.
type logErrMsg struct {
	id  int
	err error
}

// Batching: after the first entry arrives, keep collecting until the stream
// is quiet for batchQuiet or batchMax has passed, so a burst (the initial
// tail) lands in one update and a busy server cannot flood the UI.
var (
	batchQuiet = 15 * time.Millisecond
	batchMax   = 100 * time.Millisecond
	batchLimit = 2000
)

// readBatch waits for the next batch from ch.
func readBatch(id int, ch <-chan host.LogEntry) tea.Cmd {
	return func() tea.Msg {
		e, ok := <-ch
		if !ok {
			return logMsg{id: id, closed: true}
		}
		out := []host.LogEntry{e}
		deadline := time.NewTimer(batchMax)
		defer deadline.Stop()
		for len(out) < batchLimit {
			quiet := time.NewTimer(batchQuiet)
			select {
			case e, ok := <-ch:
				quiet.Stop()
				if !ok {
					return logMsg{id: id, entries: out, closed: true}
				}
				out = append(out, e)
				continue
			case <-quiet.C:
			case <-deadline.C:
				quiet.Stop()
			}
			break
		}
		return logMsg{id: id, entries: out}
	}
}

// openStream starts following h's logs as stream id.
func openStream(ctx context.Context, h host.Host, id int) tea.Cmd {
	return func() tea.Msg {
		ch, err := h.Logs(ctx, host.LogQuery{Follow: true, Tail: 500})
		if err != nil {
			return logErrMsg{id: id, err: err}
		}
		return streamMsg{id: id, ch: ch}
	}
}

// streamMsg hands the module an opened stream.
type streamMsg struct {
	id int
	ch <-chan host.LogEntry
}
