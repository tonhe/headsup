package files

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tonhe/headsup/internal/host"
)

func TestCreateNewFile(t *testing.T) {
	e := newEnv(t, Options{})
	ctx := context.Background()
	f := &host.File{Kind: host.FileExtraRecords, Path: "/etc/headscale/extra.json", Mode: 0o640, UID: -1, GID: -1, New: true, Effect: host.EffectLive}
	c, err := e.m.Plan(ctx, f, []byte("[]\n"))
	if err != nil || !c.Valid() || len(c.Diff) != 1 || c.Diff[0].Op != '+' {
		t.Fatalf("plan: %v %+v", err, c)
	}
	a, err := e.m.Apply(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	rp := filepath.Join(e.root, "etc/headscale/extra.json")
	fi, err := os.Stat(rp)
	if err != nil || fi.Mode().Perm() != 0o640 {
		t.Fatalf("created file: %v %v", err, fi)
	}
	if _, err := os.Stat(filepath.Join(e.root, "etc/headscale/.extra.json."+"headsup-tmp")); err == nil {
		t.Error("temp file left behind")
	}
	// Applying the same creation again must not clobber the file.
	if _, err := e.m.Apply(ctx, c); !errors.As(err, new(*host.ErrChangedOnDisk)) {
		t.Errorf("second create: want ErrChangedOnDisk, got %v", err)
	}
	if err := e.m.Rollback(ctx, a); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(rp); !os.IsNotExist(err) {
		t.Error("rollback of a created file should remove it")
	}
	// A missing directory is reported plainly.
	g := &host.File{Kind: host.FileExtraRecords, Path: "/nope/extra.json", New: true}
	c2, _ := e.m.Plan(ctx, g, []byte("[]\n"))
	if _, err := e.m.Apply(ctx, c2); err == nil {
		t.Error("creating in a missing directory should fail")
	}
}

func TestCreateInReadOnlyDir(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	e := newEnv(t, Options{})
	dir := filepath.Join(e.root, "etc/ro")
	if err := os.Mkdir(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	f := &host.File{Kind: host.FileExtraRecords, Path: "/etc/ro/extra.json", New: true}
	c, _ := e.m.Plan(context.Background(), f, []byte("[]\n"))
	_, err := e.m.Apply(context.Background(), c)
	var pe *host.ErrPermission
	if !errors.As(err, &pe) || pe.Need != host.NeedRecordsW {
		t.Errorf("want a records-write permission error, got %v", err)
	}
	if err := e.m.Rollback(context.Background(), host.Applied{File: &host.File{Path: "/etc/ro/none.json", New: true}}); err == nil {
		t.Error("rolling back a missing created file should fail")
	}
}

func TestRollbackCreatedRefusesAfterChange(t *testing.T) {
	e := newEnv(t, Options{})
	ctx := context.Background()
	f := &host.File{Kind: host.FileDERPMap, Path: "/etc/headscale/derp.yaml", Mode: 0o644, New: true}
	c, _ := e.m.Plan(ctx, f, []byte("regions: {}\n"))
	a, err := e.m.Apply(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.root, "etc/headscale/derp.yaml"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := e.m.Rollback(ctx, a); !errors.As(err, new(*host.ErrChangedOnDisk)) {
		t.Errorf("want ErrChangedOnDisk, got %v", err)
	}
}
