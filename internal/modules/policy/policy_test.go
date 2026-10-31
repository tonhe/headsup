package policy

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/tonhe/headsup/internal/api"
	"github.com/tonhe/headsup/internal/api/fake"
	"github.com/tonhe/headsup/internal/module"
	pol "github.com/tonhe/headsup/internal/policy"
	"github.com/tonhe/headsup/internal/store"
	"github.com/tonhe/headsup/internal/ui/uitest"
)

const dbRule = "\n    {\"action\": \"accept\", \"src\": [\"group:devs\"], \"dst\": [\"tag:db:5432\"]},"
const dnsAnchor = `"dst": ["tag:dns:53"]},`

func demoBackend() *fake.Backend {
	return fake.Demo(fake.WithClock(func() time.Time { return testNow }))
}

func TestModuleBasics(t *testing.T) {
	m := New()
	if m.ID() != "policy" || m.Order() != 70 || m.Chord() != "a" || m.Modes() != module.ModeAPIHost {
		t.Error("identity")
	}
	if m.Title() == "" || m.Badge(store.New()) != "" || m.Init(nil) != nil {
		t.Error("title/badge/init")
	}
	if len(editorCommand()) == 0 {
		t.Error("editor command")
	}
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "nano -w")
	if got := editorCommand(); strings.Join(got, " ") != "nano -w" {
		t.Errorf("editor = %v", got)
	}
}

func TestEditCheckReviewApply(t *testing.T) {
	b := demoBackend()
	hs := newHarness(t, b, nil)
	old := hs.m.cur
	next := addAfter(t, old, dnsAnchor, dbRule)

	hs.editTo(next)
	if hs.m.mode != modeReview || hs.m.review == nil {
		t.Fatalf("ctrl+s in the editor should validate and open the review, mode=%d toasts=%v", hs.m.mode, hs.ctx.toasts)
	}
	if hs.m.Capturing() {
		t.Error("the review must not capture keys")
	}
	if hs.m.review.conflict != "" || hs.m.review.local {
		t.Errorf("review: %+v", hs.m.review)
	}
	if hs := pol.Hunks(hs.m.review.lines, 3); len(hs) != 1 {
		t.Fatalf("adding one rule should be one hunk, got %d", len(hs))
	}
	if countCalls(b, "CheckPolicy") != 1 || countCalls(b, "SetPolicy") != 0 {
		t.Fatalf("check before apply, nothing applied yet: %+v", b.Calls())
	}
	out := ansi.Strip(hs.view(100, 30))
	if !strings.Contains(out, "Validated by the server") || !strings.Contains(out, "+    {\"action\": \"accept\", \"src\": [\"group:devs\"]") {
		t.Errorf("review view:\n%s", out)
	}

	a := hs.action("policy.apply")
	if !a.Mutating || a.Danger != module.DangerConfirm || !strings.Contains(a.Consequence, "+1 −0") {
		t.Fatalf("the apply in review must be a confirmed mutation: %+v", a)
	}
	hs.do("policy.apply")
	if r := hs.lastResult(); r.ActionID != "policy.apply" || r.Err != nil {
		t.Fatalf("apply result: %+v", r)
	}
	p, _ := b.GetPolicy(context.Background())
	if p.Raw != next {
		t.Fatal("SetPolicy should store the draft byte-for-byte")
	}
	if hs.m.hasDraft || hs.m.mode != modeView || hs.m.cur != next {
		t.Errorf("after apply: draft=%v mode=%d", hs.m.hasDraft, hs.m.mode)
	}
	revs, err := hs.m.hist.List(profileKey(hs.ctx))
	if err != nil || len(revs) != 2 || revs[0].Source != pol.SourceApplied {
		t.Fatalf("history after apply: %+v %v", revs, err)
	}
}

func TestInvalidEditIsRefusedWithPositionAndKept(t *testing.T) {
	b := demoBackend()
	hs := newHarness(t, b, nil)
	bad := strings.Replace(hs.m.cur, `"acls": [`, `"acls": [ oops`, 1)
	hs.editTo(bad)
	if hs.m.mode != modeInvalid || hs.m.invalid == nil {
		t.Fatalf("mode = %d, want invalid", hs.m.mode)
	}
	if hs.m.invalid.line == 0 {
		t.Errorf("the error needs a line: %+v", hs.m.invalid)
	}
	if !hs.m.hasDraft || hs.m.draft != bad {
		t.Fatal("the edit buffer must be kept")
	}
	if countCalls(b, "SetPolicy") != 0 {
		t.Fatal("nothing may be applied")
	}
	out := ansi.Strip(hs.view(100, 30))
	if !strings.Contains(out, "▶") || !strings.Contains(out, "nothing was applied") {
		t.Errorf("invalid view:\n%s", out)
	}
	if strings.Count(out, "column") != 1 {
		t.Errorf("the position should be shown once:\n%s", out)
	}
	// e reopens the editor on the draft.
	hs.do("policy.edit")
	if hs.m.ta.Value() != bad || hs.m.ta.Line() == 0 {
		t.Errorf("editor should hold the draft with the cursor on the error line (line %d)", hs.m.ta.Line())
	}
	hs.key("esc")
	if hs.m.mode != modeView || !hs.m.hasDraft {
		t.Error("esc keeps the draft")
	}
	if !strings.Contains(ansi.Strip(hs.view(100, 30)), "unsaved edits") {
		t.Error("the viewer should mention the unsaved edits")
	}
	if !hs.m.Dirty() {
		t.Error("Dirty() drives the quit guard")
	}
	// Discard.
	hs.do("policy.discard")
	if hs.m.hasDraft {
		t.Error("discard drops the draft")
	}
}

func TestCheckActionAndLocalFallback(t *testing.T) {
	b := fake.Demo(fake.WithCaps(api.NewCapabilities(api.CapPolicyWritable)))
	hs := newHarness(t, b, nil)
	hs.do("policy.check")
	if r := hs.lastResult(); r.ActionID != "policy.check" || r.Err != nil || !strings.Contains(r.Message, "locally") {
		t.Fatalf("check result: %+v", r)
	}
	// An undefined group is caught locally, with its line.
	bad := addAfter(t, hs.m.cur, dnsAnchor, "\n    {\"action\": \"accept\", \"src\": [\"group:nobody\"], \"dst\": [\"*:*\"]},")
	hs.editTo(bad)
	if hs.m.mode != modeInvalid || hs.m.invalid.source != "local" || !strings.Contains(hs.m.invalid.msg, "group:nobody") {
		t.Fatalf("local check: mode=%d %+v", hs.m.mode, hs.m.invalid)
	}
	if countCalls(b, "CheckPolicy") != 0 {
		t.Error("the server check is unavailable and must not be called")
	}
	// A valid edit reviews as locally checked.
	good := addAfter(t, hs.m.cur, dnsAnchor, dbRule)
	hs.m.clearDraft()
	hs.editTo(good)
	if hs.m.review == nil || !hs.m.review.local {
		t.Fatalf("review should be marked local: %+v", hs.m.review)
	}
	if !strings.Contains(ansi.Strip(hs.view(100, 30)), "Checked locally only") {
		t.Error("the review should say the check was local")
	}
}

func TestConflictCheck(t *testing.T) {
	b := demoBackend()
	hs := newHarness(t, b, nil)
	base := hs.m.cur
	hs.do("policy.edit")
	hs.m.ta.SetValue(addAfter(t, base, dnsAnchor, dbRule))
	// Another admin changes the policy while we edit.
	other := strings.Replace(base, `"group:devs":   ["bob@", "carol@"]`, `"group:devs":   ["bob@", "carol@", "dave@"]`, 1)
	if _, err := b.SetPolicy(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	hs.key("ctrl+s")
	if hs.m.review == nil || hs.m.review.conflict == "" {
		t.Fatalf("the review should warn about the concurrent change: %+v", hs.m.review)
	}
	if hs.m.review.against != other {
		t.Error("the diff should be against the server's current policy")
	}
	if !strings.Contains(hs.action("policy.apply").Consequence, "overwrites") {
		t.Error("the confirm text should mention the overwrite")
	}
	out := ansi.Strip(hs.view(120, 40))
	if !strings.Contains(out, "Someone changed the server's policy") {
		t.Errorf("view:\n%s", out)
	}

	// It changes again during the review: the apply refuses and re-reviews.
	third := strings.Replace(other, "dave@", "erin@", 1)
	if _, err := b.SetPolicy(context.Background(), third); err != nil {
		t.Fatal(err)
	}
	sets := countCalls(b, "SetPolicy")
	hs.do("policy.apply")
	if countCalls(b, "SetPolicy") != sets {
		t.Fatal("a policy that changed during the review must not be overwritten")
	}
	var conflict bool
	for _, r := range hs.ctx.results {
		conflict = conflict || api.IsKind(r.Err, api.KindConflict)
	}
	if !conflict {
		t.Fatalf("want a conflict result, got %+v", hs.ctx.results)
	}
	if hs.m.review == nil || hs.m.review.against != third {
		t.Fatal("after a conflict the review is rebuilt against the new policy")
	}
}

func TestRemoteFileModeIsViewOnly(t *testing.T) {
	b := fake.Demo(fake.WithCaps(api.NewCapabilities(api.CapPolicyCheck)))
	hs := newHarness(t, b, nil)
	for _, id := range []string{"policy.edit", "policy.editor", "policy.apply"} {
		if ok, why := hs.enabled(id); ok || !strings.Contains(why, "only be edited on the host") {
			t.Errorf("%s: enabled=%v %q", id, ok, why)
		}
	}
	if ok, _ := hs.enabled("policy.explore"); !ok {
		t.Error("exploring must still work")
	}
	if ok, _ := hs.enabled("policy.check"); !ok {
		t.Error("checking must still work")
	}
	if !strings.Contains(ansi.Strip(hs.view(120, 30)), "View only") {
		t.Error("the viewer should explain view-only")
	}
}

func TestFileModeLearnedOnApply(t *testing.T) {
	b := demoBackend()
	hs := newHarness(t, b, nil)
	hs.editTo(addAfter(t, hs.m.cur, dnsAnchor, dbRule))
	b.FailNext("SetPolicy", api.KindUnsupported)
	hs.do("policy.apply")
	if hs.m.viewOnly == "" || !hs.m.hasDraft {
		t.Fatalf("an unsupported SetPolicy should make the module view-only and keep the draft: %+v", hs.m.viewOnly)
	}
	if ok, _ := hs.enabled("policy.edit"); ok {
		t.Error("editing should now be disabled")
	}
}

func TestReadOnlyDisablesEditing(t *testing.T) {
	hs := newHarness(t, demoBackend(), nil)
	hs.ctx.readOnly = true
	if ok, why := hs.enabled("policy.edit"); ok || !strings.Contains(why, "read-only") {
		t.Errorf("edit in read-only: %v %q", ok, why)
	}
}

func TestEditorRoundTrip(t *testing.T) {
	b := demoBackend()
	hs := newHarness(t, b, nil)
	next := addAfter(t, hs.m.cur, dnsAnchor, dbRule)
	var ran []string
	hs.m.editor = func() []string { return []string{"my-editor", "--wait"} }
	hs.m.execProc = func(c *exec.Cmd, cb tea.ExecCallback) tea.Cmd {
		ran = c.Args
		path := c.Args[len(c.Args)-1]
		if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("temp file should be private: %v %v", fi, err)
		}
		err := os.WriteFile(path, []byte(next), 0o600)
		return func() tea.Msg { return cb(err) }
	}
	hs.do("policy.editor")
	if len(ran) != 3 || ran[0] != "my-editor" || ran[1] != "--wait" {
		t.Fatalf("editor argv = %v", ran)
	}
	if _, err := os.Stat(ran[2]); !os.IsNotExist(err) {
		t.Error("the temp file must be removed")
	}
	if hs.m.mode != modeReview || hs.m.draft != next {
		t.Fatalf("the edited text should go straight to review: mode=%d", hs.m.mode)
	}

	// An editor that fails leaves the draft alone.
	hs.m.clearDraft()
	hs.m.execProc = func(c *exec.Cmd, cb tea.ExecCallback) tea.Cmd {
		return func() tea.Msg { return cb(exec.ErrNotFound) }
	}
	hs.do("policy.editor")
	if hs.m.hasDraft || hs.lastResult().Err == nil {
		t.Errorf("failed editor: draft=%v result=%+v", hs.m.hasDraft, hs.lastResult())
	}
	// An editor that changes nothing.
	hs.m.execProc = func(c *exec.Cmd, cb tea.ExecCallback) tea.Cmd { return func() tea.Msg { return cb(nil) } }
	hs.do("policy.editor")
	if hs.m.hasDraft || !strings.Contains(strings.Join(hs.ctx.toasts, "|"), "No changes") {
		t.Errorf("unchanged editor: %v", hs.ctx.toasts)
	}
}

func TestDiffAndNoChangeApply(t *testing.T) {
	hs := newHarness(t, demoBackend(), nil)
	if ok, _ := hs.enabled("policy.diff"); ok {
		t.Error("diff needs a draft")
	}
	if ok, _ := hs.enabled("policy.apply"); ok {
		t.Error("apply needs a draft")
	}
	hs.do("policy.edit")
	hs.m.ta.SetValue(addAfter(t, hs.m.cur, dnsAnchor, dbRule))
	hs.key("tab")
	hs.key("esc")
	hs.do("policy.diff")
	if hs.m.mode != modeDiff || !strings.Contains(ansi.Strip(hs.view(100, 30)), "tag:db:5432") {
		t.Error("diff view should show the added rule")
	}
	hs.key("esc")
	// Editing back to the original is no draft.
	hs.do("policy.edit")
	hs.m.ta.SetValue(hs.m.cur)
	hs.key("ctrl+s")
	if hs.m.hasDraft || !strings.Contains(strings.Join(hs.ctx.toasts, "|"), "No changes") {
		t.Errorf("no-op edit: draft=%v toasts=%v", hs.m.hasDraft, hs.ctx.toasts)
	}
}

func TestExplore(t *testing.T) {
	hs := newHarness(t, demoBackend(), nil)
	hs.do("policy.explore")
	if hs.m.mode != modeExplore {
		t.Fatal("explore mode")
	}
	// Move to tag:dns.
	for i := 0; i < 40; i++ {
		if it, _ := hs.m.exp.selected(); it.name == "tag:dns" {
			break
		}
		hs.key("j")
	}
	it, _ := hs.m.exp.selected()
	if it.name != "tag:dns" {
		t.Fatalf("selected %+v", it)
	}
	out := ansi.Strip(hs.view(120, 40))
	if !strings.Contains(out, "Where used: tag:dns") || !strings.Contains(out, "acls.dst") {
		t.Errorf("where-used panel:\n%s", out)
	}
	_ = ansi.Strip(hs.view(80, 24))
	hs.key("m")
	if len(hs.ctx.navs) != 1 || hs.ctx.navs[0].Query != "machines tag:dns" {
		t.Errorf("m should navigate to machines filtered by the tag: %+v", hs.ctx.navs)
	}
	hs.key("k")
	hs.key("enter")
	if hs.m.mode != modeView || hs.m.cursor == 0 {
		t.Errorf("enter jumps to the line: mode=%d cursor=%d", hs.m.mode, hs.m.cursor)
	}
	if !strings.Contains(ansi.Strip(hs.view(100, 30)), "›") {
		t.Error("the viewer marks the jumped-to line")
	}
	// A rule row shows the names it mentions.
	hs.do("policy.explore")
	for i := 0; i < 60; i++ {
		if it, _ := hs.m.exp.selected(); strings.HasPrefix(it.label, "#2 accept") {
			break
		}
		hs.key("j")
	}
	if out := ansi.Strip(hs.view(120, 40)); !strings.Contains(out, "Mentions:") {
		t.Errorf("rule detail:\n%s", out)
	}
	hs.key("m")
	if !strings.Contains(strings.Join(hs.ctx.toasts, "|"), "Pick a tag") {
		t.Error("m on a rule explains itself")
	}
	hs.key("esc")
	if hs.m.mode != modeView {
		t.Error("esc leaves the explorer")
	}
}

func TestHistoryRestore(t *testing.T) {
	b := demoBackend()
	hs := newHarness(t, b, nil)
	orig := hs.m.cur
	hs.editTo(addAfter(t, orig, dnsAnchor, dbRule))
	hs.do("policy.apply")

	hs.do("policy.history")
	if hs.m.mode != modeHistory || len(hs.m.hst.revs) != 2 {
		t.Fatalf("history: mode=%d revs=%d err=%v", hs.m.mode, len(hs.m.hst.revs), hs.m.hst.err)
	}
	hs.key("j")
	if hs.m.hst.shown != pol.Sum(orig) {
		t.Fatal("moving should load the selected revision")
	}
	out := ansi.Strip(hs.view(100, 30))
	if !strings.Contains(out, "pre-apply") || !strings.Contains(out, "-    {\"action\": \"accept\", \"src\": [\"group:devs\"], \"dst\": [\"tag:db:5432\"]},") {
		t.Errorf("history view:\n%s", out)
	}
	hs.do("policy.rollback")
	if hs.m.mode != modeReview || hs.m.draft != orig {
		t.Fatalf("restore goes through review: mode=%d", hs.m.mode)
	}
	hs.do("policy.apply")
	if p, _ := b.GetPolicy(context.Background()); p.Raw != orig {
		t.Fatal("the restored revision should be applied")
	}
	// Restoring the live revision is a no-op.
	hs.do("policy.history")
	hs.key("k")
	if r, _ := hs.m.hst.selected(); r.SHA256 != pol.Sum(orig) {
		t.Fatalf("newest revision should be the live one: %+v", hs.m.hst.revs)
	}
	hs.do("policy.rollback")
	if !strings.Contains(strings.Join(hs.ctx.toasts, "|"), "live policy already") {
		t.Errorf("toasts: %v", hs.ctx.toasts)
	}
	if ok, why := func() (bool, string) { hs.m.mode = modeView; return hs.enabled("policy.rollback") }(); ok || !strings.Contains(why, "history") {
		t.Errorf("rollback outside history: %v %q", ok, why)
	}
}

func TestViewsFitAllSizes(t *testing.T) {
	hs := newHarness(t, demoBackend(), nil)
	for _, sz := range [][2]int{{58, 21}, {98, 37}, {140, 45}} {
		w, h := sz[0], sz[1]
		hs.m.mode = modeView
		hs.view(w, h)
		hs.do("policy.explore")
		hs.view(w, h)
		hs.key("esc")
		hs.do("policy.history")
		hs.view(w, h)
		hs.key("esc")
		hs.do("policy.edit")
		hs.view(w, h)
		hs.key("esc")
		hs.key("G")
		hs.key("k")
		hs.key("j")
		hs.view(w, h)
	}
	if !strings.Contains(ansi.Strip(hs.view(100, 30)), "Policy") {
		t.Error("viewer title")
	}
	for _, mode := range []mode{modeEdit, modeReview, modeInvalid, modeExplore, modeHistory, modeFilter, modeDiff, modeView} {
		hs.m.mode = mode
		if len(hs.m.Hints()) == 0 {
			t.Errorf("mode %d has no hints", mode)
		}
	}
}

func TestLoadFailureAndRefresh(t *testing.T) {
	b := demoBackend()
	b.FailNext("GetPolicy", api.KindUnreachable)
	m := New()
	ctx := &tctx{b: b, st: store.New(), s: uitest.StyleFor("tokyo-night")}
	hs := &harness{t: t, m: m, ctx: ctx}
	hs.send(store.UpdatedMsg{})
	if m.loaded || m.loadErr == nil {
		t.Fatal("a failed load is reported")
	}
	if !strings.Contains(ansi.Strip(hs.view(100, 20)), "Could not load the policy") {
		t.Error("load error view")
	}
	if ok, why := hs.enabled("policy.edit"); ok || !strings.Contains(why, "could not be loaded") {
		t.Errorf("edit before load: %q", why)
	}
	hs.send(store.UpdatedMsg{})
	if !m.loaded {
		t.Fatal("the next update retries the load")
	}
}
