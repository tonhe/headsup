// Package doctor checks a headscale host for common problems: missing
// permissions, an unreachable socket or server_url, expiring TLS
// certificates, deprecated config keys, database and WAL growth, low disk
// space and clock skew. Every problem comes with a suggested fix, shown to
// the admin and never run.
package doctor

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tonhe/headsup/internal/config"
	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/host/hsconfig"
)

// Severity of a finding.
type Severity int

const (
	SevOK Severity = iota
	SevSkipped
	SevInfo
	SevWarn
	SevError
)

func (s Severity) String() string {
	return [...]string{"ok", "skipped", "info", "warn", "error"}[s]
}

// Check names, in the order the doctor runs them.
const (
	CheckPermissions = "Permissions"
	CheckSocket      = "Socket"
	CheckServerURL   = "server_url"
	CheckTLS         = "TLS certificate"
	CheckDeprecated  = "Deprecated keys"
	CheckDatabase    = "Database"
	CheckDisk        = "Disk space"
	CheckClock       = "Clock"
)

// Finding is one result. Problems (warn, error) always carry a Fix.
type Finding struct {
	ID       string // stable, e.g. "tls.expiry" or "deprecated.tls_cert_path"
	Check    string
	Severity Severity
	Title    string
	Detail   string
	// Fix is a command or instruction for the admin; it is never executed.
	Fix string
	// Action, when set, is an in-app module that fixes the problem.
	Action string
}

// Problem reports whether the finding needs attention.
func (f Finding) Problem() bool { return f.Severity >= SevWarn }

// Thresholds are the warn and error limits (spec US16 defaults).
type Thresholds struct {
	CertWarn, CertError time.Duration
	WALWarnBytes        int64
	WALWarnRatio        float64 // of the database size
	DiskWarnPct         float64 // free percentage
	DiskErrorBytes      uint64
	SkewWarn, SkewError time.Duration
}

// DefaultThresholds: certificate under 21 days warns and under 7 errors; WAL
// over 64 MB or 50% of the database warns; free disk under 10% warns and
// under 1 GB errors; clock skew over 2s warns and over 30s errors.
func DefaultThresholds() Thresholds {
	return Thresholds{
		CertWarn: 21 * 24 * time.Hour, CertError: 7 * 24 * time.Hour,
		WALWarnBytes: 64 << 20, WALWarnRatio: 0.5,
		DiskWarnPct: 10, DiskErrorBytes: 1 << 30,
		SkewWarn: 2 * time.Second, SkewError: 30 * time.Second,
	}
}

// FromConfig applies [host.doctor] over the defaults.
func FromConfig(d config.Doctor) Thresholds {
	t := DefaultThresholds()
	t.CertWarn = d.CertWarnDuration()
	t.CertError = d.CertErrorDuration()
	if d.WALWarnMB > 0 {
		t.WALWarnBytes = int64(d.WALWarnMB) << 20
	}
	if d.DiskWarnPct > 0 {
		t.DiskWarnPct = float64(d.DiskWarnPct)
	}
	t.SkewWarn = d.SkewWarnDuration()
	if t.SkewError <= t.SkewWarn {
		t.SkewError = 15 * t.SkewWarn
	}
	return t
}

// Run executes every check against h. Probes left nil use the real OS.
func Run(ctx context.Context, h host.Host, p Probes, t Thresholds) []Finding {
	p = p.withDefaults()
	r := &run{ctx: ctx, h: h, p: p, t: t, env: h.Env()}
	r.loadConfig()
	r.permissions()
	r.socket()
	r.serverURL()
	r.tls()
	r.deprecated()
	r.database()
	r.disk()
	r.clock()
	return r.out
}

// Counts returns the number of warnings and errors.
func Counts(fs []Finding) (warn, errs int) {
	for _, f := range fs {
		switch f.Severity {
		case SevWarn:
			warn++
		case SevError:
			errs++
		}
	}
	return warn, errs
}

type run struct {
	ctx    context.Context
	h      host.Host
	p      Probes
	t      Thresholds
	env    host.Env
	doc    *hsconfig.Doc
	docErr error
	out    []Finding
}

func (r *run) add(f Finding) { r.out = append(r.out, f) }

func (r *run) ok(id, check, title string) {
	r.add(Finding{ID: id, Check: check, Severity: SevOK, Title: title})
}

func (r *run) skip(id, check, title string) {
	r.add(Finding{ID: id, Check: check, Severity: SevSkipped, Title: title})
}

func (r *run) loadConfig() {
	f, err := r.h.Open(host.FileConfig, "")
	if err != nil {
		r.docErr = err
		return
	}
	r.doc, r.docErr = hsconfig.Parse(f.Raw)
}

func (r *run) get(keys ...string) string {
	if r.doc == nil {
		return ""
	}
	for _, k := range keys {
		if v, ok := r.doc.Get(k); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// restartCmd is the command that restarts headscale on this host.
func restartCmd(env host.Env) string {
	switch env.Service {
	case host.ServiceOpenRC:
		return "sudo rc-service headscale restart"
	case host.ServiceContainer:
		rt := env.ContainerRuntime
		if rt == "" {
			rt = "docker"
		}
		name := env.Container
		if name == "" {
			name = "headscale"
		}
		return rt + " restart " + name
	default:
		return "sudo systemctl restart headscale"
	}
}

func statusCmd(env host.Env) string {
	switch env.Service {
	case host.ServiceOpenRC:
		return "rc-service headscale status"
	case host.ServiceContainer:
		rt := env.ContainerRuntime
		if rt == "" {
			rt = "docker"
		}
		return rt + " ps -a --filter name=" + env.Container
	default:
		return "systemctl status headscale"
	}
}

var needNames = []struct {
	n    host.Need
	name string
}{
	{host.NeedConfigR, "read the config"},
	{host.NeedConfigW, "write the config"},
	{host.NeedPolicyW, "write the policy file"},
	{host.NeedRecordsW, "write the extra-records file"},
	{host.NeedService, "control the service"},
	{host.NeedLogs, "read the logs"},
	{host.NeedDebug, "reach the debug endpoint"},
	{host.NeedDBRead, "read the database"},
}

// permissions reports every probed right this process lacks. The socket
// has its own check.
func (r *run) permissions() {
	missing := 0
	for _, nn := range needNames {
		p, probed := r.env.Perms[nn.n]
		if !probed || p.OK {
			continue
		}
		missing++
		fix := p.FixCommand
		if fix == "" {
			fix = "sudo usermod -aG headscale $USER && newgrp headscale"
		}
		detail := p.Reason
		if detail == "" {
			detail = "not available to this user"
		}
		r.add(Finding{ID: "perm." + strings.ReplaceAll(nn.name, " ", "-"), Check: CheckPermissions, Severity: SevWarn,
			Title: "Cannot " + nn.name, Detail: detail, Fix: fix})
	}
	if missing == 0 {
		r.ok("perm", CheckPermissions, "This user has every host permission it needs")
	}
}

func (r *run) socket() {
	path := r.env.SocketPath
	if path == "" {
		path = r.get("unix_socket")
	}
	if path == "" {
		r.skip("socket", CheckSocket, "No unix socket configured")
		return
	}
	if p, probed := r.env.Perms[host.NeedSocket]; probed && !p.OK {
		fix := p.FixCommand
		if fix == "" {
			fix = "sudo usermod -aG headscale $USER && newgrp headscale"
		}
		r.add(Finding{ID: "socket.permission", Check: CheckSocket, Severity: SevError,
			Title:  "No permission on the headscale socket",
			Detail: fmt.Sprintf("%s is not accessible to this user, so the local API cannot be used.", path),
			Fix:    fix})
		return
	}
	ctx, cancel := context.WithTimeout(r.ctx, 3*time.Second)
	defer cancel()
	if err := r.p.Dial(ctx, "unix", path); err != nil {
		if errors.Is(err, fs.ErrPermission) {
			r.add(Finding{ID: "socket.permission", Check: CheckSocket, Severity: SevError,
				Title: "No permission on the headscale socket", Detail: err.Error(),
				Fix: "sudo usermod -aG headscale $USER && newgrp headscale"})
			return
		}
		r.add(Finding{ID: "socket.unreachable", Check: CheckSocket, Severity: SevError,
			Title: "The headscale socket does not answer", Detail: fmt.Sprintf("%s: %v", path, err),
			Fix: statusCmd(r.env)})
		return
	}
	r.ok("socket", CheckSocket, "Socket "+path+" answers")
}

func (r *run) serverURL() {
	su := r.env.ServerURL
	if su == "" {
		su = r.get("server_url")
	}
	if su == "" {
		r.skip("server_url", CheckServerURL, "server_url is not set")
		return
	}
	u, err := url.Parse(su)
	if err != nil || u.Host == "" {
		r.add(Finding{ID: "server_url.invalid", Check: CheckServerURL, Severity: SevError,
			Title: "server_url is not a valid URL", Detail: su,
			Fix: "Set server_url to the public https URL clients use", Action: "serverconfig"})
		return
	}
	health := strings.TrimRight(su, "/") + "/health"
	ctx, cancel := context.WithTimeout(r.ctx, 6*time.Second)
	defer cancel()
	status, _, err := r.p.Fetch(ctx, health)
	switch {
	case err != nil:
		r.add(Finding{ID: "server_url.unreachable", Check: CheckServerURL, Severity: SevError,
			Title:  "server_url is not reachable from this host",
			Detail: fmt.Sprintf("GET %s: %v. Clients use this URL, so they may not reach the server either.", health, err),
			Fix:    "curl -fsSv " + health + "   # then check DNS, the firewall and any reverse proxy"})
	case status >= 400:
		r.add(Finding{ID: "server_url.status", Check: CheckServerURL, Severity: SevWarn,
			Title:  fmt.Sprintf("server_url answers with HTTP %d", status),
			Detail: "GET " + health + " should return 200 when headscale is healthy.",
			Fix:    "curl -fsSv " + health + "   # check the reverse proxy forwards to headscale's listen_addr"})
	default:
		r.ok("server_url", CheckServerURL, su+" is reachable")
	}
}

// --- TLS ----------------------------------------------------------------------

func (r *run) tls() {
	certPath := r.get("tls.cert_path", "tls_cert_path")
	acmeHost := r.get("tls.acme.hostname", "tls_letsencrypt_hostname")
	switch {
	case certPath != "":
		r.certFile(r.abs(certPath))
	case acmeHost != "":
		r.acme(acmeHost)
	case r.doc == nil:
		r.skip("tls", CheckTLS, "Config not readable; TLS not checked")
	default:
		r.skip("tls", CheckTLS, "headscale does not terminate TLS (no cert_path or ACME hostname)")
	}
}

// abs resolves a config-relative path against the config's directory.
func (r *run) abs(p string) string {
	if p == "" || filepath.IsAbs(p) || strings.HasPrefix(p, "/") || r.env.ConfigPath == "" {
		return p
	}
	return pjoin(pdir(r.env.ConfigPath), p)
}

// pdir and pjoin keep slash-separated host paths (as headscale's config
// writes them) slash-separated on every platform.
func pdir(p string) string {
	if strings.HasPrefix(p, "/") {
		return path.Dir(p)
	}
	return filepath.Dir(p)
}

func pjoin(dir, name string) string {
	if strings.HasPrefix(dir, "/") {
		return path.Join(dir, name)
	}
	return filepath.Join(dir, name)
}

func readErrFinding(id, check, what, path string, err error) Finding {
	var pe *host.ErrPermission
	fix := "sudo ls -l " + path
	if errors.As(err, &pe) && pe.FixCommand != "" {
		fix = pe.FixCommand
	} else if errors.Is(err, fs.ErrPermission) {
		fix = "sudo usermod -aG headscale $USER && newgrp headscale"
	} else if errors.Is(err, fs.ErrNotExist) {
		fix = "Check the path in the config, or restore the file from a backup"
	}
	return Finding{ID: id, Check: check, Severity: SevWarn, Title: "Cannot read " + what, Detail: fmt.Sprintf("%s: %v", path, err), Fix: fix}
}

func (r *run) certFile(path string) {
	b, err := r.p.ReadFile(path)
	if err != nil {
		r.add(readErrFinding("tls.read", CheckTLS, "the TLS certificate", path, err))
		return
	}
	certs := parseCerts(b)
	if len(certs) == 0 {
		r.add(Finding{ID: "tls.parse", Check: CheckTLS, Severity: SevError, Title: "The TLS certificate file holds no certificate",
			Detail: path, Fix: "openssl x509 -in " + path + " -noout -text"})
		return
	}
	r.expiry(certs[0], path, false)
}

func (r *run) acme(hostname string) {
	dir := r.abs(r.get("tls.acme.cache_dir", "tls_letsencrypt_cache_dir"))
	if dir == "" {
		dir = "/var/lib/headscale/cache"
	}
	names, err := r.p.ReadDir(dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		r.add(readErrFinding("tls.read", CheckTLS, "the ACME cache", dir, err))
		return
	}
	var best *x509.Certificate
	var bestPath string
	sort.Strings(names)
	for _, n := range names {
		// autocert stores "<domain>" (ECDSA) and "<domain>+rsa".
		if n != hostname && !strings.HasPrefix(n, hostname+"+") {
			continue
		}
		p := pjoin(dir, n)
		b, err := r.p.ReadFile(p)
		if err != nil {
			r.add(readErrFinding("tls.read", CheckTLS, "the ACME cache", p, err))
			return
		}
		for _, c := range parseCerts(b) {
			if c.IsCA {
				continue
			}
			if best == nil || c.NotAfter.After(best.NotAfter) {
				best, bestPath = c, p
			}
		}
	}
	if best == nil {
		r.add(Finding{ID: "tls.acme.none", Check: CheckTLS, Severity: SevInfo,
			Title:  "No cached ACME certificate for " + hostname + " yet",
			Detail: "headscale requests one on first use; nothing was found in " + dir + ".",
			Fix:    "journalctl -u headscale | grep -i acme"})
		return
	}
	r.expiry(best, bestPath, true)
}

func (r *run) expiry(c *x509.Certificate, path string, acme bool) {
	left := c.NotAfter.Sub(r.p.Now())
	fix := "Renew the certificate at " + path + ", then: " + restartCmd(r.env)
	if acme {
		fix = "headscale renews ACME certificates itself; check why it has not: journalctl -u headscale | grep -i acme"
	}
	subject := c.Subject.CommonName
	if subject == "" && len(c.DNSNames) > 0 {
		subject = c.DNSNames[0]
	}
	detail := fmt.Sprintf("%s (%s) expires %s.", path, subject, c.NotAfter.UTC().Format("2006-01-02 15:04 MST"))
	switch {
	case left <= 0:
		r.add(Finding{ID: "tls.expired", Check: CheckTLS, Severity: SevError, Title: "The TLS certificate has expired",
			Detail: detail + " Clients cannot connect.", Fix: fix})
	case left < r.t.CertError:
		r.add(Finding{ID: "tls.expiry", Check: CheckTLS, Severity: SevError, Title: "The TLS certificate expires in " + days(left),
			Detail: detail, Fix: fix})
	case left < r.t.CertWarn:
		r.add(Finding{ID: "tls.expiry", Check: CheckTLS, Severity: SevWarn, Title: "The TLS certificate expires in " + days(left),
			Detail: detail, Fix: fix})
	default:
		r.ok("tls", CheckTLS, "TLS certificate valid for "+days(left))
	}
}

func days(d time.Duration) string {
	n := int(d.Hours() / 24)
	if n == 1 {
		return "1 day"
	}
	if n < 1 {
		return fmt.Sprintf("%d hours", int(d.Hours()))
	}
	return fmt.Sprintf("%d days", n)
}

// parseCerts returns the certificates in PEM data. Private-key blocks are
// skipped and never retained.
func parseCerts(b []byte) []*x509.Certificate {
	var out []*x509.Certificate
	for {
		var blk *pem.Block
		blk, b = pem.Decode(b)
		if blk == nil {
			return out
		}
		if blk.Type != "CERTIFICATE" {
			continue
		}
		if c, err := x509.ParseCertificate(blk.Bytes); err == nil {
			out = append(out, c)
		}
	}
}

// --- config -------------------------------------------------------------------

func (r *run) deprecated() {
	if r.doc == nil {
		var pe *host.ErrPermission
		f := Finding{ID: "config.read", Check: CheckDeprecated, Severity: SevWarn, Title: "Cannot read the headscale config",
			Detail: fmt.Sprint(r.docErr), Fix: "sudo usermod -aG headscale $USER && newgrp headscale"}
		if errors.As(r.docErr, &pe) && pe.FixCommand != "" {
			f.Fix = pe.FixCommand
		}
		if errors.Is(r.docErr, host.ErrNoHost) {
			r.skip("deprecated", CheckDeprecated, "No headscale config found")
			return
		}
		r.add(f)
		return
	}
	deps := hsconfig.Deprecations(r.env.Version)
	keys := make([]string, 0, len(deps))
	for k := range deps {
		if r.doc.Has(k) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		by := deps[k]
		x, _ := hsconfig.Lookup(k)
		f := Finding{ID: "deprecated." + k, Check: CheckDeprecated, Severity: SevWarn, Action: "serverconfig"}
		if by != "" {
			f.Title = fmt.Sprintf("%s is deprecated; use %s", k, by)
			f.Detail = fmt.Sprintf("Since %s the key is %s. Old keys may stop working in a later release.", x.Deprecated, by)
			f.Fix = "Open Server Settings (g s) and press m to migrate deprecated keys"
		} else {
			f.Title = k + " was removed"
			f.Detail = fmt.Sprintf("headscale %s no longer reads %s; it is ignored.", x.Deprecated, k)
			f.Fix = "Open Server Settings (g s) and remove " + k
		}
		if line := r.doc.Line(k); line > 0 {
			f.Detail += fmt.Sprintf(" (config line %d)", line)
		}
		r.add(f)
	}
	if len(keys) == 0 {
		r.ok("deprecated", CheckDeprecated, "No deprecated keys")
	}
}

// --- database and disk --------------------------------------------------------

func (r *run) dbPath() string {
	if r.env.DBPath != "" {
		return r.env.DBPath
	}
	return r.abs(r.get("database.sqlite.path"))
}

func (r *run) database() {
	if r.env.DB == host.DBPostgres {
		r.skip("db", CheckDatabase, "Postgres database; size is not checked")
		return
	}
	path := r.dbPath()
	if path == "" {
		r.skip("db", CheckDatabase, "No SQLite database found")
		return
	}
	fi, err := r.p.Stat(path)
	if err != nil {
		r.add(readErrFinding("db.stat", CheckDatabase, "the database", path, err))
		return
	}
	size := fi.Size()
	var wal int64
	if wi, err := r.p.Stat(path + "-wal"); err == nil {
		wal = wi.Size()
	}
	walTooBig := wal > r.t.WALWarnBytes || (size > 0 && float64(wal) > r.t.WALWarnRatio*float64(size))
	if walTooBig && wal > 0 {
		pct := 0
		if size > 0 {
			pct = int(float64(wal) / float64(size) * 100)
		}
		r.add(Finding{ID: "db.wal", Check: CheckDatabase, Severity: SevWarn,
			Title:  fmt.Sprintf("The WAL is %s (%d%% of the %s database)", bytesH(uint64(wal)), pct, bytesH(uint64(size))),
			Detail: "A growing write-ahead log usually means checkpoints are not running. Check database.sqlite.wal_autocheckpoint.",
			Fix:    "A clean restart checkpoints the WAL: " + restartCmd(r.env)})
		return
	}
	r.add(Finding{ID: "db", Check: CheckDatabase, Severity: SevOK,
		Title: fmt.Sprintf("Database %s, WAL %s", bytesH(uint64(size)), bytesH(uint64(wal)))})
}

func (r *run) disk() {
	dir := ""
	if p := r.dbPath(); p != "" {
		dir = pdir(p)
	} else if r.env.ConfigPath != "" {
		dir = pdir(r.env.ConfigPath)
	}
	if dir == "" {
		r.skip("disk", CheckDisk, "No data directory to check")
		return
	}
	free, total, err := r.p.DiskFree(dir)
	if err != nil || total == 0 {
		r.skip("disk", CheckDisk, "Free space unknown for "+dir)
		return
	}
	pct := float64(free) / float64(total) * 100
	title := fmt.Sprintf("%s free of %s (%.0f%%) on %s", bytesH(free), bytesH(total), pct, dir)
	fix := "df -h " + dir + "   # then remove old backups and logs, or grow the volume"
	switch {
	case free < r.t.DiskErrorBytes:
		r.add(Finding{ID: "disk.low", Check: CheckDisk, Severity: SevError, Title: "Disk almost full: " + title,
			Detail: "SQLite needs free space to write; headscale may fail.", Fix: fix})
	case pct < r.t.DiskWarnPct:
		r.add(Finding{ID: "disk.low", Check: CheckDisk, Severity: SevWarn, Title: "Disk space low: " + title,
			Detail: "Free space is below the warning threshold.", Fix: fix})
	default:
		r.ok("disk", CheckDisk, title)
	}
}

func bytesH(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// --- clock --------------------------------------------------------------------

func (r *run) clock() {
	if r.p.ClockURL == "" {
		r.skip("clock", CheckClock, "Clock check turned off")
		return
	}
	ctx, cancel := context.WithTimeout(r.ctx, 6*time.Second)
	defer cancel()
	t0 := r.p.Now()
	_, date, err := r.p.Fetch(ctx, r.p.ClockURL)
	t1 := r.p.Now()
	if err != nil || date.IsZero() {
		r.add(Finding{ID: "clock.unknown", Check: CheckClock, Severity: SevInfo,
			Title: "Clock not checked: " + r.p.ClockURL + " did not answer with a date", Fix: "timedatectl status"})
		return
	}
	// The Date header has one-second resolution: the true time lies in
	// [date, date+1s), so compare with its middle.
	mid := t0.Add(t1.Sub(t0) / 2)
	skew := mid.Sub(date.Add(500 * time.Millisecond))
	abs := skew
	if abs < 0 {
		abs = -abs
	}
	dir := "ahead"
	if skew < 0 {
		dir = "behind"
	}
	title := fmt.Sprintf("Clock is %s %s", abs.Round(100*time.Millisecond), dir)
	fix := "sudo timedatectl set-ntp true   # then check: timedatectl status"
	detail := "Compared with the Date header of " + r.p.ClockURL + ". Skew breaks key expiry and TLS."
	switch {
	case abs > r.t.SkewError:
		r.add(Finding{ID: "clock.skew", Check: CheckClock, Severity: SevError, Title: title, Detail: detail, Fix: fix})
	case abs > r.t.SkewWarn:
		r.add(Finding{ID: "clock.skew", Check: CheckClock, Severity: SevWarn, Title: title, Detail: detail, Fix: fix})
	default:
		r.ok("clock", CheckClock, "Clock in sync (within "+r.t.SkewWarn.String()+")")
	}
}
