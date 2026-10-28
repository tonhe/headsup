package db

import (
	"context"
	"database/sql"
	"errors"
	"net/netip"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/host/files"
)

const schema = `
CREATE TABLE migrations (id VARCHAR(255) PRIMARY KEY);
INSERT INTO migrations VALUES ('202312101416'), ('202607241200-clear-tagged-node-expiry');
CREATE TABLE nodes(
  id integer PRIMARY KEY AUTOINCREMENT,
  machine_key text, node_key text, disco_key text,
  endpoints text, host_info text,
  ipv4 text, ipv6 text, hostname text, given_name varchar(63),
  user_id integer, register_method text, forced_tags text, auth_key_id integer,
  last_seen datetime, expiry datetime, approved_routes text,
  created_at datetime, updated_at datetime, deleted_at datetime
);
INSERT INTO nodes (id, hostname, host_info, endpoints) VALUES
 (1, 'laptop', '{"IPNVersion":"1.90.4-t1234","OS":"linux","OSVersion":"Debian 13","Hostname":"laptop","RequestTags":["tag:dev"],"NetInfo":{"PreferredDERP":1,"DERPLatency":{"1-v4":0.0123,"2-v4":0.045}}}', '["203.0.113.5:41641","[2001:db8::5]:41641"]'),
 (2, 'phone', '{"IPNVersion":"1.88.0","OS":"iOS","OSVersion":"18.1"}', NULL),
 (3, 'broken', 'not json', 'also not json');
`

func makeDB(t *testing.T, ddl string, wal bool) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "db.sqlite")
	w, err := sql.Open("sqlite", "file:"+p)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if wal {
		if _, err := w.Exec("PRAGMA journal_mode=WAL"); err != nil {
			t.Fatal(err)
		}
	}
	for _, stmt := range strings.Split(ddl, ";") {
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		if _, err := w.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	return p
}

func TestNodeExtras(t *testing.T) {
	d, err := Open(host.DBSQLite, makeDB(t, schema, false), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	got, err := d.NodeExtras(context.Background(), []uint64{1, 2, 3, 99})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d rows", len(got))
	}
	a := got[1]
	if a.OS != "linux" || a.OSVersion != "Debian 13" || a.ClientVersion != "1.90.4-t1234" || a.Hostname != "laptop" || a.PreferredDERP != 1 {
		t.Fatalf("node 1 = %+v", a)
	}
	if a.DERPLatency["1-v4"] < 12.29 || a.DERPLatency["1-v4"] > 12.31 {
		t.Errorf("latency ms = %v", a.DERPLatency)
	}
	if !slices.Equal(a.RequestTags, []string{"tag:dev"}) {
		t.Errorf("tags %v", a.RequestTags)
	}
	wantEP := []netip.AddrPort{netip.MustParseAddrPort("203.0.113.5:41641"), netip.MustParseAddrPort("[2001:db8::5]:41641")}
	if !slices.Equal(a.Endpoints, wantEP) {
		t.Errorf("endpoints %v", a.Endpoints)
	}
	if got[2].OS != "iOS" || got[2].Endpoints != nil {
		t.Errorf("node 2 = %+v", got[2])
	}
	if got[3].OS != "" {
		t.Errorf("broken host_info should be empty: %+v", got[3])
	}
	ids, err := d.Migrations(context.Background())
	if err != nil || len(ids) != 2 {
		t.Fatalf("migrations %v %v", ids, err)
	}
}

func TestWritesFail(t *testing.T) {
	d, err := Open(host.DBSQLite, makeDB(t, schema, false), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	db := d.q.(sqlQ).db
	for _, stmt := range []string{
		"UPDATE nodes SET hostname = 'x' WHERE id = 1",
		"INSERT INTO migrations VALUES ('evil')",
		"DELETE FROM nodes",
		"CREATE TABLE t (x int)",
		"DROP TABLE nodes",
	} {
		if _, err := db.Exec(stmt); err == nil {
			t.Errorf("%q succeeded on a read-only handle", stmt)
		}
	}
	got, _ := d.NodeExtras(context.Background(), []uint64{1})
	if got[1].Hostname != "laptop" {
		t.Error("data changed")
	}
}

func TestSchemaGuard(t *testing.T) {
	tests := []struct {
		name string
		ddl  string
	}{
		{"no migrations", "CREATE TABLE nodes (id integer, host_info text)"},
		{"no host_info", "CREATE TABLE migrations (id text); CREATE TABLE nodes (id integer, hostname text)"},
		{"no nodes", "CREATE TABLE migrations (id text)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Open(host.DBSQLite, makeDB(t, tt.ddl, false), Options{})
			if !errors.Is(err, ErrUnknownSchema) {
				t.Fatalf("err = %v", err)
			}
		})
	}
	if _, err := Open(host.DBSQLite, filepath.Join(t.TempDir(), "missing.sqlite"), Options{}); err == nil {
		t.Error("missing file opened")
	}
}

func TestWALUnwritableDir(t *testing.T) {
	p := makeDB(t, schema, true)
	outsider := &files.Ident{UID: 4242, GIDs: []int{4242}}
	if _, err := Open(host.DBSQLite, p, Options{Ident: outsider}); !errors.Is(err, ErrWALNeedsWrite) {
		t.Fatalf("err = %v, want ErrWALNeedsWrite", err)
	}
	d, err := Open(host.DBSQLite, p, Options{Ident: outsider, AllowImmutable: true})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if len(d.Notes) == 0 || !strings.Contains(d.Notes[0], "immutable") {
		t.Errorf("notes %v", d.Notes)
	}
	got, err := d.NodeExtras(context.Background(), []uint64{1})
	if err != nil || got[1].OS != "linux" {
		t.Fatalf("%+v %v", got, err)
	}
	// A writable directory opens normally even in WAL mode.
	d2, err := Open(host.DBSQLite, p, Options{})
	if err != nil {
		t.Fatal(err)
	}
	d2.Close()
}

func TestDSN(t *testing.T) {
	if got := SQLiteDSN("/var/lib/headscale/db.sqlite", false); got != "file:/var/lib/headscale/db.sqlite?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(2000)" {
		t.Errorf("dsn %s", got)
	}
	if got := SQLiteDSN("/a b/db.sqlite", true); !strings.HasSuffix(got, "&immutable=1") || !strings.Contains(got, "a%20b") {
		t.Errorf("dsn %s", got)
	}
	if _, err := Open(host.DBPostgres, "::not a dsn::", Options{}); err == nil {
		t.Error("bad postgres dsn accepted")
	}
}
