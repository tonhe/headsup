package policy

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	pol "github.com/tonhe/headsup/internal/policy"
	"github.com/tonhe/headsup/internal/store"
)

func TestAccessPreview(t *testing.T) {
	b := demoBackend()
	hs := newHarness(t, b, nil)
	hs.ctx.st.Apply(store.Fetch(context.Background(), b, nil, store.SliceNodes))
	if len(hs.ctx.st.Nodes) == 0 {
		t.Fatal("no demo nodes")
	}
	hs.do("policy.preview")
	if hs.m.mode != modePreview || len(hs.m.prev.subjects) == 0 {
		t.Fatalf("preview mode with sources: mode=%d n=%d", hs.m.mode, len(hs.m.prev.subjects))
	}
	out := ansi.Strip(hs.view(120, 40))
	for _, want := range []string{"Source", "can reach", "best-effort", "Best-effort preview"} {
		if !strings.Contains(out, want) {
			t.Errorf("preview missing %q:\n%s", want, out)
		}
	}
	_ = hs.view(80, 24)

	// Walk to the first tag and list its machines.
	for i := 0; i < len(hs.m.prev.subjects); i++ {
		if s, _ := hs.m.prev.selected(); s.Kind == pol.SubjectTag {
			break
		}
		hs.key("j")
	}
	s, _ := hs.m.prev.selected()
	if s.Kind != pol.SubjectTag {
		t.Fatalf("no tag source: %+v", s)
	}
	hs.key("m")
	if n := len(hs.ctx.navs); n != 1 || hs.ctx.navs[0].Query != "machines "+s.Name {
		t.Errorf("m navigates to the tag's machines: %+v", hs.ctx.navs)
	}
	for i := 0; i < 3; i++ {
		hs.key("k")
	}
	hs.key("pgdown")
	hs.key("pgup")
	_ = hs.view(120, 40)
	if got := hs.m.Hints(); len(got) == 0 || got[0].Desc != "source" {
		t.Errorf("hints = %+v", got)
	}
	hs.key("esc")
	if hs.m.mode != modeView {
		t.Error("esc leaves the preview")
	}

	// With unsaved edits the preview evaluates the draft.
	hs.m.hasDraft, hs.m.draft = true, `{"acls": []}`
	if _, under := hs.m.previewDoc(); under != "your unsaved edits" {
		t.Errorf("under = %q", under)
	}
	hs.do("policy.preview")
	out = ansi.Strip(hs.view(120, 40))
	if !strings.Contains(out, "unsaved edits") || !strings.Contains(out, "Nothing") {
		t.Errorf("draft preview:\n%s", out)
	}
}
