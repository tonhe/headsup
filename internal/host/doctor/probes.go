package doctor

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/tonhe/headsup/internal/host"
)

// Probes are the doctor's view of the machine. Every field is a function so
// tests (and the simulated demo host) can replace any one of them; nil
// fields fall back to the real operating system.
type Probes struct {
	// Stat reports a file's size and mode (database and WAL size).
	Stat func(path string) (fs.FileInfo, error)
	// ReadFile reads a small file (certificate files, the ACME cache).
	ReadFile func(path string) ([]byte, error)
	// ReadDir lists the file names in dir (the ACME cache).
	ReadDir func(dir string) ([]string, error)
	// DiskFree reports free and total bytes of the filesystem holding path.
	// It returns ErrUnsupported where the platform cannot tell.
	DiskFree func(path string) (free, total uint64, err error)
	// Dial connects to addr and closes the connection (the unix socket).
	Dial func(ctx context.Context, network, addr string) error
	// Fetch sends a GET to url and returns the status and the Date header.
	Fetch func(ctx context.Context, url string) (status int, date time.Time, err error)
	// ClockURL is the HTTPS source whose Date header the clock is compared
	// with. Empty skips the clock check.
	ClockURL string
	// Now is the local clock.
	Now func() time.Time
}

// ErrUnsupported means a probe cannot run on this platform.
var ErrUnsupported = errors.New("not supported on this platform")

// DefaultClockURL is compared with the local clock. It is the host the
// upgrade check already talks to, so the doctor adds no new destination.
const DefaultClockURL = "https://api.github.com"

// OSProbes returns probes backed by the real machine.
func OSProbes() Probes {
	return Probes{ClockURL: DefaultClockURL}.withDefaults()
}

func (p Probes) withDefaults() Probes {
	if p.Stat == nil {
		p.Stat = os.Stat
	}
	if p.ReadFile == nil {
		p.ReadFile = os.ReadFile
	}
	if p.ReadDir == nil {
		p.ReadDir = func(dir string) ([]string, error) {
			es, err := os.ReadDir(dir)
			names := make([]string, 0, len(es))
			for _, e := range es {
				if !e.IsDir() {
					names = append(names, e.Name())
				}
			}
			return names, err
		}
	}
	if p.DiskFree == nil {
		p.DiskFree = diskFree
	}
	if p.Dial == nil {
		p.Dial = func(ctx context.Context, network, addr string) error {
			var d net.Dialer
			c, err := d.DialContext(ctx, network, addr)
			if err != nil {
				return err
			}
			return c.Close()
		}
	}
	if p.Fetch == nil {
		cl := &http.Client{Timeout: 5 * time.Second}
		p.Fetch = func(ctx context.Context, url string) (int, time.Time, error) {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			if err != nil {
				return 0, time.Time{}, err
			}
			resp, err := cl.Do(req)
			if err != nil {
				return 0, time.Time{}, err
			}
			resp.Body.Close()
			d, _ := http.ParseTime(resp.Header.Get("Date"))
			return resp.StatusCode, d, nil
		}
	}
	if p.Now == nil {
		p.Now = time.Now
	}
	return p
}

// SimulatedProbes answer from the in-memory demo host: files come from
// h.Open, the database and disk have plausible fixed sizes, the socket
// answers when the socket permission is granted, and every URL is
// reachable with an accurate clock. Nothing touches the real machine.
func SimulatedProbes(h host.Host, now func() time.Time) Probes {
	if now == nil {
		now = time.Now
	}
	env := h.Env()
	sizes := map[string]int64{}
	if env.DBPath != "" {
		sizes[env.DBPath] = 2_400_000
		sizes[env.DBPath+"-wal"] = 1_400_000 // over half the database: a demo finding
	}
	return Probes{
		Stat: func(p string) (fs.FileInfo, error) {
			if n, ok := sizes[p]; ok {
				return memInfo{name: path.Base(p), size: n, mode: 0o640}, nil
			}
			f, err := h.Open(host.FileConfig, p)
			if err != nil {
				return nil, err
			}
			return memInfo{name: path.Base(p), size: int64(len(f.Raw)), mode: f.Mode}, nil
		},
		ReadFile: func(p string) ([]byte, error) {
			f, err := h.Open(host.FileConfig, p)
			if err != nil {
				return nil, err
			}
			return f.Raw, nil
		},
		ReadDir: func(string) ([]string, error) { return nil, fs.ErrNotExist },
		DiskFree: func(string) (uint64, uint64, error) {
			return 38 << 30, 80 << 30, nil
		},
		Dial: func(context.Context, string, string) error {
			if p, ok := h.Env().Perms[host.NeedSocket]; ok && !p.OK {
				return fs.ErrPermission
			}
			return nil
		},
		Fetch: func(_ context.Context, url string) (int, time.Time, error) {
			if strings.TrimSpace(url) == "" {
				return 0, time.Time{}, errors.New("no URL")
			}
			return http.StatusOK, now().UTC().Truncate(time.Second), nil
		},
		ClockURL: DefaultClockURL,
		Now:      now,
	}
}

type memInfo struct {
	name string
	size int64
	mode fs.FileMode
}

func (i memInfo) Name() string       { return i.name }
func (i memInfo) Size() int64        { return i.size }
func (i memInfo) Mode() fs.FileMode  { return i.mode }
func (i memInfo) ModTime() time.Time { return time.Time{} }
func (i memInfo) IsDir() bool        { return false }
func (i memInfo) Sys() any           { return nil }
