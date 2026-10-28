package toast

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/tonhe/headsup/internal/icons"
	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/ui/uitest"
)

func TestStack(t *testing.T) {
	uitest.ForceColor()
	st := New(uitest.StyleFor(""), icons.Unicode)
	for i, l := range []module.ToastLevel{module.ToastInfo, module.ToastSuccess, module.ToastWarn, module.ToastError} {
		_ = st.Push(l, strings.Repeat("message ", i+1))
	}
	if st.Len() != 4 {
		t.Fatal("len")
	}
	v := st.View(80)
	if got := strings.Count(ansi.Strip(v), "╭"); got != MaxVisible {
		t.Errorf("visible boxes = %d", got)
	}
	if uitest.MaxWidth(v) > MaxWidth {
		t.Errorf("toast too wide: %d", uitest.MaxWidth(v))
	}
	st.Update(DismissMsg{ID: 1})
	if st.Len() != 3 || st.Items()[0].ID != 2 {
		t.Fatal("dismiss")
	}
	base := strings.Repeat(strings.Repeat("x", 80)+"\n", 24)
	out := Overlay(base, v, 80, 24)
	lines := strings.Split(out, "\n")
	if len(lines) != 24 {
		t.Fatalf("lines %d", len(lines))
	}
	for i, l := range lines {
		if lipgloss.Width(l) != 80 {
			t.Errorf("line %d width %d", i, lipgloss.Width(l))
		}
	}
	if !strings.HasPrefix(ansi.Strip(lines[23]), strings.Repeat("x", 80)) || !strings.HasPrefix(ansi.Strip(lines[22]), "xxxx") {
		t.Error("base destroyed")
	}
}

func TestDurations(t *testing.T) {
	cases := map[module.ToastLevel]time.Duration{module.ToastInfo: 4 * time.Second, module.ToastError: 8 * time.Second}
	for l, d := range cases {
		if Duration(l) != d {
			t.Errorf("%v: %v", l, Duration(l))
		}
	}
}
