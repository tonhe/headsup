package table

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/tonhe/headsup/internal/icons"
	"github.com/tonhe/headsup/internal/ui/uitest"
)

var cellCalls atomic.Int64

type row struct {
	k, name, ip string
	n           int
}

func (r row) Key() string { return r.k }
func (r row) Cell(c string) string {
	cellCalls.Add(1)
	switch c {
	case "name":
		return lipgloss.NewStyle().Bold(true).Render(r.name)
	case "ip":
		return r.ip
	case "n":
		return fmt.Sprint(r.n)
	}
	return ""
}
func (r row) SortValue(c string) string {
	if c == "n" {
		return fmt.Sprint(r.n)
	}
	return ansi.Strip(r.Cell(c))
}

func cols() []Column {
	return []Column{
		{Key: "name", Title: "Name", MinWidth: 10, Flex: true},
		{Key: "ip", Title: "IP", Width: 15, Priority: 2},
		{Key: "n", Title: "N", Width: 6, Priority: 1, Align: lipgloss.Right},
		{Key: "x", Title: "Extra", Width: 20, Priority: 3},
	}
}

func mkRows(n int) []Row {
	out := make([]Row, n)
	for i := range out {
		out[i] = row{k: fmt.Sprintf("k%05d", i), name: fmt.Sprintf("node-%d 漢字", i), ip: fmt.Sprintf("100.64.%d.%d", i/256, i%256), n: n - i}
	}
	return out
}

func newT(w, h int) *Model {
	m := New(uitest.StyleFor(""), icons.Unicode, cols())
	m.SetSize(w, h)
	return m
}

func TestLargeRendersOnlyVisible(t *testing.T) {
	uitest.ForceColor()
	m := newT(80, 24)
	m.SetRows(mkRows(10000))
	cellCalls.Store(0)
	start := time.Now()
	out := m.View()
	if d := time.Since(start); d > 50*time.Millisecond {
		t.Errorf("view took %v", d)
	}
	lines := strings.Split(out, "\n")
	if len(lines) != 24 {
		t.Fatalf("lines = %d", len(lines))
	}
	if c := cellCalls.Load(); c > int64(23*len(cols())) {
		t.Errorf("Cell called %d times, want <= %d", c, 23*len(cols()))
	}
	for i := 0; i < 1000; i++ {
		m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	}
	if _, ok := m.Cursor(); !ok || m.CursorIndex() != 9999 {
		t.Errorf("cursor = %d", m.CursorIndex())
	}
}

func TestCursorStableAcrossReorder(t *testing.T) {
	m := newT(80, 10)
	rows := mkRows(50)
	m.SetRows(rows)
	for i := 0; i < 7; i++ {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	}
	m.Update(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")})
	want, _ := m.Cursor()
	sel := m.Selected()
	rev := make([]Row, len(rows))
	for i, r := range rows {
		rev[len(rows)-1-i] = r
	}
	m.SetRows(rev)
	got, ok := m.Cursor()
	if !ok || got.Key() != want.Key() {
		t.Fatalf("cursor %v want %v", got, want)
	}
	if fmt.Sprint(m.Selected()) != fmt.Sprint(sel) || len(sel) != 1 {
		t.Fatalf("selected %v want %v", m.Selected(), sel)
	}
	// Removing the cursor row keeps the index position.
	m.SetRows(rev[:10])
	if _, ok := m.Cursor(); !ok {
		t.Fatal("cursor lost")
	}
}

func TestColumnDropByPriority(t *testing.T) {
	cases := []struct {
		w    int
		want string
	}{
		{120, "[name ip n x]"},
		{56, "[name ip n x]"},
		{55, "[name ip n]"},
		{35, "[name ip n]"},
		{34, "[name n]"},
		{19, "[name n]"},
		{18, "[name]"},
		{5, "[name]"},
	}
	for _, c := range cases {
		m := newT(c.w, 5)
		if got := fmt.Sprint(m.VisibleColumns()); got != c.want {
			t.Errorf("w=%d got %s want %s", c.w, got, c.want)
		}
	}
}

func TestWidthNeverExceeded(t *testing.T) {
	uitest.ForceColor()
	for _, w := range []int{5, 20, 37, 80, 132} {
		m := newT(w, 8)
		m.SetRows(mkRows(20))
		m.SortBy("name")
		m.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
		for i, l := range strings.Split(m.View(), "\n") {
			if lw := lipgloss.Width(l); lw != w {
				t.Errorf("w=%d line %d width %d: %q", w, i, lw, ansi.Strip(l))
			}
		}
	}
}

func TestSortToggleAndIcon(t *testing.T) {
	m := newT(80, 6)
	m.SetRows(mkRows(5))
	m.SortBy("n")
	if r, _ := m.Cursor(); r.Key() != "k00000" {
		// cursor key preserved at k00000 regardless of order
		t.Fatalf("cursor %s", r.Key())
	}
	if first := m.Rows()[0].Key(); first != "k00004" {
		t.Errorf("asc first = %s", first)
	}
	m.SortBy("n")
	if col, desc := m.Sort(); col != "n" || !desc || m.Rows()[0].Key() != "k00000" {
		t.Errorf("desc sort wrong: %s %v %s", col, desc, m.Rows()[0].Key())
	}
	hdr := ansi.Strip(strings.Split(m.View(), "\n")[0])
	if !strings.Contains(hdr, icons.Unicode.SortDesc) {
		t.Errorf("header missing sort icon: %q", hdr)
	}
}

func TestToggleAll(t *testing.T) {
	m := newT(80, 6)
	m.SetRows(mkRows(5))
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	if len(m.Selected()) != 5 {
		t.Fatal("select all")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	if len(m.Selected()) != 0 {
		t.Fatal("deselect all")
	}
}

func TestFitWide(t *testing.T) {
	for _, w := range []int{1, 2, 3, 4, 7} {
		got := Fit("\x1b[1m漢字漢字\x1b[0m", w, lipgloss.Left, "…")
		if lipgloss.Width(got) != w {
			t.Errorf("w=%d got width %d", w, lipgloss.Width(got))
		}
	}
}
