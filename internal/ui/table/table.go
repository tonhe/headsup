// Package table is a virtualised, sortable, multi-select table. Only the rows
// in the viewport are rendered, so it handles tens of thousands of rows.
// Cursor and selection are keyed by Row.Key(), so they survive refreshes.
package table

import (
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/tonhe/headsup/internal/icons"
	"github.com/tonhe/headsup/internal/ui/styles"
)

// Row is one table row. Cell may return pre-styled text; SortValue should be
// plain (numbers are compared numerically when both sides parse).
type Row interface {
	Key() string
	Cell(col string) string
	SortValue(col string) string
}

// Column describes one column. Width is the fixed width (or the starting
// width for Flex columns, which also grow into spare space). Columns with the
// highest Priority number are dropped first when space runs out; Priority 0
// columns are dropped last.
type Column struct {
	Key, Title string
	Width      int
	MinWidth   int
	Flex       bool
	Priority   int
	Align      lipgloss.Position
}

// KeyMap holds the table bindings.
type KeyMap struct {
	Up, Down, Top, Bottom, HalfDown, HalfUp, PageDown, PageUp key.Binding
	Toggle, ToggleAll, SortNext, SortReverse                  key.Binding
}

// DefaultKeyMap returns the standard bindings.
func DefaultKeyMap() KeyMap {
	b := func(k ...string) key.Binding { return key.NewBinding(key.WithKeys(k...)) }
	return KeyMap{
		Up: b("k", "up"), Down: b("j", "down"),
		Top: b("g", "home"), Bottom: b("G", "end"),
		HalfDown: b("ctrl+d"), HalfUp: b("ctrl+u"),
		PageDown: b("pgdown"), PageUp: b("pgup"),
		Toggle: b(" ", "space"), ToggleAll: b("ctrl+a"),
		SortNext: b(">"), SortReverse: b("<"),
	}
}

// Model is the table state. Use New; methods have pointer receivers.
type Model struct {
	s    *styles.Styles
	ic   icons.Set
	Keys KeyMap

	cols    []Column
	rows    []Row
	index   map[string]int
	sel     map[string]bool
	cursor  int
	curKey  string
	offset  int
	w, h    int
	focused bool
	multi   bool

	sortCol  string
	sortDesc bool
}

// New builds a table with the given columns.
func New(s *styles.Styles, ic icons.Set, cols []Column) *Model {
	return &Model{s: s, ic: ic, Keys: DefaultKeyMap(), cols: cols,
		index: map[string]int{}, sel: map[string]bool{}, focused: true, multi: true}
}

// SetSize sets the outer size; one line is the header, the rest rows.
func (m *Model) SetSize(w, h int) { m.w, m.h = w, h; m.clamp() }

// SetFocused controls whether the cursor row is highlighted.
func (m *Model) SetFocused(f bool) { m.focused = f }

// SetMulti enables or disables multi-select (enabled by default).
func (m *Model) SetMulti(on bool) {
	m.multi = on
	if !on {
		m.sel = map[string]bool{}
	}
}

// SetColumns replaces the column set.
func (m *Model) SetColumns(cols []Column) { m.cols = cols }

// Columns returns the configured columns.
func (m *Model) Columns() []Column { return m.cols }

// Len returns the number of rows.
func (m *Model) Len() int { return len(m.rows) }

// Rows returns the rows in display (sorted) order.
func (m *Model) Rows() []Row { return m.rows }

// SetRows replaces the rows, keeping the cursor on the same key (or the same
// index when that key disappeared) and dropping selections that vanished.
func (m *Model) SetRows(rows []Row) {
	m.rows = append(m.rows[:0:0], rows...)
	m.resort()
	for k := range m.sel {
		if _, ok := m.index[k]; !ok {
			delete(m.sel, k)
		}
	}
}

func (m *Model) resort() {
	if m.sortCol != "" {
		col := m.sortCol
		type kv struct {
			s   string
			f   float64
			num bool
		}
		keys := make([]kv, len(m.rows))
		for i, r := range m.rows {
			v := r.SortValue(col)
			f, err := strconv.ParseFloat(v, 64)
			keys[i] = kv{strings.ToLower(v), f, err == nil}
		}
		idx := make([]int, len(m.rows))
		for i := range idx {
			idx[i] = i
		}
		desc := m.sortDesc
		sort.SliceStable(idx, func(a, b int) bool {
			x, y := keys[idx[a]], keys[idx[b]]
			var less, eq bool
			if x.num && y.num {
				less, eq = x.f < y.f, x.f == y.f
			} else {
				less, eq = x.s < y.s, x.s == y.s
			}
			if eq {
				return false
			}
			if desc {
				return !less
			}
			return less
		})
		sorted := make([]Row, len(m.rows))
		for i, j := range idx {
			sorted[i] = m.rows[j]
		}
		m.rows = sorted
	}
	m.index = make(map[string]int, len(m.rows))
	for i, r := range m.rows {
		m.index[r.Key()] = i
	}
	if i, ok := m.index[m.curKey]; ok && m.curKey != "" {
		m.cursor = i
	}
	m.clamp()
}

// SortBy sorts by col; calling it again for the same column flips the
// direction. An empty col clears sorting (rows keep insertion order on the
// next SetRows).
func (m *Model) SortBy(col string) {
	if col == m.sortCol && col != "" {
		m.sortDesc = !m.sortDesc
	} else {
		m.sortCol, m.sortDesc = col, false
	}
	m.resort()
}

// SetSort sets the sort column and direction explicitly.
func (m *Model) SetSort(col string, desc bool) {
	m.sortCol, m.sortDesc = col, desc
	m.resort()
}

// Sort reports the current sort column and direction.
func (m *Model) Sort() (col string, desc bool) { return m.sortCol, m.sortDesc }

// Cursor returns the row under the cursor.
func (m *Model) Cursor() (Row, bool) {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return nil, false
	}
	return m.rows[m.cursor], true
}

// CursorIndex returns the cursor position in display order.
func (m *Model) CursorIndex() int { return m.cursor }

// SetCursorKey moves the cursor to the row with key k; false if absent.
func (m *Model) SetCursorKey(k string) bool {
	i, ok := m.index[k]
	if ok {
		m.cursor = i
		m.clamp()
	}
	return ok
}

// Selected returns the selected keys in display order.
func (m *Model) Selected() []string {
	if len(m.sel) == 0 {
		return nil
	}
	out := make([]string, 0, len(m.sel))
	for _, r := range m.rows {
		if m.sel[r.Key()] {
			out = append(out, r.Key())
		}
	}
	return out
}

// IsSelected reports whether key k is selected.
func (m *Model) IsSelected(k string) bool { return m.sel[k] }

// SetSelected replaces the selection.
func (m *Model) SetSelected(keys []string) {
	m.sel = map[string]bool{}
	for _, k := range keys {
		if _, ok := m.index[k]; ok {
			m.sel[k] = true
		}
	}
}

// ClearSelection deselects everything.
func (m *Model) ClearSelection() { m.sel = map[string]bool{} }

// Targets returns the selection, or the cursor key when nothing is selected.
func (m *Model) Targets() []string {
	if s := m.Selected(); len(s) > 0 {
		return s
	}
	if r, ok := m.Cursor(); ok {
		return []string{r.Key()}
	}
	return nil
}

func (m *Model) bodyH() int { return max(0, m.h-1) }

func (m *Model) clamp() {
	n := len(m.rows)
	if m.cursor >= n {
		m.cursor = n - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	bh := m.bodyH()
	if m.offset > m.cursor {
		m.offset = m.cursor
	}
	if bh > 0 && m.cursor >= m.offset+bh {
		m.offset = m.cursor - bh + 1
	}
	if m.offset > n-bh {
		m.offset = max(0, n-bh)
	}
	if m.offset < 0 {
		m.offset = 0
	}
	if n > 0 {
		m.curKey = m.rows[m.cursor].Key()
	} else {
		m.curKey = ""
	}
}

func (m *Model) move(d int) { m.cursor += d; m.clamp() }

// Update handles navigation, selection and sort keys. It never returns a Cmd
// today but keeps the Bubble Tea shape.
func (m *Model) Update(msg tea.Msg) tea.Cmd {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	bh := max(1, m.bodyH())
	switch {
	case key.Matches(km, m.Keys.Up):
		m.move(-1)
	case key.Matches(km, m.Keys.Down):
		m.move(1)
	case key.Matches(km, m.Keys.Top):
		m.cursor = 0
		m.clamp()
	case key.Matches(km, m.Keys.Bottom):
		m.cursor = len(m.rows) - 1
		m.clamp()
	case key.Matches(km, m.Keys.HalfDown):
		m.move(max(1, bh/2))
	case key.Matches(km, m.Keys.HalfUp):
		m.move(-max(1, bh/2))
	case key.Matches(km, m.Keys.PageDown):
		m.move(bh)
	case key.Matches(km, m.Keys.PageUp):
		m.move(-bh)
	case key.Matches(km, m.Keys.Toggle):
		if r, ok := m.Cursor(); ok && m.multi {
			k := r.Key()
			if m.sel[k] {
				delete(m.sel, k)
			} else {
				m.sel[k] = true
			}
			m.move(1)
		}
	case key.Matches(km, m.Keys.ToggleAll):
		if !m.multi {
			break
		}
		if len(m.sel) == len(m.rows) {
			m.sel = map[string]bool{}
		} else {
			for _, r := range m.rows {
				m.sel[r.Key()] = true
			}
		}
	case key.Matches(km, m.Keys.SortNext):
		m.cycleSort()
	case key.Matches(km, m.Keys.SortReverse):
		if m.sortCol != "" {
			m.SortBy(m.sortCol)
		}
	}
	return nil
}

func (m *Model) cycleSort() {
	if len(m.cols) == 0 {
		return
	}
	next := 0
	for i, c := range m.cols {
		if c.Key == m.sortCol {
			next = (i + 1) % len(m.cols)
		}
	}
	m.SetSort(m.cols[next].Key, false)
}

const gutter = 2 // cursor marker + selection marker

// Layout returns the visible columns and their widths for width w.
func (m *Model) Layout(w int) ([]Column, []int) {
	avail := w - gutter
	vis := append([]Column(nil), m.cols...)
	base := func(c Column) int {
		bw := c.Width
		if c.Flex && c.MinWidth > 0 {
			bw = c.MinWidth
		}
		if bw <= 0 {
			bw = max(c.MinWidth, lipgloss.Width(c.Title)+2)
		}
		return bw
	}
	need := func() int {
		n := 0
		for _, c := range vis {
			n += base(c)
		}
		return n + max(0, len(vis)-1)
	}
	for len(vis) > 1 && need() > avail {
		drop := len(vis) - 1
		for i := len(vis) - 1; i >= 0; i-- {
			if vis[i].Priority > vis[drop].Priority {
				drop = i
			}
		}
		vis = append(vis[:drop], vis[drop+1:]...)
	}
	widths := make([]int, len(vis))
	for i, c := range vis {
		widths[i] = base(c)
	}
	spare := avail - need()
	if spare < 0 && len(vis) == 1 {
		widths[0] = max(0, avail)
	}
	var flex []int
	for i, c := range vis {
		if c.Flex {
			flex = append(flex, i)
		}
	}
	if spare > 0 && len(flex) > 0 {
		each, rem := spare/len(flex), spare%len(flex)
		for j, i := range flex {
			widths[i] += each
			if j < rem {
				widths[i]++
			}
		}
	}
	return vis, widths
}

// VisibleColumns returns the keys of columns that fit the current width.
func (m *Model) VisibleColumns() []string {
	vis, _ := m.Layout(m.w)
	out := make([]string, len(vis))
	for i, c := range vis {
		out[i] = c.Key
	}
	return out
}

// View renders exactly h lines of exactly w cells.
func (m *Model) View() string {
	if m.w <= 0 || m.h <= 0 {
		return ""
	}
	vis, widths := m.Layout(m.w)
	var b strings.Builder
	b.WriteString(m.header(vis, widths))
	bh := m.bodyH()
	end := min(len(m.rows), m.offset+bh)
	for i := m.offset; i < end; i++ {
		b.WriteByte('\n')
		b.WriteString(m.row(i, vis, widths))
	}
	blank := strings.Repeat(" ", m.w)
	for i := end - m.offset; i < bh; i++ {
		b.WriteByte('\n')
		b.WriteString(blank)
	}
	return b.String()
}

func (m *Model) header(vis []Column, widths []int) string {
	parts := make([]string, len(vis))
	for i, c := range vis {
		t := c.Title
		if c.Key == m.sortCol && m.sortCol != "" {
			ic := m.ic.SortAsc
			if m.sortDesc {
				ic = m.ic.SortDesc
			}
			t += " " + ic
		}
		parts[i] = Fit(t, widths[i], c.Align, m.ic.Ellipsis)
	}
	return fitLine(m.s.TableHeader.Render(strings.Repeat(" ", gutter)+strings.Join(parts, " ")), m.w)
}

func (m *Model) row(i int, vis []Column, widths []int) string {
	r := m.rows[i]
	k := r.Key()
	isCur := i == m.cursor
	plain := isCur && m.focused
	parts := make([]string, len(vis))
	for j, c := range vis {
		cell := r.Cell(c.Key)
		if plain {
			cell = ansi.Strip(cell)
		}
		parts[j] = Fit(cell, widths[j], c.Align, m.ic.Ellipsis)
	}
	line := strings.Join(parts, " ")
	cm, sm := " ", " "
	if plain {
		// Only the focused table marks its cursor row, so with two tables on
		// screen the marker (not just colour) shows which one keys go to.
		cm = m.ic.Select
	}
	if m.sel[k] {
		sm = m.ic.Check
	}
	cm, sm = Fit(cm, 1, lipgloss.Left, ""), Fit(sm, 1, lipgloss.Left, "")
	if plain {
		return m.s.RowSel.Render(fitLine(cm+sm+line, m.w))
	}
	if m.sel[k] {
		sm = m.s.Accent.Render(sm)
		line = m.s.RowSel.Render(ansi.Strip(line))
	}
	return fitLine(cm+sm+line, m.w)
}

// Fit truncates s with tail (ANSI- and wide-char-safe) or pads it to exactly
// w cells using align.
func Fit(s string, w int, align lipgloss.Position, tail string) string {
	if w <= 0 {
		return ""
	}
	sw := lipgloss.Width(s)
	if sw > w {
		if lipgloss.Width(tail) >= w {
			tail = ""
		}
		s = ansi.Truncate(s, w, tail) + ansi.ResetStyle
		sw = lipgloss.Width(s)
	}
	if sw >= w {
		return s
	}
	pad := w - sw
	switch align {
	case lipgloss.Right:
		return strings.Repeat(" ", pad) + s
	case lipgloss.Center:
		l := pad / 2
		return strings.Repeat(" ", l) + s + strings.Repeat(" ", pad-l)
	default:
		return s + strings.Repeat(" ", pad)
	}
}

func fitLine(s string, w int) string { return Fit(s, w, lipgloss.Left, "") }
