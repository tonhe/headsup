// Package themepicker is the theme picker modal: a searchable list of every
// registered theme with a light/dark filter and live preview. Moving the
// cursor emits PreviewMsg so the app can repaint the whole screen behind the
// picker; esc emits CancelMsg with the theme to restore.
package themepicker

import (
	"strconv"
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/sahilm/fuzzy"

	"github.com/tonhe/headsup/internal/icons"
	"github.com/tonhe/headsup/internal/theme"
	"github.com/tonhe/headsup/internal/ui/pane"
	"github.com/tonhe/headsup/internal/ui/styles"
)

// Filter restricts the list to dark or light themes.
type Filter int

const (
	FilterAll Filter = iota
	FilterDark
	FilterLight
)

func (f Filter) String() string {
	switch f {
	case FilterDark:
		return "dark"
	case FilterLight:
		return "light"
	}
	return "all"
}

// PreviewMsg asks the app to show a theme without saving it.
type PreviewMsg struct{ Slug string }

// SelectedMsg is the user's choice. ProfileOnly means "use this theme for the
// current connection profile only" (a per-profile override).
type SelectedMsg struct {
	Slug        string
	ProfileOnly bool
}

// CancelMsg closes the picker; the app should restore Orig.
type CancelMsg struct{ Orig string }

// ReloadMsg asks the app to reload user theme files and call SetThemes.
type ReloadMsg struct{}

// Model is the picker state.
type Model struct {
	s       *styles.Styles
	ic      icons.Set
	themes  []theme.Theme
	shown   []int
	cursor  int
	offset  int
	query   string
	filter  Filter
	orig    string
	preview string
	profile string // non-empty enables the per-profile choice
	w, h    int
}

// New builds a picker over themes with the cursor on current. profile names
// the active connection profile; empty hides the per-profile option.
func New(s *styles.Styles, ic icons.Set, themes []theme.Theme, current, profile string) *Model {
	m := &Model{s: s, ic: ic, themes: themes, orig: current, preview: current, profile: profile}
	m.refilter(current)
	return m
}

// SetSize sets the outer box size.
func (m *Model) SetSize(w, h int) { m.w, m.h = w, h; m.clamp() }

// SetThemes replaces the theme list (after a reload), keeping the cursor on
// the same theme when it still exists.
func (m *Model) SetThemes(ts []theme.Theme) {
	cur := m.Current()
	m.themes = ts
	m.refilter(cur)
}

// Current is the slug under the cursor ("" when nothing matches).
func (m *Model) Current() string {
	if m.cursor < 0 || m.cursor >= len(m.shown) {
		return ""
	}
	return m.themes[m.shown[m.cursor]].Slug
}

// Orig is the theme that was active when the picker opened.
func (m *Model) Orig() string { return m.orig }

// Shown returns the slugs currently listed, in order.
func (m *Model) Shown() []string {
	out := make([]string, len(m.shown))
	for i, ix := range m.shown {
		out[i] = m.themes[ix].Slug
	}
	return out
}

// Filter returns the light/dark filter.
func (m *Model) Filter() Filter { return m.filter }

type names []theme.Theme

func (n names) String(i int) string { return n[i].Name + " " + n[i].Slug }
func (n names) Len() int            { return len(n) }

func (m *Model) keep(t theme.Theme) bool {
	switch m.filter {
	case FilterDark:
		return !t.Light
	case FilterLight:
		return t.Light
	}
	return true
}

// refilter rebuilds the list and puts the cursor on slug if listed.
func (m *Model) refilter(slug string) {
	m.shown = m.shown[:0]
	q := strings.TrimSpace(m.query)
	if q == "" {
		for i, t := range m.themes {
			if m.keep(t) {
				m.shown = append(m.shown, i)
			}
		}
	} else {
		for _, r := range fuzzy.FindFrom(q, names(m.themes)) {
			if m.keep(m.themes[r.Index]) {
				m.shown = append(m.shown, r.Index)
			}
		}
	}
	m.cursor = 0
	for i, ix := range m.shown {
		if m.themes[ix].Slug == slug {
			m.cursor = i
		}
	}
	m.offset = 0
	m.clamp()
}

func (m *Model) listH() int { return max(1, m.h-5) } // border x2, query, rule, help

func (m *Model) clamp() {
	n := len(m.shown)
	m.cursor = max(0, min(m.cursor, n-1))
	lh := m.listH()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+lh {
		m.offset = m.cursor - lh + 1
	}
	m.offset = max(0, min(m.offset, max(0, n-lh)))
}

// previewCmd emits PreviewMsg when the highlighted theme changed.
func (m *Model) previewCmd() tea.Cmd {
	cur := m.Current()
	if cur == "" || cur == m.preview {
		return nil
	}
	m.preview = cur
	return func() tea.Msg { return PreviewMsg{Slug: cur} }
}

// Update handles keys and the mouse wheel.
func (m *Model) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.MouseMsg:
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			m.cursor--
		case tea.MouseButtonWheelDown:
			m.cursor++
		default:
			return nil
		}
		m.clamp()
		return m.previewCmd()
	case tea.KeyMsg:
		return m.key(msg)
	}
	return nil
}

func (m *Model) key(k tea.KeyMsg) tea.Cmd {
	switch k.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		if m.query != "" && k.Type == tea.KeyEsc {
			m.setQuery("")
			return m.previewCmd()
		}
		orig := m.orig
		return func() tea.Msg { return CancelMsg{Orig: orig} }
	case tea.KeyEnter, tea.KeyCtrlT:
		cur := m.Current()
		if cur == "" {
			return nil
		}
		only := k.Type == tea.KeyCtrlT
		if only && m.profile == "" {
			return nil
		}
		return func() tea.Msg { return SelectedMsg{Slug: cur, ProfileOnly: only} }
	case tea.KeyCtrlR:
		return func() tea.Msg { return ReloadMsg{} }
	case tea.KeyTab:
		m.filter = (m.filter + 1) % 3
		m.refilter(m.Current())
		return m.previewCmd()
	case tea.KeyShiftTab:
		m.filter = (m.filter + 2) % 3
		m.refilter(m.Current())
		return m.previewCmd()
	case tea.KeyUp, tea.KeyCtrlP, tea.KeyCtrlK:
		m.cursor--
	case tea.KeyDown, tea.KeyCtrlN, tea.KeyCtrlJ:
		m.cursor++
	case tea.KeyPgUp:
		m.cursor -= m.listH()
	case tea.KeyPgDown:
		m.cursor += m.listH()
	case tea.KeyHome:
		m.cursor = 0
	case tea.KeyEnd:
		m.cursor = len(m.shown) - 1
	case tea.KeyBackspace:
		if m.query != "" {
			_, n := utf8.DecodeLastRuneInString(m.query)
			m.setQuery(m.query[:len(m.query)-n])
		}
		return m.previewCmd()
	case tea.KeyCtrlU:
		m.setQuery("")
		return m.previewCmd()
	case tea.KeySpace:
		m.setQuery(m.query + " ")
		return m.previewCmd()
	case tea.KeyRunes:
		m.setQuery(m.query + string(k.Runes))
		return m.previewCmd()
	default:
		return nil
	}
	m.clamp()
	return m.previewCmd()
}

func (m *Model) setQuery(q string) {
	cur := m.Current()
	m.query = q
	m.refilter(cur)
}

// View renders the picker at exactly w x h: the list on the left and, when
// wide enough, a palette swatch and sample UI on the right.
func (m *Model) View() string {
	if m.w < 20 || m.h < 6 {
		return ""
	}
	iw, _ := pane.Inner(m.w, m.h)
	listW := iw
	const previewW = 26
	showPreview := iw >= 60
	if showPreview {
		listW = iw - previewW - 2
	}

	q := m.s.Text.Render(m.query) + m.s.Accent.Render("▏")
	if m.query == "" {
		q = m.s.Accent.Render("▏") + m.s.Muted.Render("search themes")
	}
	head := fit(m.s.Accent.Render("/ ")+q, iw)
	rule := m.s.Muted.Render(strings.Repeat("─", iw))

	lh := m.listH()
	var list []string
	end := min(len(m.shown), m.offset+lh)
	for i := m.offset; i < end; i++ {
		list = append(list, m.row(m.themes[m.shown[i]], i == m.cursor, listW))
	}
	if len(m.shown) == 0 {
		list = append(list, m.s.Muted.Render("No themes match."))
	}
	for len(list) < lh {
		list = append(list, "")
	}
	for i := range list {
		list[i] = pad(list[i], listW)
	}
	body := strings.Join(list, "\n")
	if showPreview {
		prev := m.previewPane(previewW, lh)
		body = lipgloss.JoinHorizontal(lipgloss.Top, body, "  ", prev)
	}

	help := "enter use · tab dark/light · ctrl+r reload · esc cancel"
	if m.profile != "" {
		help = "enter use · ctrl+t this profile only · tab dark/light · ctrl+r reload · esc"
	}
	lines := []string{head, rule, body, fit(m.s.Muted.Render(help), iw)}
	extra := strconv.Itoa(len(m.shown)) + " " + m.filter.String()
	return pane.Render(m.s, "Theme", strings.Join(lines, "\n"), m.w, m.h, true,
		pane.WithExtra(m.s.Muted.Render(extra)))
}

func (m *Model) row(t theme.Theme, cur bool, w int) string {
	marker := "  "
	if cur {
		marker = m.ic.Arrow + " "
	}
	tags := ""
	if t.Light {
		tags += " light"
	}
	if t.Source != "" {
		tags += " user"
	}
	if t.Slug == m.orig {
		tags += " " + m.ic.Check
	}
	room := w - lipgloss.Width(marker) - lipgloss.Width(tags)
	name := t.Name
	if lipgloss.Width(name) > room {
		name = ansi.Truncate(name, max(0, room), m.ic.Ellipsis)
	}
	gap := strings.Repeat(" ", max(0, room-lipgloss.Width(name)))
	st := m.s.Text
	if cur {
		st = m.s.Strong
		marker = m.s.Accent.Render(marker)
	}
	return marker + st.Render(name) + gap + m.s.Muted.Render(tags)
}

// previewPane shows the highlighted theme's Base16 palette and a sample of
// the semantic roles. The swatch reads colours from the theme value; the
// sample uses the live styles, which the app has already switched.
func (m *Model) previewPane(w, h int) string {
	t := m.s.Theme
	if ix := m.cursor; ix >= 0 && ix < len(m.shown) {
		t = m.themes[m.shown[ix]]
	}
	base := []lipgloss.Color{t.Base00, t.Base01, t.Base02, t.Base03, t.Base04, t.Base05, t.Base06, t.Base07,
		t.Base08, t.Base09, t.Base0A, t.Base0B, t.Base0C, t.Base0D, t.Base0E, t.Base0F}
	sw := func(cs []lipgloss.Color) string {
		var b strings.Builder
		for _, c := range cs {
			b.WriteString(lipgloss.NewStyle().Background(c).Render("   "))
		}
		return b.String()
	}
	s, ic := m.s, m.ic
	lines := []string{
		s.Strong.Render(fit(t.Name, w)),
		sw(base[:8]),
		sw(base[8:]),
		"",
		s.Online.Render(ic.Online) + " " + s.Text.Render("online") + "  " + s.Offline.Render(ic.Offline) + " " + s.Text.Render("offline"),
		s.Pending.Render(ic.Pending+" pending") + "  " + s.Expiring.Render(ic.Warn+" expiring"),
		s.IP.Render("100.64.0.7") + "  " + s.Tag.Render("tag:web"),
		s.Danger.Render(ic.Cross+" delete") + "  " + s.OK.Render(ic.Check+" approved"),
		s.Accent.Render("accent") + "  " + s.Muted.Render("muted help text"),
		s.RowSel.Render(pad(" selected row", w)),
	}
	for i := range lines {
		lines[i] = pad(fit(lines[i], w), w)
	}
	for len(lines) < h {
		lines = append(lines, strings.Repeat(" ", w))
	}
	return strings.Join(lines[:h], "\n")
}

func fit(s string, w int) string {
	if lipgloss.Width(s) > w {
		return ansi.Truncate(s, max(0, w), "")
	}
	return s
}

func pad(s string, w int) string {
	if d := w - lipgloss.Width(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return fit(s, w)
}
