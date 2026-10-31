package changeset

import (
	"context"
	"errors"
	"testing"

	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/host/fake"
)

func TestPlanApplyRollback(t *testing.T) {
	ctx := context.Background()
	h := fake.New()
	cfg, err := h.Open(host.FileConfig, fake.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	rec, _ := h.Open(host.FileExtraRecords, fake.RecordsPath)
	newFile := &host.File{Kind: host.FileExtraRecords, Path: "/var/lib/headscale/more.json", New: true, Effect: host.EffectLive}
	items := []Item{
		{File: rec, Next: []byte(`[{"name":"a.example.com","type":"A","value":"100.64.0.9"}]`)},
		{File: newFile, Next: []byte("[]\n")},
		{File: cfg, Next: cfg.Raw}, // unchanged: skipped
		{},
	}
	cs, err := Plan(ctx, h, items)
	if err != nil || len(cs) != 2 {
		t.Fatalf("plan: %v %d", err, len(cs))
	}
	if !Valid(cs) || Effect(cs) != host.EffectLive || Danger(cs) != host.DangerNone {
		t.Errorf("summary: valid=%v effect=%v danger=%v", Valid(cs), Effect(cs), Danger(cs))
	}
	as, err := Apply(ctx, h, cs)
	if err != nil || len(as) != 2 {
		t.Fatalf("apply: %v", err)
	}
	if _, ok := h.File("/var/lib/headscale/more.json"); !ok {
		t.Error("new file should exist")
	}
	if err := Rollback(ctx, h, as); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.File("/var/lib/headscale/more.json"); ok {
		t.Error("rollback should remove the created file")
	}
	if b, _ := h.File(fake.RecordsPath); string(b) != string(fake.SeedRecords) {
		t.Error("rollback should restore the records file")
	}
}

func TestApplyUndoesEarlierWritesOnFailure(t *testing.T) {
	ctx := context.Background()
	h := fake.New()
	rec, _ := h.Open(host.FileExtraRecords, fake.RecordsPath)
	cfg, _ := h.Open(host.FileConfig, fake.ConfigPath)
	cs, err := Plan(ctx, h, []Item{
		{File: rec, Next: []byte("[]\n")},
		{File: cfg, Next: append(append([]byte(nil), cfg.Raw...), "\n# edited\n"...)},
	})
	if err != nil || len(cs) != 2 {
		t.Fatalf("plan: %v", err)
	}
	h.FailPermission(host.NeedConfigW, "")
	_, err = Apply(ctx, h, cs)
	var pe *host.ErrPermission
	if !errors.As(err, &pe) {
		t.Fatalf("want permission error, got %v", err)
	}
	if b, _ := h.File(fake.RecordsPath); string(b) != string(fake.SeedRecords) {
		t.Errorf("records file should be rolled back, got %s", b)
	}
	// A rollback that itself fails is reported.
	h.FailPermission(host.NeedRecordsW, "")
	if err := Rollback(ctx, h, []host.Applied{{File: rec, BackupPath: "x"}}); err == nil {
		t.Error("rollback error should be reported")
	}
}
