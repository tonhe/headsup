package local

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/host/service"
)

// The records file is written live through the same pipeline, can be
// created new, and rolls back.
func TestRecordsFileThroughPipeline(t *testing.T) {
	rf := func(ctx context.Context, name string, args ...string) (service.Result, error) {
		return service.Result{}, nil
	}
	env := host.Env{ConfigPath: "/etc/headscale/config.yaml", RecordsPath: "/etc/headscale/extra.json"}
	h, root := setup(t, env, rf)
	ctx := context.Background()
	if h.Env().RecordsPath != env.RecordsPath || h.Files() == nil {
		t.Fatal("env/files accessors")
	}
	if _, err := h.Open(host.FileExtraRecords, ""); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing records file: %v", err)
	}
	f := &host.File{Kind: host.FileExtraRecords, Path: env.RecordsPath, New: true, Mode: 0o640, Effect: host.EffectLive}
	c, err := h.Plan(ctx, f, []byte(`[{"name":"a.example.com","type":"A","value":"100.64.0.1"}]`+"\n"))
	if err != nil || !c.Valid() || c.Danger != host.DangerNone {
		t.Fatalf("plan: %v %+v", err, c)
	}
	a, err := h.Apply(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := h.Open(host.FileExtraRecords, "")
	if err != nil || opened.Effect != host.EffectLive {
		t.Fatalf("open created file: %v", err)
	}
	bad, _ := h.Plan(ctx, opened, []byte(`[{"name":"a","type":"A","value":"fd7a::1"}]`))
	if bad.Valid() {
		t.Error("an A record with an IPv6 value must be invalid")
	}
	if err := h.Rollback(ctx, a); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "etc/headscale/extra.json")); !os.IsNotExist(err) {
		t.Error("rollback should remove the created file")
	}
}

func TestDefaultPaths(t *testing.T) {
	h, _ := setup(t, host.Env{PolicyMode: "database"}, nil)
	for _, k := range []host.FileKind{host.FileConfig, host.FilePolicy, host.FileExtraRecords, host.FileDERPMap} {
		if _, err := h.Open(k, ""); !errors.Is(err, host.ErrNoHost) {
			t.Errorf("%v without a path: %v", k, err)
		}
	}
	h2, _ := setup(t, host.Env{PolicyMode: "file"}, nil)
	if _, err := h2.Open(host.FilePolicy, ""); !errors.Is(err, host.ErrNoHost) {
		t.Errorf("policy without a path: %v", err)
	}
	h3, _ := setup(t, host.Env{PolicyMode: "file", PolicyPath: "/etc/headscale/policy.hujson"}, nil)
	if _, err := h3.Open(host.FilePolicy, ""); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("policy path is used: %v", err)
	}
}

func TestNoDebugAndPolicyCheck(t *testing.T) {
	ctx := context.Background()
	var d NoDebug
	if d.Available() {
		t.Error("NoDebug is never available")
	}
	if _, err := d.RegistrationCache(ctx); !errors.Is(err, host.ErrNoHost) {
		t.Error("registration cache")
	}
	if _, err := d.Routes(ctx); !errors.Is(err, host.ErrNoHost) {
		t.Error("routes")
	}
	if _, err := d.Ping(ctx, 1); !errors.Is(err, host.ErrNoHost) {
		t.Error("ping")
	}
	h, _ := setup(t, host.Env{}, nil)
	if _, err := h.NodeExtras(ctx, []uint64{1}); !errors.Is(err, host.ErrNoHost) {
		t.Error("no database")
	}
	h2, _ := setup(t, host.Env{DB: host.DBSQLite, DBPath: "/x.sqlite", Perms: host.Perms{host.NeedDBRead: {Reason: "no", FixCommand: "fix"}}}, nil)
	var pe *host.ErrPermission
	if _, err := h2.NodeExtras(ctx, []uint64{1}); !errors.As(err, &pe) || pe.FixCommand != "fix" {
		t.Errorf("db permission: %v", err)
	}

	fail := func(ctx context.Context, name string, args ...string) (service.Result, error) {
		return service.Result{Exit: 1}, nil
	}
	is, err := policyCheck(ctx, fail, "/tmp/p.hujson")
	if err != nil || len(is) != 1 || !strings.Contains(is[0].Message, "status 1") || is[0].Source != "policy check" {
		t.Errorf("policy check failure: %v %+v", err, is)
	}
	broken := func(ctx context.Context, name string, args ...string) (service.Result, error) {
		return service.Result{}, errors.New("not found")
	}
	if _, err := policyCheck(ctx, broken, "/tmp/p.hujson"); err == nil {
		t.Error("a validator that cannot run is an error")
	}
	if hasError([]host.Issue{{Severity: host.SevWarn}}) || !hasError([]host.Issue{{Severity: host.SevError}}) {
		t.Error("hasError")
	}
}
