package policy

import (
	"strings"
	"testing"

	"github.com/tonhe/headsup/internal/api/fake"
)

func addRule(t *testing.T, raw, after, rule string) string {
	t.Helper()
	i := strings.Index(raw, after)
	if i < 0 {
		t.Fatalf("anchor %q not found", after)
	}
	i += len(after)
	return raw[:i] + rule + raw[i:]
}

func TestAddingOneRuleIsOneHunk(t *testing.T) {
	old := fake.DemoSeed().Policy
	rule := "\n    {\"action\": \"accept\", \"src\": [\"group:devs\"], \"dst\": [\"tag:db:5432\"]},"
	next := addRule(t, old, `"dst": ["tag:dns:53"]},`, rule)

	lines := Diff(old, next)
	add, del := Changed(lines)
	if add != 1 || del != 0 {
		t.Fatalf("changed +%d -%d, want +1 -0", add, del)
	}
	hs := Hunks(lines, 3)
	if len(hs) != 1 {
		t.Fatalf("got %d hunks, want 1", len(hs))
	}
	h := hs[0]
	if len(h.Lines) != 7 || h.OldLines != 6 || h.NewLines != 7 {
		t.Errorf("hunk shape: %d lines, -%d +%d", len(h.Lines), h.OldLines, h.NewLines)
	}
	var plus []Line
	for _, l := range h.Lines {
		if l.Op == '+' {
			plus = append(plus, l)
		}
	}
	if len(plus) != 1 || !strings.Contains(plus[0].Text, "tag:db:5432") {
		t.Fatalf("added line: %+v", plus)
	}
	if !strings.HasPrefix(h.Header(), "@@ -") {
		t.Errorf("header %q", h.Header())
	}
	u := Unified("current", "edited", lines, 3)
	if strings.Count(u, "@@ ") != 1 || !strings.Contains(u, "+    {\"action\": \"accept\", \"src\": [\"group:devs\"], \"dst\": [\"tag:db:5432\"]},") {
		t.Errorf("unified:\n%s", u)
	}
}

func TestDiffShapes(t *testing.T) {
	if hs := Hunks(Diff("a\nb\n", "a\nb\n"), 3); len(hs) != 0 {
		t.Fatalf("identical input has %d hunks", len(hs))
	}
	if Unified("a", "b", Diff("x\n", "x\n"), 3) != "" {
		t.Fatal("no changes means an empty unified diff")
	}
	// Two changes far apart are two hunks; close together, one.
	var old []string
	for i := range 30 {
		old = append(old, "line"+string(rune('a'+i%26)))
	}
	o := strings.Join(old, "\n") + "\n"
	far := strings.Replace(strings.Replace(o, "linea\n", "LINEA\n", 1), "lined\nlinee\nlinef\nlineg\nlineh\nlinei\nlinej\nlinek\nlinel\nlinem\nlinen\nlineo\nlinep\nlineq\nliner\nlines\nlinet\n", "lined\nlinee\nlinef\nlineg\nlineh\nlinei\nlinej\nlinek\nlinel\nlinem\nlinen\nlineo\nlinep\nlineq\nliner\nlines\nLINET\n", 1)
	if hs := Hunks(Diff(o, far), 3); len(hs) != 2 {
		t.Errorf("far-apart changes: %d hunks, want 2", len(hs))
	}
	near := strings.Replace(strings.Replace(o, "lineb\n", "LINEB\n", 1), "linee\n", "LINEE\n", 1)
	if hs := Hunks(Diff(o, near), 3); len(hs) != 1 {
		t.Errorf("nearby changes: %d hunks, want 1", len(hs))
	}
	// Insertion at the very top.
	hs := Hunks(Diff("a\nb\n", "z\na\nb\n"), 1)
	if len(hs) != 1 || hs[0].NewStart != 1 || hs[0].Lines[0].Op != '+' {
		t.Errorf("top insertion: %+v", hs)
	}
	// Deletion of everything.
	lines := Diff("a\nb\n", "")
	if a, d := Changed(lines); a != 0 || d != 2 {
		t.Errorf("delete all: +%d -%d", a, d)
	}
	// Pure insertion after context: start falls back to the line before.
	hs = Hunks(Diff("a\nb\nc\nd\ne\n", "a\nb\nc\nd\ne\nf\n"), 0)
	if len(hs) != 1 || hs[0].OldStart != 5 || hs[0].NewStart != 6 {
		t.Errorf("tail insertion: %+v", hs)
	}
}
