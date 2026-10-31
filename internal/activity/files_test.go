package activity

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestAddFilesAndRoundTrip(t *testing.T) {
	e := Entry{ActionID: "dns.apply", Targets: []Target{{Kind: "file", ID: "/etc/headscale/config.yaml", Name: "/etc/headscale/config.yaml"}}}
	e.AddFiles(nil)
	if e.DiffStats != nil {
		t.Fatal("no files must leave stats unset")
	}
	e.AddFiles([]FileChange{
		{Path: "/etc/headscale/config.yaml", BackupID: "config.yaml.20260301-120000", DiffStats: DiffStats{Added: 2, Removed: 1}},
		{Path: "/etc/headscale/records.json", BackupID: "", DiffStats: DiffStats{Added: 3}},
	})
	if e.DiffStats == nil || e.DiffStats.Added != 5 || e.DiffStats.Removed != 1 {
		t.Errorf("stats = %+v", e.DiffStats)
	}
	if e.BackupID != "config.yaml.20260301-120000" {
		t.Errorf("backup = %q", e.BackupID)
	}
	if len(e.Targets) != 2 || e.Targets[1].ID != "/etc/headscale/records.json" {
		t.Errorf("targets = %+v", e.Targets)
	}
	two := Entry{}
	two.AddFiles([]FileChange{{Path: "a", BackupID: "x"}, {Path: "b", BackupID: "y"}})
	if two.BackupID != "" {
		t.Errorf("different backups must not pick one: %q", two.BackupID)
	}

	f, err := Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	e.Files[0].Path = "/tmp/hskey-auth-leak"
	if err := f.Log(e); err != nil {
		t.Fatal(err)
	}
	got, err := f.Recent(1)
	if err != nil || len(got) != 1 {
		t.Fatal(got, err)
	}
	if len(got[0].Files) != 2 || got[0].Files[1].DiffStats.Added != 3 || strings.Contains(got[0].Files[0].Path, "hskey-") {
		t.Errorf("files = %+v", got[0].Files)
	}
}

func TestMatches(t *testing.T) {
	e := Entry{ActionID: "service.restart", Profile: "host:hs01", Targets: []Target{{Kind: "service", Name: "headscale"}},
		Result: Err("permission", "denied"), Files: []FileChange{{Path: "/etc/headscale/config.yaml"}}}
	for _, q := range []string{"", "RESTART", "hs01", "headscale", "permission", "config.yaml"} {
		if !e.Matches(q) {
			t.Errorf("should match %q", q)
		}
	}
	if e.Matches("machines") {
		t.Error("should not match machines")
	}
}

func TestMemory(t *testing.T) {
	m := &Memory{Max: 2}
	for _, id := range []string{"a", "b", "c"} {
		if err := m.Log(Entry{ActionID: id, Result: Err("api", "bad hskey-auth-secret1")}); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := m.Recent(0)
	if len(got) != 2 || got[0].ActionID != "c" || got[1].ActionID != "b" || got[0].Time.IsZero() {
		t.Fatalf("got %+v", got)
	}
	if strings.Contains(got[0].Result.Message, "hskey-") {
		t.Error("memory entries must be redacted")
	}
	if one, _ := m.Recent(1); len(one) != 1 {
		t.Error("limit")
	}
	var _ Logger = m
}
