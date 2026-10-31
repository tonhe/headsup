// Package prefs is the Preferences page: the app's own look-and-feel and
// behaviour settings ([ui] in config.toml), applied live and saved. It is
// labelled "Preferences" and opened with "," (or "g ,").
//
// The page talks to the app through Settings, an interface the app's Context
// implements; prefs does not import the app.
package prefs

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/tonhe/headsup/internal/config"
	"github.com/tonhe/headsup/internal/icons"
	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/modules/registry"
	"github.com/tonhe/headsup/internal/store"
	"github.com/tonhe/headsup/internal/theme"
	"github.com/tonhe/headsup/internal/ui/empty"
	"github.com/tonhe/headsup/internal/ui/pane"
)

func init() { registry.Register(func() module.Module { return New() }) }

// Settings is what the app offers the Preferences page.
type Settings interface {
	// UIPrefs returns the saved [ui] settings.
	UIPrefs() config.UI
	// SetUIPrefs changes [ui] settings, applies them live, and saves.
	SetUIPrefs(change func(*config.UI)) tea.Cmd
	// ProfileTheme reports the connected config profile and its theme
	// override; ok is false when there is no config profile.
	ProfileTheme() (profile, slug string, ok bool)
	// SetProfileTheme sets ("" clears) the connected profile's theme.
	SetProfileTheme(slug string) tea.Cmd
	// OpenThemePicker opens the theme picker modal.
	OpenThemePicker() tea.Cmd
	// ColumnSets lists, per module ID, the columns a table module offers.
	ColumnSets() map[string][]string
}

// saver is optionally implemented to say whether changes reach disk.
type saver interface{ ConfigSaved() bool }

type kind int

const (
	kTheme kind = iota
	kProfileTheme
	kEnum
	kToggle
	kDuration
	kColumns
)

// row is one setting.
type row struct {
	id, label, help string
	kind            kind
	options         []string
	get             func(config.UI) string
	set             func(*config.UI, string)
}

// Model is the Preferences module.
type Model struct {
	cursor, offset int
}

// New returns the module.
func New() *Model { return &Model{} }

func (m *Model) ID() string                { return "prefs" }
func (m *Model) Title() string             { return "Preferences" }
func (m *Model) Icon(ic icons.Set) string  { return ic.Settings }
func (m *Model) Order() int                { return 1000 }
func (m *Model) Modes() module.Modes       { return module.ModeLocal }
func (m *Model) Chord() string             { return "," }
func (m *Model) Section() module.Section   { return module.SectionApp }
func (m *Model) JumpKeys() []string        { return []string{","} }
func (m *Model) Capturing() bool           { return false }
func (m *Model) Badge(*store.Store) string { return "" }
func (m *Model) Init(module.Context) tea.Cmd {
	return nil
}

func (m *Model) Hints() []module.Hint {
	return []module.Hint{
		{Key: "j/k", Desc: "move"}, {Key: "enter", Desc: "change"},
		{Key: "h/l", Desc: "cycle"}, {Key: "x", Desc: "reset"}, {Key: "T", Desc: "themes"},
	}
}

func settings(ctx module.Context) (Settings, bool) {
	s, ok := ctx.(Settings)
	return s, ok
}

func durationRow(id, label, help string, get func(config.UI) string, set func(*config.UI, string)) row {
	return row{id: id, label: label, help: help, kind: kDuration, get: get, set: set}
}

// rows lists the settings shown for this context.
func (m *Model) rows(st Settings) []row {
	rs := []row{
		{id: "theme", label: "Theme", kind: kTheme,
			help: "Default colour theme. enter opens the picker with live preview",
			get:  func(u config.UI) string { return u.Theme },
			set:  func(u *config.UI, v string) { u.Theme = v }},
	}
	if prof, _, ok := st.ProfileTheme(); ok {
		rs = append(rs, row{id: "profile_theme", label: "Profile theme", kind: kProfileTheme,
			help: "Theme used only while connected to " + prof + " (a visual cue for production)"})
	}
	rs = append(rs,
		row{id: "icons", label: "Icons", kind: kEnum, options: []string{"unicode", "nerd", "ascii"},
			help: "Glyph set: Unicode, Nerd Font, or plain ASCII",
			get:  func(u config.UI) string { return u.Icons }, set: func(u *config.UI, v string) { u.Icons = v }},
		row{id: "density", label: "Density", kind: kEnum, options: []string{"comfortable", "compact"},
			help: "Row spacing in tables and lists",
			get:  func(u config.UI) string { return u.Density }, set: func(u *config.UI, v string) { u.Density = v }},
		row{id: "sidebar", label: "Sidebar", kind: kEnum, options: []string{"auto", "always", "never"},
			help: "auto shows the sidebar at 100 columns or wider, a tab bar below that",
			get:  func(u config.UI) string { return u.Sidebar }, set: func(u *config.UI, v string) { u.Sidebar = v }},
		row{id: "mouse", label: "Mouse", kind: kToggle, options: []string{"true", "false"},
			help: "Click sidebar entries and rows, scroll with the wheel",
			get:  func(u config.UI) string { return fmt.Sprint(u.Mouse) }, set: func(u *config.UI, v string) { u.Mouse = v == "true" }},
		row{id: "time_format", label: "Time format", kind: kEnum, options: []string{"relative", "rfc3339", "local"},
			help: "How timestamps are shown: \"3h ago\", RFC 3339, or local time",
			get:  func(u config.UI) string { return u.TimeFormat }, set: func(u *config.UI, v string) { u.TimeFormat = v }},
		durationRow("refresh", "Refresh", "Poll interval for the visible page (e.g. 5s)",
			func(u config.UI) string { return u.Refresh }, func(u *config.UI, v string) { u.Refresh = v }),
		durationRow("refresh_idle", "Idle refresh", "Poll interval when nothing is happening (e.g. 30s)",
			func(u config.UI) string { return u.RefreshIdle }, func(u *config.UI, v string) { u.RefreshIdle = v }),
		durationRow("expiry_warn", "Expiry warning", "How far ahead expiring keys and nodes are flagged (e.g. 168h)",
			func(u config.UI) string { return u.ExpiryWarn }, func(u *config.UI, v string) { u.ExpiryWarn = v }),
	)
	sets := st.ColumnSets()
	ids := map[string]bool{}
	for id := range sets {
		ids[id] = true
	}
	for id := range st.UIPrefs().Columns {
		ids[id] = true
	}
	keys := make([]string, 0, len(ids))
	for id := range ids {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	for _, id := range keys {
		help := "Columns shown, in order; empty restores the default"
		if avail := sets[id]; len(avail) > 0 {
			help = "Available: " + strings.Join(avail, ", ")
		}
		rs = append(rs, row{id: "columns." + id, label: "Columns: " + id, kind: kColumns, help: help,
			options: sets[id],
			get:     func(u config.UI) string { return strings.Join(u.Columns[id], ", ") },
			set: func(u *config.UI, v string) {
				cols := splitList(v)
				if u.Columns == nil {
					u.Columns = map[string][]string{}
				}
				if len(cols) == 0 {
					delete(u.Columns, id)
					return
				}
				u.Columns[id] = cols
			}})
	}
	return rs
}

func splitList(v string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' }) {
		if !slices.Contains(out, f) {
			out = append(out, f)
		}
	}
	return out
}

func (m *Model) current(ctx module.Context) (Settings, row, bool) {
	st, ok := settings(ctx)
	if !ok {
		return nil, row{}, false
	}
	rs := m.rows(st)
	if len(rs) == 0 {
		return st, row{}, false
	}
	m.cursor = max(0, min(m.cursor, len(rs)-1))
	return st, rs[m.cursor], true
}

func (m *Model) value(st Settings, r row) string {
	if r.kind == kProfileTheme {
		_, slug, _ := st.ProfileTheme()
		return slug
	}
	return r.get(st.UIPrefs())
}

// cycle moves an enum, toggle or theme row to its next (d=1) or previous
// (d=-1) value and applies it.
func (m *Model) cycle(ctx module.Context, d int) tea.Cmd {
	st, r, ok := m.current(ctx)
	if !ok {
		return nil
	}
	var opts []string
	switch r.kind {
	case kEnum, kToggle:
		opts = r.options
	case kTheme:
		opts = themeSlugs()
	case kProfileTheme:
		opts = append([]string{""}, themeSlugs()...)
	default:
		return ctx.Toast(module.ToastInfo, "Press enter to edit "+r.label+".")
	}
	cur := slices.Index(opts, m.value(st, r))
	next := opts[((cur+d)%len(opts)+len(opts))%len(opts)]
	if cur < 0 && d < 0 {
		next = opts[len(opts)-1]
	}
	return m.apply(st, r, next)
}

func (m *Model) apply(st Settings, r row, v string) tea.Cmd {
	if r.kind == kProfileTheme {
		return st.SetProfileTheme(v)
	}
	return st.SetUIPrefs(func(u *config.UI) { r.set(u, v) })
}

func themeSlugs() []string {
	ns := theme.Names()
	out := make([]string, len(ns))
	for i, n := range ns {
		out[i] = n[0]
	}
	return out
}

func (m *Model) Actions() []module.Action {
	return []module.Action{
		{
			ID: "prefs.edit", Title: "Change setting", Keys: []string{"enter"},
			Params: func(ctx module.Context) *module.FormSpec {
				st, r, ok := m.current(ctx)
				if !ok {
					return nil
				}
				return m.form(st, r)
			},
			Run: func(ctx module.Context, v module.Values) tea.Cmd {
				st, r, ok := m.current(ctx)
				if !ok {
					return nil
				}
				if r.kind == kTheme {
					return st.OpenThemePicker()
				}
				val, ok := v["value"]
				if !ok {
					return nil
				}
				if r.kind == kProfileTheme && val == defaultLabel {
					val = ""
				}
				return m.apply(st, r, val)
			},
		},
		{
			ID: "prefs.next", Title: "Next value", Keys: []string{"l", "right", "space"},
			Run: func(ctx module.Context, _ module.Values) tea.Cmd { return m.cycle(ctx, 1) },
		},
		{
			ID: "prefs.prev", Title: "Previous value", Keys: []string{"h", "left"},
			Run: func(ctx module.Context, _ module.Values) tea.Cmd { return m.cycle(ctx, -1) },
		},
		{
			ID: "prefs.reset", Title: "Reset setting to default", Keys: []string{"x"},
			Run: func(ctx module.Context, _ module.Values) tea.Cmd {
				st, r, ok := m.current(ctx)
				if !ok {
					return nil
				}
				if r.kind == kProfileTheme {
					return st.SetProfileTheme("")
				}
				def := r.get(config.Default().UI)
				return tea.Batch(m.apply(st, r, def), ctx.Toast(module.ToastInfo, r.label+" reset to default."))
			},
		},
	}
}

const defaultLabel = "(use default theme)"

// form returns the editor for a row; nil for the theme row, which opens the
// picker instead.
func (m *Model) form(st Settings, r row) *module.FormSpec {
	cur := m.value(st, r)
	f := module.Field{ID: "value", Label: r.label, Value: cur, Help: r.help}
	switch r.kind {
	case kTheme:
		return nil
	case kProfileTheme:
		f.Kind = module.FieldSelect
		f.Options = append([]string{defaultLabel}, themeSlugs()...)
		if cur == "" {
			f.Value = defaultLabel
		}
	case kEnum, kToggle:
		f.Kind = module.FieldSelect
		f.Options = r.options
	case kDuration:
		f.Kind = module.FieldDuration
		f.Required = true
		f.Validate = func(s string) error {
			if _, err := time.ParseDuration(strings.TrimSpace(s)); err != nil {
				return fmt.Errorf("not a duration (e.g. 5s, 2m, 168h)")
			}
			return nil
		}
	case kColumns:
		f.Placeholder = "comma-separated column keys"
		if len(r.options) > 0 {
			avail := r.options
			f.Validate = func(s string) error {
				for _, c := range splitList(s) {
					if !slices.Contains(avail, c) {
						return fmt.Errorf("unknown column %q", c)
					}
				}
				return nil
			}
		}
	}
	return &module.FormSpec{Title: "Preferences: " + r.label, Submit: "Save", Fields: []module.Field{f}}
}

func (m *Model) Update(ctx module.Context, msg tea.Msg) (module.Module, tea.Cmd) {
	st, ok := settings(ctx)
	if !ok {
		return m, nil
	}
	n := len(m.rows(st))
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "j", "down", "ctrl+n":
			m.cursor++
		case "k", "up":
			m.cursor--
		case "G", "end":
			m.cursor = n - 1
		case "home":
			m.cursor = 0
		}
	case tea.MouseMsg:
		// Rows start below the pane's top border.
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			if i := m.offset + msg.Y - 1; msg.Y >= 1 && i < n {
				m.cursor = i
			}
		}
	}
	m.cursor = max(0, min(m.cursor, n-1))
	return m, nil
}

func (m *Model) View(ctx module.Context, r module.Rect) string {
	s := ctx.Styles()
	st, ok := settings(ctx)
	if !ok {
		return empty.Render(s, "Preferences are not available here", "", "", r.W, r.H)
	}
	rows := m.rows(st)
	iw, ih := pane.Inner(r.W, r.H)
	listH := max(1, ih-3) // a blank line and two help lines below
	m.cursor = max(0, min(m.cursor, len(rows)-1))
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+listH {
		m.offset = m.cursor - listH + 1
	}

	labelW := 0
	for _, rw := range rows {
		labelW = max(labelW, lipgloss.Width(rw.label))
	}
	labelW = min(labelW, iw/3)
	_, profSlug, _ := st.ProfileTheme()
	var lines []string
	end := min(len(rows), m.offset+listH)
	for i := m.offset; i < end; i++ {
		rw := rows[i]
		v := m.value(st, rw)
		shown := v
		switch rw.kind {
		case kTheme:
			if t := theme.ByName(v); t != nil {
				shown = t.Name
			}
			if profSlug != "" {
				shown += s.Muted.Render("  (overridden by profile theme)")
			}
		case kProfileTheme:
			if t := theme.ByName(v); t != nil {
				shown = t.Name
			} else if v == "" {
				shown = s.Muted.Render("none, uses Theme")
			}
		case kToggle:
			shown = "off"
			if v == "true" {
				shown = "on"
			}
		case kColumns:
			if v == "" {
				shown = s.Muted.Render("default")
			}
		}
		marker := "  "
		ls := s.Text
		if i == m.cursor {
			marker = s.Accent.Render(ctx.Icons().Arrow + " ")
			ls = s.Strong
		}
		label := ls.Render(padRight(rw.label, labelW))
		line := marker + label + "  " + s.Accent.Render(shown)
		if lipgloss.Width(line) > iw {
			line = ansi.Truncate(line, iw, ctx.Icons().Ellipsis)
		}
		lines = append(lines, line)
	}
	for len(lines) < listH {
		lines = append(lines, "")
	}
	help := ""
	if m.cursor < len(rows) {
		help = rows[m.cursor].help
	}
	note := "Changes apply now and are saved to config.toml."
	if sv, ok := ctx.(saver); ok && !sv.ConfigSaved() {
		note = "Changes apply now, for this session only."
	}
	lines = append(lines, "", fitW(s.Muted.Render(help), iw), fitW(s.Muted.Render(note), iw))
	return pane.Render(s, "Preferences", strings.Join(lines, "\n"), r.W, r.H, true)
}

func padRight(s string, w int) string {
	if lipgloss.Width(s) > w {
		return ansi.Truncate(s, w, "")
	}
	return s + strings.Repeat(" ", w-lipgloss.Width(s))
}

func fitW(s string, w int) string {
	if lipgloss.Width(s) > w {
		return ansi.Truncate(s, w, "")
	}
	return s
}
