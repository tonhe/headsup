package fake

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/host/hsconfig"
)

func TestSeedIsValid(t *testing.T) {
	h := Demo()
	for _, k := range []host.FileKind{host.FileConfig, host.FilePolicy, host.FileExtraRecords} {
		f, err := h.Open(k, "")
		if err != nil {
			t.Fatal(err)
		}
		c, err := h.Plan(context.Background(), f, f.Raw)
		if err != nil {
			t.Fatal(err)
		}
		if !c.Valid() {
			t.Errorf("%s seed invalid: %+v", k, c.Issues)
		}
	}
	d, _ := hsconfig.Parse(SeedConfig)
	if v, _ := d.Get("policy.path"); v != PolicyPath {
		t.Errorf("policy.path = %q", v)
	}
	if v, _ := d.Get("dns.extra_records_path"); v != RecordsPath {
		t.Errorf("extra_records_path = %q", v)
	}
	if !h.Env().Usable() {
		t.Error("demo env not usable")
	}
}

func TestFilePipeline(t *testing.T) {
	ctx := context.Background()
	h := New()
	f, _ := h.Open(host.FileConfig, "")
	d, _ := hsconfig.Parse(f.Raw)
	_ = d.Set("log.level", "debug")
	c, _ := h.Plan(ctx, f, d.Bytes())
	if !c.Valid() || c.Danger != host.DangerConfirm || c.Effect != host.EffectRestart {
		t.Fatalf("change %+v", c)
	}
	a, err := h.Apply(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := h.File(ConfigPath); string(got) != string(d.Bytes()) || h.Backups() != 1 {
		t.Fatal("not written")
	}
	if err := h.Rollback(ctx, a); err != nil {
		t.Fatal(err)
	}
	if got, _ := h.File(ConfigPath); string(got) != string(SeedConfig) {
		t.Fatal("rollback not byte-exact")
	}

	// Changed on disk.
	f, _ = h.Open(host.FileConfig, "")
	c, _ = h.Plan(ctx, f, append(append([]byte{}, f.Raw...), "# x\n"...))
	h.ChangeOnDisk(ConfigPath, []byte("server_url: https://other\n"))
	var ec *host.ErrChangedOnDisk
	if _, err := h.Apply(ctx, c); !errors.As(err, &ec) || !strings.Contains(string(ec.Current), "other") {
		t.Fatalf("err = %v", err)
	}

	// Permission.
	h.FailPermission(host.NeedPolicyW, "")
	pf, _ := h.Open(host.FilePolicy, "")
	pc, _ := h.Plan(ctx, pf, pf.Raw)
	var pe *host.ErrPermission
	if _, err := h.Apply(ctx, pc); !errors.As(err, &pe) || pe.FixCommand == "" {
		t.Fatalf("err = %v", err)
	}
	if h.Env().Perms.Has(host.NeedPolicyW) {
		t.Error("env should reflect the injected failure")
	}

	// Configtest failure blocks config changes.
	h.ClearFailures()
	h.SetConfigtestFailure(host.Issue{Severity: host.SevError, Message: "listen_addr: bind: address already in use"})
	f, _ = h.Open(host.FileConfig, "")
	c, _ = h.Plan(ctx, f, f.Raw)
	var ei *host.ErrInvalid
	if _, err := h.Apply(ctx, c); !errors.As(err, &ei) {
		t.Fatalf("err = %v", err)
	}
	if is, _ := h.Service().Configtest(ctx, ConfigPath); len(is) != 1 {
		t.Errorf("configtest %+v", is)
	}

	// Invalid policy.
	pf, _ = h.Open(host.FilePolicy, "")
	if pc, _ := h.Plan(ctx, pf, []byte("{ nope")); pc.Valid() {
		t.Error("bad policy accepted")
	}
}

func TestServiceStateMachine(t *testing.T) {
	ctx := context.Background()
	h := New()
	s := h.Service()
	steps := []struct {
		op      func(context.Context) error
		state   string
		wantErr bool
	}{
		{s.Reload, "active", false},
		{s.Stop, "inactive", false},
		{s.Reload, "inactive", true},
		{s.Start, "active", false},
		{s.Restart, "active", false},
	}
	for i, st := range steps {
		err := st.op(ctx)
		if (err != nil) != st.wantErr {
			t.Fatalf("step %d err %v", i, err)
		}
		if got, _ := s.Status(ctx); got.State != st.state {
			t.Fatalf("step %d state %q want %q", i, got.State, st.state)
		}
	}
	h.SetUnhealthyRestart(true)
	if err := s.Restart(ctx); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.Status(ctx); st.Running || st.State != "failed" {
		t.Fatalf("unhealthy restart -> %+v", st)
	}
	if h.Debug().Available() {
		t.Error("debug available while failed")
	}
	if _, err := h.Metrics(ctx); err == nil {
		t.Error("metrics while failed")
	}
	h.SetUnhealthyRestart(false)
	h.FailPermission(host.NeedService, "")
	var pe *host.ErrPermission
	if err := s.Restart(ctx); !errors.As(err, &pe) || pe.FixCommand != "sudo systemctl restart headscale" {
		t.Fatalf("err %v", err)
	}
	if err := s.Stop(ctx); !errors.As(err, &pe) || pe.FixCommand != "sudo systemctl stop headscale" {
		t.Fatalf("err %v", err)
	}
}

func TestDebugAndExtras(t *testing.T) {
	ctx := context.Background()
	h := New(WithNodes([]uint64{11, 12, 13}))
	regs, err := h.Debug().RegistrationCache(ctx)
	if err != nil || len(regs) != 2 {
		t.Fatalf("%+v %v", regs, err)
	}
	r, _ := h.Debug().Routes(ctx)
	if len(r) != 1 || r[0].Prefix != HAPrefix || r[0].Primary != 11 || !reflect.DeepEqual(r[0].Candidates, []uint64{11, 12}) {
		t.Fatalf("routes %+v", r)
	}
	if d, err := h.Debug().Ping(ctx, 12); err != nil || d <= 0 {
		t.Fatalf("ping %v %v", d, err)
	}
	if _, err := h.Debug().Ping(ctx, 99); err == nil {
		t.Error("ping of unknown node succeeded")
	}
	m1, _ := h.Metrics(ctx)
	m2, _ := h.Metrics(ctx)
	if m1.Nodes != 3 || m2.MapResponsesTotal <= m1.MapResponsesTotal {
		t.Errorf("metrics %+v %+v", m1, m2)
	}
	a, _ := h.NodeExtras(ctx, []uint64{11, 999})
	b, _ := h.NodeExtras(ctx, []uint64{11, 999})
	if !reflect.DeepEqual(a, b) || len(a) != 2 || a[11].OS == "" || a[999].ClientVersion == "" || a[11].PreferredDERP == 0 {
		t.Fatalf("extras %+v", a)
	}
	if len(a[11].RequestTags) == 0 {
		t.Error("HA router should carry tag:router")
	}
}

func TestLogs(t *testing.T) {
	h := New(WithLogInterval(5 * time.Millisecond))
	ch, err := h.Logs(context.Background(), host.LogQuery{Tail: 10})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for e := range ch {
		n++
		if e.Msg == "" || e.Time.IsZero() {
			t.Errorf("entry %+v", e)
		}
	}
	if n != 10 {
		t.Fatalf("tail %d", n)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, _ = h.Logs(ctx, host.LogQuery{Tail: 1, Follow: true})
	got := 0
	refs := 0
	for e := range ch {
		got++
		refs += len(e.NodeRefs)
		if got == 6 {
			cancel()
		}
	}
	if got < 6 || refs == 0 {
		t.Fatalf("follow got %d entries, %d node refs", got, refs)
	}
	h.FailPermission(host.NeedLogs, "sudo usermod -aG systemd-journal $USER && newgrp systemd-journal")
	if _, err := h.Logs(context.Background(), host.LogQuery{}); err == nil {
		t.Error("expected permission error")
	}
}
