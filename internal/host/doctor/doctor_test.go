package doctor_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	iofs "io/fs"
	"math/big"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tonhe/headsup/internal/config"
	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/host/doctor"
	"github.com/tonhe/headsup/internal/host/fake"
)

var now = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

func clock() time.Time { return now }

// certPEM returns a self-signed certificate (and its key, to plant a
// realistic file) expiring at notAfter.
func certPEM(t *testing.T, cn string, notAfter time.Time) []byte {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: cn}, DNSNames: []string{cn},
		NotBefore: notAfter.Add(-90 * 24 * time.Hour), NotAfter: notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	kb, _ := x509.MarshalECPrivateKey(k)
	out := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb})
	return append(out, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
}

func withConfig(extra string) fake.Option {
	return fake.WithFile(fake.ConfigPath, append(append([]byte{}, fake.SeedConfig...), []byte("\n"+extra+"\n")...))
}

// seedWithTLS replaces the seed's empty tls.cert_path.
func seedWithTLS(path string) fake.Option {
	cfg := strings.Replace(string(fake.SeedConfig), `cert_path: ""`, `cert_path: "`+path+`"`, 1)
	return fake.WithFile(fake.ConfigPath, []byte(cfg))
}

func run(t *testing.T, h *fake.Host, mod func(*doctor.Probes)) []doctor.Finding {
	t.Helper()
	p := doctor.SimulatedProbes(h, clock)
	if mod != nil {
		mod(&p)
	}
	return doctor.Run(context.Background(), h, p, doctor.DefaultThresholds())
}

func find(t *testing.T, fs []doctor.Finding, id string) doctor.Finding {
	t.Helper()
	for _, f := range fs {
		if f.ID == id {
			return f
		}
	}
	t.Fatalf("no finding %q in %+v", id, fs)
	return doctor.Finding{}
}

// every problem must carry a fix (US16: "Each finding has a suggested fix").
func checkFixes(t *testing.T, fs []doctor.Finding) {
	t.Helper()
	for _, f := range fs {
		if f.Problem() && strings.TrimSpace(f.Fix) == "" {
			t.Errorf("finding %s (%s) has no fix", f.ID, f.Title)
		}
		if f.Check == "" || f.Title == "" {
			t.Errorf("finding %+v lacks a check or title", f)
		}
	}
}

func TestHealthyHostHasNoProblems(t *testing.T) {
	h := fake.New()
	// The simulated demo WAL is deliberately large; the demo shows that finding.
	if f := find(t, run(t, h, nil), "db.wal"); f.Severity != doctor.SevWarn {
		t.Errorf("demo WAL finding: %+v", f)
	}
	fs := run(t, h, func(p *doctor.Probes) {
		p.Stat = func(path string) (iofs.FileInfo, error) {
			if strings.HasSuffix(path, "-wal") {
				return sized(1 << 20), nil
			}
			return sized(10 << 20), nil
		}
	})
	checkFixes(t, fs)
	for _, f := range fs {
		if f.Problem() {
			t.Errorf("unexpected problem on a healthy host: %+v", f)
		}
	}
	checks := map[string]bool{}
	for _, f := range fs {
		checks[f.Check] = true
	}
	for _, c := range []string{doctor.CheckPermissions, doctor.CheckSocket, doctor.CheckServerURL, doctor.CheckTLS,
		doctor.CheckDeprecated, doctor.CheckDatabase, doctor.CheckDisk, doctor.CheckClock} {
		if !checks[c] {
			t.Errorf("check %q produced no result", c)
		}
	}
	if w, e := doctor.Counts(fs); w+e != 0 {
		t.Errorf("counts %d/%d", w, e)
	}
}

func TestExpiredCertificate(t *testing.T) {
	const crt = "/etc/headscale/tls.crt"
	h := fake.New(seedWithTLS(crt), fake.WithFile(crt, certPEM(t, "hs.demo.example.com", now.Add(-2*time.Hour))))
	fs := run(t, h, nil)
	checkFixes(t, fs)
	f := find(t, fs, "tls.expired")
	if f.Severity != doctor.SevError || !strings.Contains(f.Detail, crt) {
		t.Errorf("expired cert: %+v", f)
	}
	if strings.Contains(f.Detail, "PRIVATE") {
		t.Error("finding must not echo file contents")
	}
}

func TestCertificateThresholds(t *testing.T) {
	const crt = "/etc/headscale/tls.crt"
	for _, tt := range []struct {
		left time.Duration
		id   string
		sev  doctor.Severity
	}{
		{60 * 24 * time.Hour, "tls", doctor.SevOK},
		{20 * 24 * time.Hour, "tls.expiry", doctor.SevWarn},
		{3 * 24 * time.Hour, "tls.expiry", doctor.SevError},
	} {
		h := fake.New(seedWithTLS(crt), fake.WithFile(crt, certPEM(t, "hs", now.Add(tt.left))))
		f := find(t, run(t, h, nil), tt.id)
		if f.Severity != tt.sev {
			t.Errorf("%v left: severity %v, want %v", tt.left, f.Severity, tt.sev)
		}
	}
}

func TestCertificateUnreadableOrEmpty(t *testing.T) {
	const crt = "/etc/headscale/tls.crt"
	h := fake.New(seedWithTLS(crt))
	fs := run(t, h, nil)
	checkFixes(t, fs)
	if f := find(t, fs, "tls.read"); f.Severity != doctor.SevWarn {
		t.Errorf("missing cert: %+v", f)
	}
	h = fake.New(seedWithTLS(crt), fake.WithFile(crt, []byte("not a cert")))
	if f := find(t, run(t, h, nil), "tls.parse"); f.Severity != doctor.SevError || f.Fix == "" {
		t.Errorf("garbage cert: %+v", f)
	}
}

func TestACMECacheCertificate(t *testing.T) {
	cfg := strings.Replace(string(fake.SeedConfig), `hostname: ""`, `hostname: "hs.demo.example.com"`, 1)
	cache := map[string][]byte{
		"/var/lib/headscale/cache/hs.demo.example.com":     certPEM(t, "hs.demo.example.com", now.Add(5*24*time.Hour)),
		"/var/lib/headscale/cache/acme_account+key":        []byte("-----BEGIN EC PRIVATE KEY-----\nxx\n-----END EC PRIVATE KEY-----\n"),
		"/var/lib/headscale/cache/hs.demo.example.com+rsa": certPEM(t, "hs.demo.example.com", now.Add(4*24*time.Hour)),
	}
	h := fake.New(fake.WithFile(fake.ConfigPath, []byte(cfg)))
	fs := run(t, h, func(p *doctor.Probes) {
		p.ReadDir = func(dir string) ([]string, error) {
			if dir != "/var/lib/headscale/cache" {
				return nil, iofs.ErrNotExist
			}
			return []string{"acme_account+key", "hs.demo.example.com", "hs.demo.example.com+rsa"}, nil
		}
		p.ReadFile = func(path string) ([]byte, error) {
			if b, ok := cache[path]; ok {
				return b, nil
			}
			return nil, iofs.ErrNotExist
		}
	})
	checkFixes(t, fs)
	f := find(t, fs, "tls.expiry")
	if f.Severity != doctor.SevError || !strings.Contains(f.Title, "5 days") || !strings.Contains(f.Fix, "acme") {
		t.Errorf("ACME cert (latest of the two, 5 days): %+v", f)
	}

	// An empty cache is informational, not an error.
	h = fake.New(fake.WithFile(fake.ConfigPath, []byte(cfg)))
	if f := find(t, run(t, h, nil), "tls.acme.none"); f.Severity != doctor.SevInfo {
		t.Errorf("empty cache: %+v", f)
	}
}

func TestDeprecatedKeys(t *testing.T) {
	h := fake.New(withConfig("tls_letsencrypt_hostname: hs.example.com\ngrpc_listen_addr: 127.0.0.1:50443"))
	fs := run(t, h, nil)
	checkFixes(t, fs)
	f := find(t, fs, "deprecated.tls_letsencrypt_hostname")
	if f.Severity != doctor.SevWarn || !strings.Contains(f.Title, "tls.acme.hostname") || f.Action != "serverconfig" {
		t.Errorf("replaced key: %+v", f)
	}
	if !strings.Contains(f.Detail, "config line") {
		t.Errorf("detail should name the line: %q", f.Detail)
	}
	g := find(t, fs, "deprecated.grpc_listen_addr")
	if !strings.Contains(g.Title, "removed") || g.Fix == "" {
		t.Errorf("removed key: %+v", g)
	}
}

func TestConfigUnreadable(t *testing.T) {
	h := fake.New()
	h.FailPermission(host.NeedConfigR, "sudo usermod -aG headscale $USER")
	fs := run(t, h, nil)
	checkFixes(t, fs)
	if f := find(t, fs, "config.read"); f.Fix != "sudo usermod -aG headscale $USER" {
		t.Errorf("config unreadable: %+v", f)
	}
	if f := find(t, fs, "perm.read-the-config"); f.Severity != doctor.SevWarn {
		t.Errorf("permission finding: %+v", f)
	}
}

func TestNoSocketPermission(t *testing.T) {
	h := fake.New()
	h.FailPermission(host.NeedSocket, "sudo usermod -aG headscale alice")
	fs := run(t, h, nil)
	checkFixes(t, fs)
	f := find(t, fs, "socket.permission")
	if f.Severity != doctor.SevError || f.Fix != "sudo usermod -aG headscale alice" {
		t.Errorf("socket permission: %+v", f)
	}
}

func TestSocketUnreachable(t *testing.T) {
	h := fake.New()
	fs := run(t, h, func(p *doctor.Probes) {
		p.Dial = func(context.Context, string, string) error { return errors.New("connection refused") }
	})
	checkFixes(t, fs)
	f := find(t, fs, "socket.unreachable")
	if !strings.Contains(f.Fix, "systemctl status headscale") {
		t.Errorf("socket down: %+v", f)
	}
	fs = run(t, h, func(p *doctor.Probes) {
		p.Dial = func(context.Context, string, string) error { return iofs.ErrPermission }
	})
	find(t, fs, "socket.permission")
}

func TestWALGrowth(t *testing.T) {
	h := fake.New()
	sizes := map[string]int64{"/var/lib/headscale/db.sqlite": 10 << 20, "/var/lib/headscale/db.sqlite-wal": 8 << 20}
	stat := func(p *doctor.Probes) {
		p.Stat = func(path string) (iofs.FileInfo, error) {
			if n, ok := sizes[path]; ok {
				return sized(n), nil
			}
			return nil, iofs.ErrNotExist
		}
	}
	fs := run(t, h, stat)
	checkFixes(t, fs)
	f := find(t, fs, "db.wal")
	if f.Severity != doctor.SevWarn || !strings.Contains(f.Title, "80%") || !strings.Contains(f.Fix, "systemctl restart headscale") {
		t.Errorf("WAL over half the database: %+v", f)
	}
	// Over 64 MB warns even when small relative to a huge database.
	sizes["/var/lib/headscale/db.sqlite"] = 1 << 30
	sizes["/var/lib/headscale/db.sqlite-wal"] = 65 << 20
	find(t, run(t, h, stat), "db.wal")
	// Small WAL: fine.
	sizes["/var/lib/headscale/db.sqlite-wal"] = 1 << 20
	if f := find(t, run(t, h, stat), "db"); f.Severity != doctor.SevOK {
		t.Errorf("small WAL: %+v", f)
	}
	// Missing database is reported.
	delete(sizes, "/var/lib/headscale/db.sqlite")
	find(t, run(t, h, stat), "db.stat")
}

func TestPostgresSkipsSize(t *testing.T) {
	env := fake.DefaultEnv()
	env.DB = host.DBPostgres
	h := fake.New(fake.WithEnv(env))
	if f := find(t, run(t, h, nil), "db"); f.Severity != doctor.SevSkipped {
		t.Errorf("postgres: %+v", f)
	}
}

func TestUnreachableServerURL(t *testing.T) {
	h := fake.New()
	fs := run(t, h, func(p *doctor.Probes) {
		p.Fetch = func(_ context.Context, url string) (int, time.Time, error) {
			if strings.HasSuffix(url, "/health") {
				return 0, time.Time{}, errors.New("dial tcp: lookup hs.demo.example.com: no such host")
			}
			return 200, now, nil
		}
	})
	checkFixes(t, fs)
	f := find(t, fs, "server_url.unreachable")
	if f.Severity != doctor.SevError || !strings.Contains(f.Fix, "https://hs.demo.example.com/health") {
		t.Errorf("unreachable server_url: %+v", f)
	}
	fs = run(t, h, func(p *doctor.Probes) {
		p.Fetch = func(context.Context, string) (int, time.Time, error) { return 502, now, nil }
	})
	if f := find(t, fs, "server_url.status"); f.Severity != doctor.SevWarn || f.Fix == "" {
		t.Errorf("502: %+v", f)
	}
}

func TestDiskSpace(t *testing.T) {
	h := fake.New()
	for _, tt := range []struct {
		free, total uint64
		sev         doctor.Severity
	}{
		{50 << 30, 100 << 30, doctor.SevOK},
		{5 << 30, 100 << 30, doctor.SevWarn},
		{512 << 20, 4 << 30, doctor.SevError},
	} {
		fs := run(t, h, func(p *doctor.Probes) {
			p.DiskFree = func(string) (uint64, uint64, error) { return tt.free, tt.total, nil }
		})
		checkFixes(t, fs)
		var got doctor.Finding
		for _, f := range fs {
			if f.Check == doctor.CheckDisk {
				got = f
			}
		}
		if got.Severity != tt.sev {
			t.Errorf("free %d/%d: %+v", tt.free, tt.total, got)
		}
	}
	fs := run(t, h, func(p *doctor.Probes) {
		p.DiskFree = func(string) (uint64, uint64, error) { return 0, 0, doctor.ErrUnsupported }
	})
	if f := find(t, fs, "disk"); f.Severity != doctor.SevSkipped {
		t.Errorf("unsupported: %+v", f)
	}
}

func TestClockSkew(t *testing.T) {
	h := fake.New()
	for _, tt := range []struct {
		off time.Duration
		sev doctor.Severity
	}{
		{0, doctor.SevOK},
		{5 * time.Second, doctor.SevWarn},
		{-2 * time.Minute, doctor.SevError},
	} {
		fs := run(t, h, func(p *doctor.Probes) {
			p.Fetch = func(_ context.Context, url string) (int, time.Time, error) {
				// The remote clock reads now-off, so the local clock is off ahead.
				return 200, now.Add(-tt.off), nil
			}
		})
		checkFixes(t, fs)
		var got doctor.Finding
		for _, f := range fs {
			if f.Check == doctor.CheckClock {
				got = f
			}
		}
		if got.Severity != tt.sev {
			t.Errorf("offset %v: %+v", tt.off, got)
		}
		if tt.off < 0 && !strings.Contains(got.Title, "behind") {
			t.Errorf("direction: %q", got.Title)
		}
	}
	fs := run(t, h, func(p *doctor.Probes) { p.ClockURL = "" })
	if f := find(t, fs, "clock"); f.Severity != doctor.SevSkipped {
		t.Errorf("off: %+v", f)
	}
	fs = run(t, h, func(p *doctor.Probes) {
		p.Fetch = func(_ context.Context, url string) (int, time.Time, error) {
			if url == doctor.DefaultClockURL {
				return 0, time.Time{}, errors.New("offline")
			}
			return 200, now, nil
		}
	})
	if f := find(t, fs, "clock.unknown"); f.Severity != doctor.SevInfo {
		t.Errorf("unknown: %+v", f)
	}
}

func TestPermissionFindings(t *testing.T) {
	h := fake.New()
	h.FailPermission(host.NeedService, "")
	h.FailPermission(host.NeedLogs, "sudo usermod -aG systemd-journal $USER")
	fs := run(t, h, nil)
	checkFixes(t, fs)
	if f := find(t, fs, "perm.control-the-service"); f.Fix != "sudo systemctl restart headscale" {
		t.Errorf("service: %+v", f)
	}
	if f := find(t, fs, "perm.read-the-logs"); !strings.Contains(f.Fix, "systemd-journal") {
		t.Errorf("logs: %+v", f)
	}
}

func TestNoConfigOrSocket(t *testing.T) {
	env := fake.DefaultEnv()
	env.ConfigPath, env.SocketPath, env.ServerURL, env.DBPath = "", "", "", ""
	h := fake.New(fake.WithEnv(env))
	fs := run(t, h, nil)
	checkFixes(t, fs)
	for _, id := range []string{"socket", "server_url", "deprecated", "db", "disk"} {
		if f := find(t, fs, id); f.Severity != doctor.SevSkipped {
			t.Errorf("%s: %+v", id, f)
		}
	}
}

func TestContainerRestartCommand(t *testing.T) {
	env := fake.DefaultEnv()
	env.Service, env.ContainerRuntime, env.Container = host.ServiceContainer, "podman", "hs"
	h := fake.New(fake.WithEnv(env))
	fs := run(t, h, func(p *doctor.Probes) {
		p.Dial = func(context.Context, string, string) error { return errors.New("refused") }
		p.Stat = func(path string) (iofs.FileInfo, error) {
			if strings.HasSuffix(path, "-wal") {
				return sized(100 << 20), nil
			}
			return sized(10 << 20), nil
		}
	})
	if f := find(t, fs, "db.wal"); !strings.Contains(f.Fix, "podman restart hs") {
		t.Errorf("container restart: %q", f.Fix)
	}
	if f := find(t, fs, "socket.unreachable"); !strings.Contains(f.Fix, "podman ps") {
		t.Errorf("container status: %q", f.Fix)
	}
}

func TestFromConfig(t *testing.T) {
	d := config.Default().Host.Doctor
	got := doctor.FromConfig(d)
	if got != doctor.DefaultThresholds() {
		t.Errorf("defaults differ:\n%+v\n%+v", got, doctor.DefaultThresholds())
	}
	d.CertWarn, d.WALWarnMB, d.DiskWarnPct, d.SkewWarn = "72h", 8, 25, "5s"
	got = doctor.FromConfig(d)
	if got.CertWarn != 72*time.Hour || got.WALWarnBytes != 8<<20 || got.DiskWarnPct != 25 || got.SkewWarn != 5*time.Second {
		t.Errorf("overrides: %+v", got)
	}
	d.SkewWarn = "1m"
	if got := doctor.FromConfig(d); got.SkewError <= got.SkewWarn {
		t.Errorf("skew error must stay above warn: %+v", got)
	}
}

func TestOSProbesDefaults(t *testing.T) {
	p := doctor.OSProbes()
	if p.Stat == nil || p.ReadFile == nil || p.ReadDir == nil || p.DiskFree == nil || p.Dial == nil || p.Fetch == nil || p.Now == nil {
		t.Fatal("OSProbes left a probe nil")
	}
	if p.ClockURL != doctor.DefaultClockURL {
		t.Errorf("clock url %q", p.ClockURL)
	}
	dir := t.TempDir()
	if names, err := p.ReadDir(dir); err != nil || len(names) != 0 {
		t.Errorf("ReadDir(empty) = %v, %v", names, err)
	}
	if _, _, err := p.DiskFree(dir); err != nil && !errors.Is(err, doctor.ErrUnsupported) {
		t.Errorf("DiskFree: %v", err)
	}
	if err := p.Dial(context.Background(), "tcp", "127.0.0.1:1"); err == nil {
		t.Error("dial to a closed port should fail")
	}
	if _, _, err := p.Fetch(context.Background(), "http://127.0.0.1:1/"); err == nil {
		t.Error("fetch to a closed port should fail")
	}
	if _, _, err := p.Fetch(context.Background(), "::bad"); err == nil {
		t.Error("bad URL should fail")
	}
}

func TestSeverityString(t *testing.T) {
	if doctor.SevError.String() != "error" || doctor.SevSkipped.String() != "skipped" {
		t.Error("severity names")
	}
}

type sized int64

func (s sized) Name() string        { return "f" }
func (s sized) Size() int64         { return int64(s) }
func (s sized) Mode() iofs.FileMode { return 0o640 }
func (s sized) ModTime() time.Time  { return time.Time{} }
func (s sized) IsDir() bool         { return false }
func (s sized) Sys() any            { return nil }

func TestTLSPathDefaults(t *testing.T) {
	// 0.29-style ACME with no cache dir: the default cache is searched.
	h := fake.New(fake.WithFile(fake.ConfigPath, []byte("server_url: https://hs.example.com\ntls_letsencrypt_hostname: hs.example.com\n")))
	var dirs []string
	run(t, h, func(p *doctor.Probes) {
		p.ReadDir = func(dir string) ([]string, error) { dirs = append(dirs, dir); return nil, iofs.ErrNotExist }
	})
	if len(dirs) != 1 || filepath.ToSlash(dirs[0]) != "/var/lib/headscale/cache" {
		t.Errorf("ACME cache searched in %v", dirs)
	}
	// A relative cert path resolves against the config directory.
	h = fake.New(fake.WithFile(fake.ConfigPath, []byte("tls:\n  cert_path: tls.crt\n")),
		fake.WithFile("/etc/headscale/tls.crt", certPEM(t, "hs", now.Add(100*24*time.Hour))))
	var read []string
	got := run(t, h, func(p *doctor.Probes) {
		base := doctor.SimulatedProbes(h, clock).ReadFile
		p.ReadFile = func(path string) ([]byte, error) {
			read = append(read, filepath.ToSlash(path))
			return base(filepath.ToSlash(path))
		}
	})
	if f := find(t, got, "tls"); f.Severity != doctor.SevOK || len(read) != 1 || read[0] != "/etc/headscale/tls.crt" {
		t.Errorf("relative cert: %+v read %v", f, read)
	}
}
