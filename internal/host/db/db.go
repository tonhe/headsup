// Package db reads extra node detail from headscale's database, strictly
// read-only: SQLite is opened with mode=ro and query_only, Postgres with
// default_transaction_read_only=on. A lint test forbids write SQL here.
package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/tonhe/fathom/internal/host"
	"github.com/tonhe/fathom/internal/host/files"
	_ "modernc.org/sqlite"
)

// ErrUnknownSchema means the database does not look like headscale's; the
// enrichment feature turns itself off.
var ErrUnknownSchema = errors.New("unrecognised headscale database schema")

// ErrWALNeedsWrite means the SQLite database is in WAL mode, its directory
// is not writable by this process, and immutable mode was not allowed.
var ErrWALNeedsWrite = errors.New("sqlite WAL database needs a writable directory for a consistent read-only open")

// Options tunes Open.
type Options struct {
	QueryTimeout   time.Duration // per query; default 3s
	AllowImmutable bool          // for WAL databases in an unwritable dir, open with immutable=1 (may read stale data)
	Ident          *files.Ident  // identity for the directory check; nil = this process
}

// DB is a read-only handle.
type DB struct {
	kind    host.DBKind
	q       querier
	timeout time.Duration
	Notes   []string
}

type scanner interface{ Scan(dest ...any) error }

type rowsIter interface {
	scanner
	Next() bool
	Err() error
}

// querier abstracts database/sql (SQLite) and a single pgx connection
// (Postgres; pgx's database/sql adapter pulls in a pool dependency).
type querier interface {
	row(ctx context.Context, q string, args ...any) scanner
	rows(ctx context.Context, q string, args ...any) (rowsIter, func(), error)
	close() error
}

type sqlQ struct{ db *sql.DB }

func (s sqlQ) row(ctx context.Context, q string, args ...any) scanner {
	return s.db.QueryRowContext(ctx, q, args...)
}

func (s sqlQ) rows(ctx context.Context, q string, args ...any) (rowsIter, func(), error) {
	r, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, nil, err
	}
	return r, func() { r.Close() }, nil
}

func (s sqlQ) close() error { return s.db.Close() }

type pgxQ struct {
	mu   sync.Mutex
	conn *pgx.Conn
}

type lockedRow struct {
	r  pgx.Row
	mu *sync.Mutex
}

func (l lockedRow) Scan(dest ...any) error { defer l.mu.Unlock(); return l.r.Scan(dest...) }

func (p *pgxQ) row(ctx context.Context, q string, args ...any) scanner {
	p.mu.Lock()
	return lockedRow{r: p.conn.QueryRow(ctx, q, args...), mu: &p.mu}
}

func (p *pgxQ) rows(ctx context.Context, q string, args ...any) (rowsIter, func(), error) {
	p.mu.Lock()
	r, err := p.conn.Query(ctx, q, args...)
	if err != nil {
		p.mu.Unlock()
		return nil, nil, err
	}
	return r, func() { r.Close(); p.mu.Unlock() }, nil
}

func (p *pgxQ) close() error { return p.conn.Close(context.Background()) }

// Open opens the database read-only and checks the schema.
func Open(kind host.DBKind, dsnOrPath string, o Options) (*DB, error) {
	if o.QueryTimeout <= 0 {
		o.QueryTimeout = 3 * time.Second
	}
	d := &DB{kind: kind, timeout: o.QueryTimeout}
	var err error
	switch kind {
	case host.DBSQLite:
		err = d.openSQLite(dsnOrPath, o)
	case host.DBPostgres:
		err = d.openPostgres(dsnOrPath)
	default:
		return nil, fmt.Errorf("db: unsupported kind %v", kind)
	}
	if err != nil {
		return nil, err
	}
	if err := d.guard(); err != nil {
		d.q.close()
		return nil, err
	}
	return d, nil
}

// SQLiteDSN builds the read-only modernc DSN for path.
func SQLiteDSN(path string, immutable bool) string {
	dsn := "file:" + (&url.URL{Path: path}).EscapedPath() + "?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(2000)"
	if immutable {
		dsn += "&immutable=1"
	}
	return dsn
}

func (d *DB) openSQLite(path string, o Options) error {
	fi, err := os.Stat(path)
	if err != nil {
		if os.IsPermission(err) {
			return &host.ErrPermission{Need: host.NeedDBRead, Op: "read " + path, FixCommand: "sudo usermod -aG headscale $USER && newgrp headscale"}
		}
		return err
	}
	if fi.IsDir() {
		return fmt.Errorf("%s is a directory", path)
	}
	fh, err := os.Open(path)
	if err != nil {
		if os.IsPermission(err) {
			return &host.ErrPermission{Need: host.NeedDBRead, Op: "read " + path, FixCommand: "sudo usermod -aG headscale $USER && newgrp headscale"}
		}
		return err
	}
	hdr := make([]byte, 100)
	n, _ := fh.Read(hdr)
	fh.Close()
	wal := n >= 20 && string(hdr[:15]) == "SQLite format 3" && (hdr[18] == 2 || hdr[19] == 2)

	immutable := false
	if wal && !dirWritable(filepath.Dir(path), o.Ident) {
		if !o.AllowImmutable {
			return ErrWALNeedsWrite
		}
		immutable = true
		d.Notes = append(d.Notes, "database is in WAL mode and its directory is not writable; opened with immutable=1, recent changes may be missing")
	}
	db, err := sql.Open("sqlite", SQLiteDSN(path, immutable))
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(2)
	db.SetConnMaxIdleTime(30 * time.Second)
	d.q = sqlQ{db}
	return nil
}

func dirWritable(dir string, id *files.Ident) bool {
	fi, err := os.Stat(dir)
	if err != nil {
		return false
	}
	ident := files.Ident{UID: os.Getuid()}
	if id != nil {
		ident = *id
	} else {
		g, _ := os.Getgroups()
		ident.GIDs = append([]int{os.Getgid()}, g...)
	}
	return files.AllowedInfo(ident, fi, files.AccessWrite|files.AccessExec)
}

func (d *DB) openPostgres(dsn string) error {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("postgres dsn: %w", err)
	}
	if cfg.RuntimeParams == nil {
		cfg.RuntimeParams = map[string]string{}
	}
	cfg.RuntimeParams["default_transaction_read_only"] = "on"
	cfg.RuntimeParams["application_name"] = "headscale-admin-readonly"
	cfg.ConnectTimeout = 5 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 2*d.timeout+cfg.ConnectTimeout)
	defer cancel()
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return err
	}
	var ro string
	if err := conn.QueryRow(ctx, "SHOW default_transaction_read_only").Scan(&ro); err != nil {
		conn.Close(context.Background())
		return err
	}
	if ro != "on" {
		conn.Close(context.Background())
		return errors.New("postgres session is not read-only")
	}
	d.q = &pgxQ{conn: conn}
	return nil
}

// Close releases the handle.
func (d *DB) Close() error { return d.q.close() }

// Kind is the database kind.
func (d *DB) Kind() host.DBKind { return d.kind }

func (d *DB) ctx(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, d.timeout)
}

// guard checks for gormigrate's migrations table and nodes.host_info.
func (d *DB) guard() error {
	ctx, cancel := d.ctx(context.Background())
	defer cancel()
	var tablesQ, colsQ string
	switch d.kind {
	case host.DBSQLite:
		tablesQ = "SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'migrations'"
		colsQ = "SELECT name FROM pragma_table_info('nodes')"
	default:
		tablesQ = "SELECT count(*) FROM information_schema.tables WHERE table_name = 'migrations' AND table_schema = current_schema()"
		colsQ = "SELECT column_name FROM information_schema.columns WHERE table_name = 'nodes' AND table_schema = current_schema()"
	}
	var n int
	if err := d.q.row(ctx, tablesQ).Scan(&n); err != nil {
		return fmt.Errorf("%w: %v", ErrUnknownSchema, err)
	}
	if n == 0 {
		return fmt.Errorf("%w: no migrations table", ErrUnknownSchema)
	}
	rows, done, err := d.q.rows(ctx, colsQ)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnknownSchema, err)
	}
	defer done()
	cols := map[string]bool{}
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return err
		}
		cols[c] = true
	}
	if !cols["id"] || !cols["host_info"] {
		return fmt.Errorf("%w: nodes table lacks host_info", ErrUnknownSchema)
	}
	if !cols["endpoints"] {
		d.Notes = append(d.Notes, "nodes.endpoints column missing; endpoints will be empty")
	}
	return nil
}

// Migrations lists the applied gormigrate IDs (for doctor/upgrade checks).
func (d *DB) Migrations(ctx context.Context) ([]string, error) {
	ctx, cancel := d.ctx(ctx)
	defer cancel()
	rows, done, err := d.q.rows(ctx, "SELECT id FROM migrations ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer done()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// NodeExtras reads host_info and endpoints for ids.
func (d *DB) NodeExtras(ctx context.Context, ids []uint64) (map[uint64]host.NodeExtras, error) {
	out := make(map[uint64]host.NodeExtras, len(ids))
	const batch = 500
	for start := 0; start < len(ids); start += batch {
		chunk := ids[start:min(start+batch, len(ids))]
		if err := d.extras(ctx, chunk, out); err != nil {
			return out, err
		}
	}
	return out, nil
}

func (d *DB) extras(parent context.Context, ids []uint64, out map[uint64]host.NodeExtras) error {
	ctx, cancel := d.ctx(parent)
	defer cancel()
	ph := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		if d.kind == host.DBPostgres {
			ph[i] = "$" + strconv.Itoa(i+1)
		} else {
			ph[i] = "?"
		}
		args[i] = int64(id)
	}
	q := "SELECT id, host_info, endpoints FROM nodes WHERE id IN (" + strings.Join(ph, ",") + ")"
	rows, done, err := d.q.rows(ctx, q, args...)
	if err != nil {
		return err
	}
	defer done()
	for rows.Next() {
		var id int64
		var hi, ep sql.NullString
		if err := rows.Scan(&id, &hi, &ep); err != nil {
			return err
		}
		x := ParseHostinfo([]byte(hi.String))
		x.Endpoints = ParseEndpoints([]byte(ep.String))
		out[uint64(id)] = x
	}
	return rows.Err()
}

type hostinfo struct {
	OS          string   `json:"OS"`
	OSVersion   string   `json:"OSVersion"`
	IPNVersion  string   `json:"IPNVersion"`
	Hostname    string   `json:"Hostname"`
	RequestTags []string `json:"RequestTags"`
	NetInfo     *struct {
		PreferredDERP int                `json:"PreferredDERP"`
		DERPLatency   map[string]float64 `json:"DERPLatency"`
	} `json:"NetInfo"`
}

// ParseHostinfo parses tailcfg.Hostinfo JSON. DERPLatency is converted from
// seconds to milliseconds. Malformed input yields an empty value.
func ParseHostinfo(b []byte) host.NodeExtras {
	var h hostinfo
	if len(b) == 0 || json.Unmarshal(b, &h) != nil {
		return host.NodeExtras{}
	}
	x := host.NodeExtras{OS: h.OS, OSVersion: h.OSVersion, ClientVersion: h.IPNVersion, Hostname: h.Hostname, RequestTags: h.RequestTags}
	if h.NetInfo != nil {
		x.PreferredDERP = h.NetInfo.PreferredDERP
		if len(h.NetInfo.DERPLatency) > 0 {
			x.DERPLatency = make(map[string]float64, len(h.NetInfo.DERPLatency))
			for k, v := range h.NetInfo.DERPLatency {
				x.DERPLatency[k] = v * 1000
			}
		}
	}
	return x
}

// ParseEndpoints parses the JSON array of "ip:port" strings.
func ParseEndpoints(b []byte) []netip.AddrPort {
	var ss []string
	if len(b) == 0 || json.Unmarshal(b, &ss) != nil {
		return nil
	}
	var out []netip.AddrPort
	for _, s := range ss {
		if ap, err := netip.ParseAddrPort(s); err == nil {
			out = append(out, ap)
		}
	}
	return out
}
