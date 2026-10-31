package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/host/hsconfig"
	"github.com/tonhe/headsup/internal/host/upgrade"
	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/ui/pane"
)

type upgradeMsg struct {
	plan upgrade.Plan
	err  error
}

// demoReleases answer the upgrade check on the simulated demo host, which
// never contacts GitHub.
type demoReleases struct{ now time.Time }

func (d demoReleases) Releases(context.Context) ([]upgrade.Release, error) {
	day := 24 * time.Hour
	return []upgrade.Release{
		{Tag: "v0.29.6", Published: d.now.Add(-120 * day), Body: "## Fixes\n- Expired nodes keep their routes until they reconnect."},
		{Tag: "v0.30.0", Published: d.now.Add(-60 * day), Body: "## Breaking\n- `tls_*` and `acme_*` keys move under `tls:`; the old keys are still read but deprecated.\n- `grpc_listen_addr` is removed.\n\n## Features\n- Tag-only pre-auth keys.\n- RFC 7807 error responses."},
		{Tag: "v0.30.1", Published: d.now.Add(-10 * day), Body: "## Fixes\n- Fix a crash when a DERP map file is empty.\n- **Security**: validate extra records before reloading them."},
	}, nil
}

func (m *Model) checkUpgrade(ctx module.Context) tea.Cmd {
	installed := ctx.Store().Info.Version
	h := ctx.Host()
	var env host.Env
	if h != nil {
		env = h.Env()
		if installed == "" {
			installed = env.Version
		}
	}
	target := upgrade.TargetFor(env)
	if m.arch != "" {
		target.Arch = m.arch
	}
	if h == nil {
		target = upgrade.Target{Install: upgrade.InstallUnknown}
	}
	src := m.src
	switch {
	case src != nil:
	case simulated(ctx):
		src = demoReleases{now: ctx.Now()}
	default:
		src = upgrade.GitHub{}
	}
	disabled := !m.hostSettings(ctx).CheckUpdates
	return func() tea.Msg {
		off := disabled
		if !off && h != nil {
			if f, err := h.Open(host.FileConfig, ""); err == nil {
				if d, err := hsconfig.Parse(f.Raw); err == nil {
					if v, ok := d.Get("disable_check_updates"); ok && strings.TrimSpace(v) == "true" {
						off = true
					}
				}
			}
		}
		c, cancel := context.WithTimeout(bg(), 15*time.Second)
		defer cancel()
		p, err := upgrade.Check(c, src, installed, target, off)
		return upgradeMsg{plan: p, err: err}
	}
}

func (m *Model) onUpgrade(msg upgradeMsg) tea.Cmd {
	m.upBusy = false
	m.upErr = msg.err
	if msg.err == nil {
		p := msg.plan
		m.plan = &p
	}
	err := msg.err
	text := ""
	switch {
	case errors.Is(err, upgrade.ErrDisabled):
		err = nil
		text = "Update checks are turned off (check_updates, or headscale's disable_check_updates)."
	case err != nil:
	case msg.plan.Blocked != "":
		text = msg.plan.Blocked
	case msg.plan.UpToDate:
		text = fmt.Sprintf("headscale %s is up to date.", msg.plan.Installed)
	case msg.plan.Next != nil:
		text = "Upgrade available: " + msg.plan.Next.Tag + "."
	}
	return func() tea.Msg { return module.ActionResult{ActionID: "server.upgrade", Err: err, Message: text} }
}

func (m *Model) upgradeLines(ctx module.Context, w int) []string {
	s, ic := ctx.Styles(), ctx.Icons()
	var lines []string
	add := func(l string) { lines = append(lines, pane.Fit(l, w)) }
	addWrapped := func(text string, render func(...string) string) {
		for _, l := range wrap(text, w) {
			add(render(l))
		}
	}
	switch {
	case m.upBusy:
		add(s.Muted.Render("Checking the latest release" + ic.Ellipsis))
		return lines
	case errors.Is(m.upErr, upgrade.ErrDisabled):
		addWrapped("Update checks are turned off by check_updates in this tool's config or headscale's disable_check_updates.", s.Muted.Render)
		return lines
	case m.upErr != nil:
		addWrapped("Could not check for upgrades: "+m.upErr.Error(), s.Danger.Render)
		add(s.Muted.Render("Press U to try again."))
		return lines
	case m.plan == nil:
		addWrapped("Press U to compare this server with the latest headscale release. Nothing is installed for you: the commands are shown for you to run.", s.Muted.Render)
		return lines
	}
	p := m.plan
	kvl := func(k, v string) { add(s.Label.Render(fmt.Sprintf("%-10s", k)) + v) }
	if p.Blocked != "" {
		addWrapped(p.Blocked, s.Warn.Render)
		return lines
	}
	kvl("Installed", p.Installed.String())
	if p.Latest != nil {
		kvl("Latest", p.Latest.Tag+s.Muted.Render(" "+p.Latest.Published.Format("2006-01-02")))
	}
	if p.UpToDate {
		add(s.OK.Render(ic.Check + " Up to date"))
		for _, st := range p.Steps {
			addWrapped(st, s.Muted.Render)
		}
		return lines
	}
	if p.Next != nil {
		kvl("Next", s.Accent.Render(p.Next.Tag))
	}
	add("")
	for _, st := range p.Steps {
		render := s.Text.Render
		if strings.Contains(st, "Run: ") {
			render = s.Accent.Render
		}
		addWrapped(st, render)
	}
	for _, r := range p.Notes {
		add("")
		add(s.Strong.Render("Release notes " + r.Tag))
		for _, l := range upgrade.NotesLines(r.Body, ic.Bullet) {
			if l == "" {
				add("")
				continue
			}
			addWrapped(l, s.Text.Render)
		}
	}
	return lines
}

func (m *Model) upgradeView(ctx module.Context, w, h int) string {
	s := ctx.Styles()
	iw, ih := pane.Inner(w, h)
	lines := m.upgradeLines(ctx, iw)
	m.upTop = max(0, min(m.upTop, len(lines)-ih))
	end := min(len(lines), m.upTop+ih)
	extra := ""
	if len(lines) > ih {
		extra = s.Muted.Render(fmt.Sprintf("%d-%d of %d", m.upTop+1, end, len(lines)))
	}
	return pane.Render(s, "Upgrade", strings.Join(lines[m.upTop:end], "\n"), w, h, true, pane.WithExtra(extra))
}
