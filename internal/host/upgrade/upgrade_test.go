package upgrade_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/host/upgrade"
)

func rels(tags ...string) []upgrade.Release {
	out := make([]upgrade.Release, len(tags))
	for i, t := range tags {
		out[i] = upgrade.Release{Tag: t, Body: "## " + t + "\n- change in " + t}
	}
	return out
}

var all = rels("v0.28.0", "v0.28.2", "v0.29.0", "v0.29.6", "v0.30.0", "v0.30.1")

func tags(rs []upgrade.Release) string {
	var s []string
	for _, r := range rs {
		s = append(s, r.Tag)
	}
	return strings.Join(s, " ")
}

var deb = upgrade.Target{Install: upgrade.InstallDeb, Arch: "amd64", Service: host.ServiceSystemd}

func TestPath(t *testing.T) {
	for _, tt := range []struct {
		installed, next, path string
		upToDate              bool
	}{
		{"0.30.1", "", "", true},
		{"v0.30.0", "v0.30.1", "v0.30.1", false},
		{"0.29.3", "v0.30.1", "v0.30.1", false}, // 0.30 directly from any 0.29.x
		{"0.29.6", "v0.30.1", "v0.30.1", false},
		{"0.28.0", "v0.29.6", "v0.29.6 v0.30.1", false}, // one minor at a time
		{"0.27.1", "v0.28.2", "v0.28.2 v0.29.6 v0.30.1", false},
		{"0.31.0", "", "", true}, // newer than latest: no downgrade
	} {
		p := upgrade.Assess(tt.installed, all, deb)
		if p.UpToDate != tt.upToDate || p.Blocked != "" {
			t.Errorf("%s: upToDate=%v blocked=%q", tt.installed, p.UpToDate, p.Blocked)
		}
		got := ""
		if p.Next != nil {
			got = p.Next.Tag
		}
		if got != tt.next || tags(p.Path) != tt.path {
			t.Errorf("%s: next %q path %q, want %q %q", tt.installed, got, tags(p.Path), tt.next, tt.path)
		}
		if p.Latest == nil || p.Latest.Tag != "v0.30.1" {
			t.Errorf("%s: latest %+v", tt.installed, p.Latest)
		}
		if tt.installed == "0.31.0" && !strings.Contains(strings.Join(p.Steps, " "), "Downgrades are not supported") {
			t.Errorf("newer install should explain no downgrade: %v", p.Steps)
		}
	}
}

func TestNotesAndSteps(t *testing.T) {
	p := upgrade.Assess("0.28.0", all, deb)
	if got := tags(p.Notes); got != "v0.29.6 v0.29.0 v0.28.2" {
		t.Errorf("notes %q", got)
	}
	steps := strings.Join(p.Steps, "\n")
	for _, want := range []string{"one minor version at a time", "0.30 can only be reached from 0.29.x", "backup", "configtest",
		"curl -fLO https://github.com/juanfont/headscale/releases/download/v0.29.6/headscale_0.29.6_linux_amd64.deb",
		"sudo apt install ./headscale_0.29.6_linux_amd64.deb"} {
		if !strings.Contains(steps, want) {
			t.Errorf("steps lack %q:\n%s", want, steps)
		}
	}
	if len(p.Commands) != 3 {
		t.Errorf("commands %v", p.Commands)
	}
	// The backup comes before any command.
	if strings.Index(steps, "backup") > strings.Index(steps, "curl") {
		t.Error("backup must be offered before the upgrade")
	}
	p = upgrade.Assess("0.29.6", all, deb)
	if strings.Contains(strings.Join(p.Steps, "\n"), "one minor version") {
		t.Error("a single hop needs no path warning")
	}
}

func TestPrereleasesAndDraftsIgnored(t *testing.T) {
	rs := append(rels("v0.29.6", "v0.31.0-beta.1"), upgrade.Release{Tag: "v0.30.0", Draft: true}, upgrade.Release{Tag: "v0.30.1", Prerelease: true}, upgrade.Release{Tag: "nightly"})
	p := upgrade.Assess("0.29.6", rs, deb)
	if !p.UpToDate || p.Latest.Tag != "v0.29.6" {
		t.Errorf("only stable releases count: %+v", p)
	}
}

func TestBlocked(t *testing.T) {
	if p := upgrade.Assess("dev", all, deb); p.Blocked == "" || p.Next != nil {
		t.Errorf("dev build: %+v", p)
	}
	if p := upgrade.Assess("0.29.0", nil, deb); p.Blocked == "" {
		t.Errorf("no releases: %+v", p)
	}
}

func TestCommands(t *testing.T) {
	v, _ := upgrade.Parse("v0.30.1")
	bin := strings.Join(upgrade.Commands(v, upgrade.Target{Install: upgrade.InstallBinary, Arch: "arm64", Service: host.ServiceOpenRC}), "\n")
	if !strings.Contains(bin, "headscale_0.30.1_linux_arm64") || !strings.Contains(bin, "rc-service headscale restart") {
		t.Errorf("binary: %s", bin)
	}
	ctr := strings.Join(upgrade.Commands(v, upgrade.Target{Install: upgrade.InstallContainer, Runtime: "podman", Container: "hs"}), "\n")
	if !strings.Contains(ctr, "podman pull docker.io/headscale/headscale:0.30.1") || !strings.Contains(ctr, "recreate hs") {
		t.Errorf("container: %s", ctr)
	}
	if c := upgrade.Commands(v, upgrade.Target{Install: upgrade.InstallContainer}); !strings.HasPrefix(c[0], "docker pull") {
		t.Errorf("default runtime: %v", c)
	}
}

func TestTargetFor(t *testing.T) {
	for _, tt := range []struct {
		env  host.Env
		want upgrade.Install
	}{
		{host.Env{Service: host.ServiceSystemd, ConfigPath: "/etc/headscale/config.yaml"}, upgrade.InstallDeb},
		{host.Env{Service: host.ServiceSystemd, ConfigPath: "/opt/hs/config.yaml"}, upgrade.InstallBinary},
		{host.Env{Service: host.ServiceContainer, Container: "hs"}, upgrade.InstallContainer},
		{host.Env{Transport: host.TransportContainerExec}, upgrade.InstallContainer},
		{host.Env{}, upgrade.InstallUnknown},
	} {
		if got := upgrade.TargetFor(tt.env).Install; got != tt.want {
			t.Errorf("%+v: %v, want %v", tt.env, got, tt.want)
		}
	}
	if upgrade.InstallDeb.String() != "deb package" {
		t.Error("install name")
	}
}

func TestParseCompare(t *testing.T) {
	a, _ := upgrade.Parse("0.30.0-beta.1")
	b, _ := upgrade.Parse("v0.30.0")
	c, _ := upgrade.Parse("0.30")
	if a.Compare(b) >= 0 || b.Compare(a) <= 0 || b.Compare(c) != 0 || a.String() != "0.30.0-beta.1" {
		t.Errorf("compare: %v %v %v", a, b, c)
	}
	d, _ := upgrade.Parse("0.30.0-beta.2")
	if a.Compare(d) >= 0 {
		t.Error("pre-release ordering")
	}
	if _, ok := upgrade.Parse("main"); ok {
		t.Error("non-version parsed")
	}
}

type fakeSource struct {
	rs    []upgrade.Release
	err   error
	calls int
}

func (f *fakeSource) Releases(context.Context) ([]upgrade.Release, error) {
	f.calls++
	return f.rs, f.err
}

func TestCheck(t *testing.T) {
	src := &fakeSource{rs: all}
	if _, err := upgrade.Check(context.Background(), src, "0.29.6", deb, true); !errors.Is(err, upgrade.ErrDisabled) || src.calls != 0 {
		t.Errorf("disabled must not fetch: %v calls=%d", err, src.calls)
	}
	p, err := upgrade.Check(context.Background(), src, "0.29.6", deb, false)
	if err != nil || p.Next == nil || p.Next.Tag != "v0.30.1" {
		t.Errorf("check: %+v %v", p, err)
	}
	src.err = errors.New("offline")
	if _, err := upgrade.Check(context.Background(), src, "0.29.6", deb, false); err == nil {
		t.Error("source error should surface")
	}
	if _, err := upgrade.Check(context.Background(), nil, "0.29.6", deb, false); !errors.Is(err, upgrade.ErrDisabled) {
		t.Error("nil source")
	}
}

func TestGitHubSource(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/vnd.github+json" {
			t.Errorf("accept %q", r.Header.Get("Accept"))
		}
		switch r.URL.Path {
		case "/ok":
			w.Write([]byte(`[{"tag_name":"v0.30.1","name":"v0.30.1","body":"notes","html_url":"https://x","published_at":"2026-10-01T00:00:00Z","prerelease":false,"draft":false}]`))
		case "/bad":
			w.Write([]byte(`{`))
		default:
			http.Error(w, "rate limited", http.StatusForbidden)
		}
	}))
	defer srv.Close()
	rs, err := upgrade.GitHub{Client: srv.Client(), URL: srv.URL + "/ok"}.Releases(context.Background())
	if err != nil || len(rs) != 1 || rs[0].Tag != "v0.30.1" || rs[0].Published.IsZero() {
		t.Fatalf("releases %+v %v", rs, err)
	}
	if _, err := (upgrade.GitHub{URL: srv.URL + "/bad"}).Releases(context.Background()); err == nil {
		t.Error("bad JSON should fail")
	}
	if _, err := (upgrade.GitHub{URL: srv.URL + "/limit"}).Releases(context.Background()); err == nil || !strings.Contains(err.Error(), "403") {
		t.Errorf("status: %v", err)
	}
	if _, err := (upgrade.GitHub{URL: "http://127.0.0.1:1/"}).Releases(context.Background()); err == nil {
		t.Error("unreachable should fail")
	}
	if _, err := (upgrade.GitHub{URL: "::"}).Releases(context.Background()); err == nil {
		t.Error("bad URL should fail")
	}
}

func TestNotesLines(t *testing.T) {
	body := "## Breaking\r\n\r\n\r\n- **Removed** `grpc_listen_addr` ([#123](https://github.com/x))\r\n* new <b>thing</b>\n\n\n"
	got := upgrade.NotesLines(body, "•")
	want := []string{"Breaking", "", "• Removed grpc_listen_addr (#123)", "• new thing"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %q\nwant %q", got, want)
	}
}
