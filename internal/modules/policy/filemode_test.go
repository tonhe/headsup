package policy

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/tonhe/headsup/internal/api"
	"github.com/tonhe/headsup/internal/api/fake"
	"github.com/tonhe/headsup/internal/host"
	hostfake "github.com/tonhe/headsup/internal/host/fake"
	"github.com/tonhe/headsup/internal/module"
)

// fileBackend is a headscale in file mode: GetPolicy serves the policy it
// loaded from the file at the last reload, and SetPolicy is refused.
type fileBackend struct {
	*fake.Backend
	h      *countingHost
	served string
	stale  bool // reloads do not pick up the new file
}

func (f *fileBackend) GetPolicy(context.Context) (api.Policy, error) {
	return api.Policy{Raw: f.served}, nil
}

func (f *fileBackend) SetPolicy(context.Context, string) (api.Policy, error) {
	return api.Policy{}, api.Errf(api.KindUnsupported, "update is disabled for modes other than 'database'")
}

// countingHost counts reloads and makes them pick up the file.
type countingHost struct {
	*hostfake.Host
	be      *fileBackend
	reloads int
	failing bool
}

func (c *countingHost) Service() host.ServiceManager { return &countingSvc{c.Host.Service(), c} }

type countingSvc struct {
	host.ServiceManager
	c *countingHost
}

func (s *countingSvc) Reload(ctx context.Context) error {
	s.c.reloads++
	if s.c.failing {
		return errors.New("headscale.service: reload failed (policy rejected)")
	}
	if !s.c.be.stale {
		b, _ := s.c.Host.File(hostfake.PolicyPath)
		s.c.be.served = string(b)
	}
	return nil
}

func fileHarness(t *testing.T) (*harness, *countingHost, *fileBackend) {
	t.Helper()
	h := &countingHost{Host: hostfake.New()}
	seed, _ := h.File(hostfake.PolicyPath)
	be := &fileBackend{Backend: fake.Demo(), h: h, served: string(seed)}
	h.be = be
	hs := newHarness(t, be, h)
	if hs.m.file == nil || hs.m.cur != string(seed) {
		t.Fatalf("file mode should load the policy file: %+v", hs.m.file)
	}
	return hs, h, be
}

const fileRule = "\n    {\"action\": \"accept\", \"src\": [\"group:admins\"], \"dst\": [\"tag:server:443\"]},"

func TestFileModeInvalidLeavesFileUntouched(t *testing.T) {
	hs, h, _ := fileHarness(t)
	before, _ := h.File(hostfake.PolicyPath)
	hs.editTo(strings.Replace(hs.m.cur, `"acls": [`, `"acls": [ nope`, 1))
	if hs.m.mode != modeInvalid || hs.m.invalid == nil || hs.m.invalid.line == 0 {
		t.Fatalf("want invalid with a line: mode=%d %+v", hs.m.mode, hs.m.invalid)
	}
	if ok, _ := hs.enabled("policy.apply"); ok && hs.action("policy.apply").Mutating {
		t.Fatal("an invalid draft must not reach the confirmed apply")
	}
	after, _ := h.File(hostfake.PolicyPath)
	if string(after) != string(before) || h.Backups() != 0 || h.reloads != 0 {
		t.Fatal("the file must be byte-identical, with no backup and no reload")
	}
	if !hs.m.hasDraft {
		t.Error("the draft is kept")
	}
}

func TestFileModeValidIsWrittenReloadedActive(t *testing.T) {
	hs, h, be := fileHarness(t)
	next := addAfter(t, hs.m.cur, `"acls": [`, fileRule)
	hs.editTo(next)
	if hs.m.mode != modeReview || hs.m.review.change == nil {
		t.Fatalf("file mode reviews a managed-file change: mode=%d", hs.m.mode)
	}
	a := hs.action("policy.apply")
	if a.Needs != host.NeedPolicyW || !a.Mutating || a.Danger != module.DangerConfirm {
		t.Fatalf("apply action: %+v", a)
	}
	if tg := a.Targets(hs.ctx); len(tg) != 1 || tg[0].ID != hostfake.PolicyPath {
		t.Errorf("targets: %+v", tg)
	}
	if !strings.Contains(ansi.Strip(hs.view(100, 30)), "headscale policy check") {
		t.Error("the review should say which validator ran")
	}
	hs.do("policy.apply")
	got, _ := h.File(hostfake.PolicyPath)
	if string(got) != next {
		t.Fatal("the file should hold the draft")
	}
	if h.Backups() != 1 || h.reloads != 1 {
		t.Fatalf("backups=%d reloads=%d, want 1 and 1", h.Backups(), h.reloads)
	}
	var r module.ActionResult
	for _, x := range hs.ctx.results {
		if x.ActionID == "policy.apply" {
			r = x
		}
	}
	if r.Err != nil || !strings.Contains(r.Message, "reports it active") {
		t.Fatalf("result: %+v", r)
	}
	if be.served != next || hs.m.applied != nil || hs.m.cur != next {
		t.Error("the new policy is active and no rollback is offered")
	}
}

func TestFileModeReloadFailureOffersRollback(t *testing.T) {
	hs, h, _ := fileHarness(t)
	orig := hs.m.cur
	next := addAfter(t, orig, `"acls": [`, fileRule)
	h.failing = true
	hs.editTo(next)
	hs.do("policy.apply")
	if hs.m.applied == nil {
		t.Fatal("a failed reload must offer a rollback")
	}
	if r := hs.lastResult(); r.Err == nil && !strings.Contains(strings.Join(resultErrs(hs), "|"), "Press u to roll back") {
		t.Fatalf("results: %v", resultErrs(hs))
	}
	if !strings.Contains(ansi.Strip(hs.view(200, 30)), "Press u to roll back") {
		t.Error("the banner offers the rollback")
	}
	rb := hs.action("policy.rollback")
	if !rb.Mutating || rb.Danger != module.DangerConfirm || rb.Needs != host.NeedPolicyW {
		t.Fatalf("rollback action: %+v", rb)
	}
	h.failing = false
	hs.do("policy.rollback")
	got, _ := h.File(hostfake.PolicyPath)
	if string(got) != orig {
		t.Fatal("rollback restores the previous file")
	}
	if hs.m.applied != nil || h.reloads != 2 {
		t.Errorf("after rollback: applied=%v reloads=%d", hs.m.applied, h.reloads)
	}
	if hs.m.cur != orig {
		t.Error("the viewer reloads the restored file")
	}
}

func TestFileModeStaleReloadOffersRollback(t *testing.T) {
	hs, _, be := fileHarness(t)
	be.stale = true
	hs.editTo(addAfter(t, hs.m.cur, `"acls": [`, fileRule))
	hs.do("policy.apply")
	if hs.m.applied == nil || !strings.Contains(hs.m.reloadErr, "previous policy") {
		t.Fatalf("headscale still serving the old policy must offer a rollback: %q", hs.m.reloadErr)
	}
}

func TestFileModeChangedOnDisk(t *testing.T) {
	hs, h, _ := fileHarness(t)
	next := addAfter(t, hs.m.cur, `"acls": [`, fileRule)
	hs.do("policy.edit")
	hs.m.ta.SetValue(next)
	h.ChangeOnDisk(hostfake.PolicyPath, []byte(strings.Replace(hs.m.cur, `"bob@"`, `"bob@", "dan@"`, 1)))
	hs.key("ctrl+s")
	if hs.m.review == nil || !strings.Contains(hs.m.review.conflict, "changed on disk") {
		t.Fatalf("the review warns about the external change: %+v", hs.m.review)
	}
	hs.do("policy.apply")
	if h.Backups() != 0 {
		t.Fatal("a file changed since it was opened must not be written")
	}
	if !strings.Contains(strings.Join(resultErrs(hs), "|"), "changed on disk") {
		t.Errorf("results: %v", resultErrs(hs))
	}
	if !hs.m.hasDraft {
		t.Error("the draft survives")
	}
}

func TestFileModePermissionGate(t *testing.T) {
	hs, h, _ := fileHarness(t)
	h.FailPermission(host.NeedPolicyW, "sudo usermod -aG headscale $USER")
	if ok, why := hs.enabled("policy.edit"); ok || !strings.Contains(why, "usermod") {
		t.Errorf("edit without write permission: %v %q", ok, why)
	}
}

func TestFilterView(t *testing.T) {
	hs, _, _ := fileHarness(t)
	hs.do("policy.filter")
	if hs.m.mode != modeFilter || hs.m.filt.err != nil || len(hs.m.filt.rules) != 2 {
		t.Fatalf("filter: mode=%d err=%v rules=%d", hs.m.mode, hs.m.filt.err, len(hs.m.filt.rules))
	}
	out := ansi.Strip(hs.view(100, 30))
	if !strings.Contains(out, "Compiled packet filter") || !strings.Contains(out, "100.64.0.1:53") {
		t.Errorf("filter view:\n%s", out)
	}
	// Without a reachable debug listener the view explains itself.
	hs.m.mode = modeView
	hs.ctx.h.(*countingHost).Service().Stop(context.Background())
	hs.do("policy.filter")
	if !errors.Is(hs.m.filt.err, errNoFilter) {
		t.Errorf("want errNoFilter, got %v", hs.m.filt.err)
	}
	hs.view(80, 24)
}

func resultErrs(hs *harness) []string {
	var out []string
	for _, r := range hs.ctx.results {
		if r.Err != nil {
			out = append(out, r.Err.Error())
		}
	}
	return out
}
