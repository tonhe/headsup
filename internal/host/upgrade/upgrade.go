// Package upgrade is the upgrade assistant: it compares the installed
// headscale with the latest release, works out the supported path (one
// minor version at a time, never a downgrade, 0.30 only from 0.29.x), and
// prints the package-manager commands for the admin to run. It never runs
// them, and it fetches releases only through an injectable Source.
package upgrade

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tonhe/headsup/internal/host"
)

// Release is one published headscale release.
type Release struct {
	Tag        string    `json:"tag_name"`
	Name       string    `json:"name"`
	Body       string    `json:"body"`
	URL        string    `json:"html_url"`
	Published  time.Time `json:"published_at"`
	Prerelease bool      `json:"prerelease"`
	Draft      bool      `json:"draft"`
}

// Source lists releases, newest first or in any order.
type Source interface {
	Releases(ctx context.Context) ([]Release, error)
}

// DefaultReleasesURL is headscale's GitHub releases API.
const DefaultReleasesURL = "https://api.github.com/repos/juanfont/headscale/releases?per_page=50"

// GitHub fetches releases from the GitHub API.
type GitHub struct {
	Client *http.Client // nil uses a client with a 10s timeout
	URL    string       // "" uses DefaultReleasesURL
}

// Releases implements Source.
func (g GitHub) Releases(ctx context.Context) ([]Release, error) {
	cl := g.Client
	if cl == nil {
		cl = &http.Client{Timeout: 10 * time.Second}
	}
	u := g.URL
	if u == "" {
		u = DefaultReleasesURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := cl.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch releases: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch releases: GitHub answered %s", resp.Status)
	}
	var out []Release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&out); err != nil {
		return nil, fmt.Errorf("fetch releases: %w", err)
	}
	return out, nil
}

// Version is a parsed semantic version.
type Version struct {
	Major, Minor, Patch int
	Pre                 string
}

var verRE = regexp.MustCompile(`^v?(\d+)\.(\d+)(?:\.(\d+))?(?:-([0-9A-Za-z.\-]+))?`)

// Parse reads "v0.30.1", "0.29.6" or "0.30.0-beta.2".
func Parse(s string) (Version, bool) {
	m := verRE.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return Version{}, false
	}
	a, _ := strconv.Atoi(m[1])
	b, _ := strconv.Atoi(m[2])
	c, _ := strconv.Atoi(m[3])
	return Version{a, b, c, m[4]}, true
}

func (v Version) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Pre != "" {
		s += "-" + v.Pre
	}
	return s
}

// Compare orders versions; a pre-release sorts before its release.
func (v Version) Compare(o Version) int {
	for _, d := range []int{v.Major - o.Major, v.Minor - o.Minor, v.Patch - o.Patch} {
		if d != 0 {
			if d < 0 {
				return -1
			}
			return 1
		}
	}
	switch {
	case v.Pre == o.Pre:
		return 0
	case v.Pre == "":
		return 1
	case o.Pre == "":
		return -1
	}
	return strings.Compare(v.Pre, o.Pre)
}

func (v Version) series() [2]int { return [2]int{v.Major, v.Minor} }

// Install is how headscale was installed, which decides the commands.
type Install int

const (
	InstallUnknown Install = iota
	InstallDeb
	InstallBinary
	InstallContainer
)

func (i Install) String() string {
	return [...]string{"unknown", "deb package", "binary", "container"}[i]
}

// Target describes the machine the commands are for.
type Target struct {
	Install   Install
	Arch      string // amd64, arm64; "" = this machine
	Runtime   string // docker | podman for containers
	Container string
	Service   host.ServiceKind
}

// TargetFor guesses the install method from the detected environment.
func TargetFor(env host.Env) Target {
	t := Target{Arch: runtime.GOARCH, Runtime: env.ContainerRuntime, Container: env.Container, Service: env.Service}
	switch {
	case env.Service == host.ServiceContainer || env.Transport == host.TransportContainerExec:
		t.Install = InstallContainer
	case env.Service == host.ServiceSystemd && (env.ConfigPath == "" || strings.HasPrefix(env.ConfigPath, "/etc/headscale/")):
		t.Install = InstallDeb
	case env.Service != host.ServiceNone || env.ConfigPath != "":
		t.Install = InstallBinary
	}
	return t
}

// Plan is the assistant's answer.
type Plan struct {
	Installed Version
	Latest    *Release // newest stable release
	Next      *Release // the release to install now; nil when up to date or blocked
	// Path lists every release still needed to reach Latest, Next first.
	Path     []Release
	UpToDate bool
	// Blocked explains why no upgrade can be suggested (unknown version, ...).
	Blocked string
	// Notes are the releases newer than the installed one up to Next,
	// newest first, whose notes the admin should read.
	Notes []Release
	// Steps are what to do, in order; Commands are the shell commands among
	// them, for copying. Nothing is ever run.
	Steps    []string
	Commands []string
}

// Assess builds the plan from the installed version and the releases.
func Assess(installed string, releases []Release, t Target) Plan {
	var p Plan
	iv, ok := Parse(installed)
	if !ok {
		p.Blocked = fmt.Sprintf("The installed version %q is not a release version (a development build?), so no upgrade path can be worked out.", installed)
		return p
	}
	p.Installed = iv
	type rv struct {
		v Version
		r Release
	}
	var stable []rv
	for _, r := range releases {
		v, ok := Parse(r.Tag)
		if !ok || r.Draft || r.Prerelease || v.Pre != "" {
			continue
		}
		stable = append(stable, rv{v, r})
	}
	if len(stable) == 0 {
		p.Blocked = "No stable releases were found."
		return p
	}
	sort.Slice(stable, func(i, j int) bool { return stable[i].v.Compare(stable[j].v) < 0 })
	latest := stable[len(stable)-1]
	p.Latest = &latest.r
	if iv.Compare(latest.v) >= 0 {
		p.UpToDate = true
		if iv.Compare(latest.v) > 0 {
			p.Steps = []string{fmt.Sprintf("The installed %s is newer than the latest release %s. Downgrades are not supported; nothing to do.", iv, latest.v)}
		}
		return p
	}

	// Latest patch of each minor series newer than the installed one.
	best := map[[2]int]rv{}
	var series [][2]int
	for _, s := range stable {
		if s.v.Compare(iv) <= 0 {
			continue
		}
		k := s.v.series()
		if _, seen := best[k]; !seen {
			series = append(series, k)
		}
		best[k] = s // ascending, so the last one wins
	}
	sort.Slice(series, func(i, j int) bool {
		if series[i][0] != series[j][0] {
			return series[i][0] < series[j][0]
		}
		return series[i][1] < series[j][1]
	})
	// One minor at a time: a newer patch of the installed series is skipped
	// when the next series exists, since the next series is reachable from
	// any patch of this one.
	if len(series) > 1 && series[0] == iv.series() {
		series = series[1:]
	}
	for _, k := range series {
		p.Path = append(p.Path, best[k].r)
	}
	next := best[series[0]]
	p.Next = &p.Path[0]
	for i := len(stable) - 1; i >= 0; i-- {
		if s := stable[i]; s.v.Compare(iv) > 0 && s.v.Compare(next.v) <= 0 {
			p.Notes = append(p.Notes, s.r)
		}
	}

	nv := next.v
	step := 1
	add := func(s string) { p.Steps = append(p.Steps, fmt.Sprintf("%d. %s", step, s)); step++ }
	if len(p.Path) > 1 {
		hops := make([]string, len(p.Path))
		for i, r := range p.Path {
			v, _ := Parse(r.Tag)
			hops[i] = v.String()
		}
		add(fmt.Sprintf("Upgrade one minor version at a time: %s -> %s. Do %s now, check it, then come back.", iv, strings.Join(hops, " -> "), nv))
	}
	if iv.Major == 0 && iv.Minor < 29 && reaches(p.Path, 0, 30) {
		add("0.30 can only be reached from 0.29.x, so 0.29 comes first.")
	}
	add("Read the release notes below for breaking config changes.")
	add("Take a backup first (press b).")
	cmds := Commands(nv, t)
	for _, c := range cmds {
		add("Run: " + c)
	}
	add("Check the config with: headscale configtest, and the server with the doctor (press c).")
	p.Commands = cmds
	return p
}

func reaches(path []Release, major, minor int) bool {
	for _, r := range path {
		if v, _ := Parse(r.Tag); v.Major == major && v.Minor == minor {
			return true
		}
	}
	return false
}

// Commands are the shell commands that install v for t. They are printed,
// never executed.
func Commands(v Version, t Target) []string {
	arch := t.Arch
	if arch == "" {
		arch = runtime.GOARCH
	}
	ver := v.String()
	base := "https://github.com/juanfont/headscale/releases/download/v" + ver + "/"
	restart := "sudo systemctl restart headscale"
	if t.Service == host.ServiceOpenRC {
		restart = "sudo rc-service headscale restart"
	}
	switch t.Install {
	case InstallContainer:
		rt := t.Runtime
		if rt == "" {
			rt = "docker"
		}
		name := t.Container
		if name == "" {
			name = "headscale"
		}
		return []string{
			rt + " pull docker.io/headscale/headscale:" + ver,
			"# then recreate " + name + " with image tag " + ver + ", e.g. edit your compose file and run: " + rt + " compose up -d",
		}
	case InstallDeb:
		deb := fmt.Sprintf("headscale_%s_linux_%s.deb", ver, arch)
		return []string{
			"curl -fLO " + base + deb,
			"sudo apt install ./" + deb,
			restart,
		}
	default:
		bin := fmt.Sprintf("headscale_%s_linux_%s", ver, arch)
		return []string{
			"curl -fLo headscale " + base + bin,
			"sudo install -m 0755 headscale $(command -v headscale || echo /usr/local/bin/headscale)",
			restart,
		}
	}
}

// ErrDisabled means update checks are turned off.
var ErrDisabled = errors.New("update checks are turned off")

// Check fetches releases and assesses them. When disabled (headsup's
// check_updates or headscale's disable_check_updates), it does not fetch.
func Check(ctx context.Context, src Source, installed string, t Target, disabled bool) (Plan, error) {
	if disabled || src == nil {
		return Plan{}, ErrDisabled
	}
	rs, err := src.Releases(ctx)
	if err != nil {
		return Plan{}, err
	}
	return Assess(installed, rs, t), nil
}

var (
	mdLink    = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`)
	mdEmph    = regexp.MustCompile(`(\*\*|__|\x60)`)
	mdHeading = regexp.MustCompile(`^#{1,6}\s+`)
	mdBullet  = regexp.MustCompile(`^(\s*)[*+-]\s+`)
	htmlTag   = regexp.MustCompile(`<[^>]+>`)
)

// NotesLines renders release-notes markdown as plain lines: heading marks
// and emphasis are dropped, list items get the bullet glyph, links keep
// their text, HTML tags are removed and runs of blank lines collapse.
func NotesLines(body, bullet string) []string {
	var out []string
	blank := false
	for _, l := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		l = strings.TrimRight(l, " \t")
		l = htmlTag.ReplaceAllString(l, "")
		l = mdLink.ReplaceAllString(l, "$1")
		l = mdEmph.ReplaceAllString(l, "")
		if mdHeading.MatchString(l) {
			l = mdHeading.ReplaceAllString(l, "")
		}
		l = mdBullet.ReplaceAllString(l, "${1}"+bullet+" ")
		if strings.TrimSpace(l) == "" {
			if !blank && len(out) > 0 {
				out = append(out, "")
			}
			blank = true
			continue
		}
		blank = false
		out = append(out, l)
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}
