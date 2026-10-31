// Package activity is the Activity module: a filterable view of the local
// log of mutations this app made (API calls, host file writes, service
// actions), next to the live feed of watch events (machines coming online or
// going offline, keys about to expire, new routes), with export to JSON.
package activity

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	alog "github.com/tonhe/headsup/internal/activity"
	"github.com/tonhe/headsup/internal/brand"
	"github.com/tonhe/headsup/internal/icons"
	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/modules/registry"
	"github.com/tonhe/headsup/internal/store"
	"github.com/tonhe/headsup/internal/ui/pane"
	"github.com/tonhe/headsup/internal/ui/table"
	"github.com/tonhe/headsup/internal/ui/tabs"
	"github.com/tonhe/headsup/internal/watch"
)

func init() { registry.Register(func() module.Module { return New() }) }

// Source is implemented by the app's Context to hand this page the
// activity logger. Without it the log tab is empty.
type Source interface {
	Activity() alog.Logger
}

// Limits on what the page keeps in memory.
const (
	MaxEntries = 1000
	MaxEvents  = 500
)

type tab int

const (
	tabLog tab = iota
	tabEvents
)

// Model is the activity module.
type Model struct {
	ic  icons.Set
	tab tab

	entries []alog.Entry  // newest first
	events  []watch.Event // newest first
	loadErr error
	loaded  bool

	log, feed *table.Model

	in        textinput.Model
	filtering bool
	query     string

	// home overrides the export directory default (tests).
	home string
}

// New returns the module.
func New() *Model {
	in := textinput.New()
	in.Prompt = ""
	in.Placeholder = "action, target, profile, or result"
	return &Model{ic: icons.Unicode, in: in}
}

func (m *Model) ID() string                { return "activity" }
func (m *Model) Title() string             { return "Activity" }
func (m *Model) Icon(ic icons.Set) string  { return ic.Activity }
func (m *Model) Order() int                { return 90 }
func (m *Model) Modes() module.Modes       { return module.ModeLocal }
func (m *Model) Chord() string             { return "v" }
func (m *Model) Capturing() bool           { return m.filtering }
func (m *Model) Badge(*store.Store) string { return "" }

// --- messages -----------------------------------------------------------------

type loadedMsg struct {
	entries []alog.Entry
	err     error
}

func (m *Model) load(ctx module.Context) tea.Cmd {
	src, ok := ctx.(Source)
	if !ok || src.Activity() == nil {
		return nil
	}
	l := src.Activity()
	return func() tea.Msg {
		es, err := l.Recent(MaxEntries)
		return loadedMsg{entries: es, err: err}
	}
}

func (m *Model) Init(ctx module.Context) tea.Cmd { return m.load(ctx) }

func (m *Model) Update(ctx module.Context, msg tea.Msg) (module.Module, tea.Cmd) {
	m.ic = ctx.Icons()
	switch msg := msg.(type) {
	case loadedMsg:
		m.entries, m.loadErr, m.loaded = msg.entries, msg.err, true
		m.rebuild(ctx)
	case module.ActionResult:
		// The app logs before it broadcasts the result, so the new entry is there.
		return m, m.load(ctx)
	case module.NavigateMsg:
		return m, m.load(ctx)
	case watch.EventsMsg:
		evs := make([]watch.Event, 0, len(msg.Events)+len(m.events))
		for i := len(msg.Events) - 1; i >= 0; i-- {
			evs = append(evs, msg.Events[i])
		}
		m.events = append(evs, m.events...)
		if len(m.events) > MaxEvents {
			m.events = m.events[:MaxEvents]
		}
		m.rebuild(ctx)
	case tea.KeyMsg:
		return m, m.key(ctx, msg)
	default:
		if m.filtering {
			var cmd tea.Cmd
			m.in, cmd = m.in.Update(msg)
			return m, cmd
		}
	}
	return m, nil
}

func (m *Model) key(ctx module.Context, k tea.KeyMsg) tea.Cmd {
	if m.filtering {
		switch k.String() {
		case "esc":
			m.in.SetValue("")
			m.query, m.filtering = "", false
			m.in.Blur()
		case "enter":
			m.filtering = false
			m.in.Blur()
		default:
			var cmd tea.Cmd
			m.in, cmd = m.in.Update(k)
			m.query = m.in.Value()
			m.rebuild(ctx)
			return cmd
		}
		m.rebuild(ctx)
		return nil
	}
	switch k.String() {
	case "tab", "shift+tab", "left", "right":
		m.tab = 1 - m.tab
		return nil
	case "esc":
		if m.query != "" {
			m.in.SetValue("")
			m.query = ""
			m.rebuild(ctx)
		}
		return nil
	}
	if t := m.current(); t != nil {
		return t.Update(k)
	}
	return nil
}

func (m *Model) current() *table.Model {
	if m.tab == tabEvents {
		return m.feed
	}
	return m.log
}

// --- rows ---------------------------------------------------------------------

func logColumns() []table.Column {
	return []table.Column{
		{Key: "time", Title: "TIME", Width: 16},
		{Key: "action", Title: "ACTION", Width: 20},
		{Key: "targets", Title: "TARGETS", Flex: true, MinWidth: 12},
		{Key: "result", Title: "RESULT", Width: 12, Priority: 1},
		{Key: "backup", Title: "BACKUP", Width: 16, Priority: 3},
		{Key: "diff", Title: "DIFF", Width: 8, Priority: 2},
		{Key: "profile", Title: "PROFILE", Width: 14, Priority: 4},
	}
}

func eventColumns() []table.Column {
	return []table.Column{
		{Key: "time", Title: "TIME", Width: 16},
		{Key: "kind", Title: "EVENT", Width: 18},
		{Key: "text", Title: "WHAT", Flex: true, MinWidth: 16},
	}
}

type entryRow struct {
	i int
	e alog.Entry
}

func (r entryRow) Key() string { return fmt.Sprint(r.i) }
func (r entryRow) Cell(col string) string {
	e := r.e
	switch col {
	case "time":
		return e.Time.Local().Format("2006-01-02 15:04")
	case "action":
		return e.ActionID
	case "targets":
		var ts []string
		for _, t := range e.Targets {
			n := t.Name
			if n == "" {
				n = t.ID
			}
			ts = append(ts, t.Kind+" "+n)
		}
		return strings.Join(ts, ", ")
	case "result":
		if e.Result.Status == "ok" {
			return "ok"
		}
		if e.Result.Kind != "" {
			return "error: " + e.Result.Kind
		}
		return e.Result.Status
	case "backup":
		return e.BackupID
	case "diff":
		if e.DiffStats == nil {
			return ""
		}
		return fmt.Sprintf("+%d -%d", e.DiffStats.Added, e.DiffStats.Removed)
	case "profile":
		return e.Profile
	}
	return ""
}

// SortValue keeps the log in time order whatever column is sorted.
func (r entryRow) SortValue(col string) string {
	if col == "time" {
		return fmt.Sprintf("%020d", r.e.Time.UnixNano())
	}
	return r.Cell(col)
}

type eventRow struct {
	i int
	e watch.Event
}

func (r eventRow) Key() string { return fmt.Sprint(r.i) }
func (r eventRow) Cell(col string) string {
	switch col {
	case "time":
		return r.e.At.Local().Format("2006-01-02 15:04")
	case "kind":
		return strings.ReplaceAll(string(r.e.Kind), "_", " ")
	case "text":
		return r.e.Text()
	}
	return ""
}
func (r eventRow) SortValue(col string) string {
	if col == "time" {
		return fmt.Sprintf("%020d", r.e.At.UnixNano())
	}
	return r.Cell(col)
}

func eventMatches(e watch.Event, q string) bool {
	q = strings.ToLower(strings.TrimSpace(q))
	return q == "" || strings.Contains(strings.ToLower(string(e.Kind)+" "+e.Text()), q)
}

// filteredEntries are the log entries matching the filter, newest first.
func (m *Model) filteredEntries() []alog.Entry {
	var out []alog.Entry
	for _, e := range m.entries {
		if e.Matches(m.query) {
			out = append(out, e)
		}
	}
	return out
}

func (m *Model) filteredEvents() []watch.Event {
	var out []watch.Event
	for _, e := range m.events {
		if eventMatches(e, m.query) {
			out = append(out, e)
		}
	}
	return out
}

func (m *Model) ensureTables(ctx module.Context) {
	if m.log == nil {
		m.log = table.New(ctx.Styles(), ctx.Icons(), logColumns())
		m.log.SetMulti(false)
		m.log.SetSort("time", true)
	}
	if m.feed == nil {
		m.feed = table.New(ctx.Styles(), ctx.Icons(), eventColumns())
		m.feed.SetMulti(false)
		m.feed.SetSort("time", true)
	}
}

func (m *Model) rebuild(ctx module.Context) {
	m.ensureTables(ctx)
	es := m.filteredEntries()
	rows := make([]table.Row, len(es))
	for i, e := range es {
		rows[i] = entryRow{i: len(es) - i, e: e}
	}
	m.log.SetRows(rows)
	evs := m.filteredEvents()
	erows := make([]table.Row, len(evs))
	for i, e := range evs {
		erows[i] = eventRow{i: len(evs) - i, e: e}
	}
	m.feed.SetRows(erows)
}

// --- actions --------------------------------------------------------------------

// Export is what the export action writes.
type Export struct {
	ExportedAt time.Time     `json:"exported_at"`
	Profile    string        `json:"profile,omitempty"`
	Filter     string        `json:"filter,omitempty"`
	Entries    []alog.Entry  `json:"entries"`
	Events     []watch.Event `json:"events"`
}

func (m *Model) exportPath(now time.Time) string {
	dir := m.home
	if dir == "" {
		if h, err := os.UserHomeDir(); err == nil {
			dir = h
		}
	}
	return filepath.Join(dir, fmt.Sprintf("%s-activity-%s.json", brand.Name, now.Format("20060102-150405")))
}

func (m *Model) Actions() []module.Action {
	return []module.Action{
		{
			ID: "activity.filter", Title: "Filter activity", Keys: []string{"/"},
			Run: func(module.Context, module.Values) tea.Cmd {
				m.filtering = true
				return m.in.Focus()
			},
		},
		{
			ID: "activity.export", Title: "Export activity to JSON", Keys: []string{"x"},
			Params: func(ctx module.Context) *module.FormSpec {
				return &module.FormSpec{Title: "Export activity", Submit: "Export", Fields: []module.Field{{
					ID: "path", Label: "File", Kind: module.FieldText, Required: true,
					Value: m.exportPath(ctx.Now()),
					Help:  "Writes the entries and events shown (after the filter) as JSON, mode 0600. Secrets are already redacted.",
				}}}
			},
			Run: func(ctx module.Context, v module.Values) tea.Cmd {
				ex := Export{ExportedAt: ctx.Now(), Profile: ctx.Profile(), Filter: m.query,
					Entries: m.filteredEntries(), Events: m.filteredEvents()}
				if ex.Entries == nil {
					ex.Entries = []alog.Entry{}
				}
				if ex.Events == nil {
					ex.Events = []watch.Event{}
				}
				path := expandHome(strings.TrimSpace(v["path"]))
				return func() tea.Msg {
					res := module.ActionResult{ActionID: "activity.export"}
					if err := writeExport(path, ex); err != nil {
						res.Err = err
					} else {
						res.Message = fmt.Sprintf("Exported %d entries and %d events to %s.", len(ex.Entries), len(ex.Events), path)
					}
					return res
				}
			},
		},
	}
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

// writeExport writes ex as indented JSON, refusing to replace an existing file.
func writeExport(path string, ex Export) error {
	if path == "" {
		return fmt.Errorf("no file name given")
	}
	b, err := json.MarshalIndent(ex, "", "  ")
	if err != nil {
		return fmt.Errorf("encode: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		// Same words on every OS (Windows says "The file exists.").
		return fmt.Errorf("file exists, not overwriting %s", path)
	}
	if err != nil {
		return fmt.Errorf("export: %w", err)
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return fmt.Errorf("export: %w", err)
	}
	return f.Close()
}

// --- view -------------------------------------------------------------------------

func (m *Model) View(ctx module.Context, r module.Rect) string {
	m.ensureTables(ctx)
	s := ctx.Styles()
	items := []tabs.Item{
		{ID: "log", Title: fmt.Sprintf("Changes (%d)", len(m.entries))},
		{ID: "events", Title: fmt.Sprintf("Live events (%d)", len(m.events))},
	}
	active := "log"
	if m.tab == tabEvents {
		active = "events"
	}
	bar := tabs.Render(s, m.ic, items, active, r.W)

	var filterLine string
	switch {
	case m.filtering:
		m.in.Width = max(1, r.W-6)
		filterLine = s.Accent.Render(" / ") + m.in.View()
	case m.query != "":
		filterLine = s.Muted.Render(" filter: ") + s.Text.Render(m.query) + s.Muted.Render("  (esc clears)")
	}
	h := r.H - 1
	if filterLine != "" {
		h--
	}
	t := m.current()
	iw, ih := pane.Inner(r.W, h)
	t.SetSize(iw, ih)
	var body, title, right string
	if m.tab == tabEvents {
		title, right = "Live events", "since start · this session only"
		body = t.View()
		if len(m.events) == 0 {
			body = s.Muted.Render("No events yet. Machines coming online or going offline, keys about to expire,\n" +
				"and newly advertised routes show here as they are noticed on each refresh.")
		}
	} else {
		title, right = "Changes made with "+brand.Display, fmt.Sprintf("%d shown", t.Len())
		body = t.View()
		switch {
		case m.loadErr != nil:
			body = s.Danger.Render("Could not read the activity log: " + m.loadErr.Error())
		case len(m.entries) == 0:
			body = s.Muted.Render("Nothing recorded yet. Every change made here (API calls, file writes,\n" +
				"service actions) is logged with its targets, result, and backup ID.")
		}
	}
	p := pane.Render(s, title, body, r.W, h, true, pane.WithRight(right))
	parts := []string{bar}
	if filterLine != "" {
		parts = append(parts, filterLine)
	}
	return strings.Join(append(parts, p), "\n")
}

func (m *Model) Hints() []module.Hint {
	if m.filtering {
		return []module.Hint{{Key: "enter", Desc: "keep filter"}, {Key: "esc", Desc: "clear"}}
	}
	return []module.Hint{{Key: "tab", Desc: "changes/events"}, {Key: "/", Desc: "filter"}, {Key: "x", Desc: "export"}}
}
