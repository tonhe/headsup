package server

import (
	"fmt"
	"strings"

	"github.com/tonhe/headsup/internal/api"
	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/ui/pane"
	"github.com/tonhe/headsup/internal/ui/table"
)

// Avail is whether a feature works in a mode or in this session.
type Avail int

const (
	AvailNo Avail = iota
	AvailYes
	AvailPartial // works with a limit, given in the reason
)

// CapRow is one line of the capability matrix.
type CapRow struct {
	Feature string
	Remote  Avail // in remote (API) mode, on a server that supports it
	Host    Avail // in host mode, with the access it needs
	Now     Avail // in this session: this server, this mode, these permissions
	Why     string
	// Note explains the feature; config-file-only features say so and link
	// to headscale's documentation.
	Note string
}

const docs = "https://headscale.net/stable/ref/"

// hostFeature is a capability that comes from the host, not the API.
type hostFeature struct {
	name   string
	needs  host.Need
	remote Avail
	note   string
}

var hostFeatures = []hostFeature{
	{"Server Settings (config.yaml)", host.NeedConfigR, AvailNo,
		"Config file only: headscale has no API for its settings. Edit them on the host. Docs: " + docs + "configuration/"},
	{"DNS: MagicDNS, nameservers, records", host.NeedConfigR, AvailNo,
		"Config file only (dns.*, and the extra-records file). Docs: " + docs + "dns/"},
	{"DERP map and embedded relay", host.NeedConfigR, AvailNo,
		"Config file only (derp.*, plus local map files). Docs: " + docs + "derp/"},
	{"OIDC login", host.NeedConfigR, AvailNo,
		"Config file only (oidc.*). Docs: " + docs + "oidc/"},
	{"TLS and ACME certificates", host.NeedConfigR, AvailNo,
		"Config file only (tls.*). Docs: " + docs + "tls/"},
	{"Policy file (policy.mode: file)", host.NeedPolicyW, AvailNo,
		"With policy.mode file the policy lives on disk and the API cannot change it. Docs: " + docs + "acls/"},
	{"Service start, stop, restart", host.NeedService, AvailNo,
		"Uses the service manager (systemd, OpenRC, container) on the host."},
	{"Server logs", host.NeedLogs, AvailNo, "Reads journald, the container log or the log file on the host."},
	{"Live debug data and metrics", host.NeedDebug, AvailNo,
		"Reads metrics_listen_addr (/debug, /metrics), which is bound to localhost by default."},
	{"Machine OS, client, DERP detail", host.NeedDBRead, AvailNo,
		"Read-only from the database; the API does not expose host info."},
	{"Doctor", host.NeedConfigR, AvailNo,
		"Checks permissions, socket, server_url, certificates, deprecated keys, database, disk and clock."},
	{"Backup and restore (files)", host.NeedConfigR, AvailNo,
		"Bundles the config, policy, records, DERP files, keys and an online database copy. Database restore is given as steps."},
	{"Upgrade assistant", host.NeedNone, AvailPartial,
		"Compares with the latest release and prints the upgrade commands; it never runs them."},
}

func capNote(c api.Cap) string {
	switch c {
	case api.CapHealthEndpoint:
		return "GET /health answers when headscale and its database are up."
	case api.CapPolicyCheck:
		return "Validate a policy on the server before saving it."
	case api.CapAuthApproval:
		return "Approve or reject web logins waiting in Add device."
	case api.CapPreAuthKeyDelete:
		return "Delete pre-auth keys, not just expire them."
	case api.CapAPIKeyDelete:
		return "Delete API keys, not just expire them."
	case api.CapPolicyWritable:
		return "The API can change the policy only when policy.mode is database; with file mode, edit it on the host."
	case api.CapOAuthClients:
		return "Planned in headscale; not available yet."
	case api.CapTagOnlyKeys:
		return "Pre-auth keys owned by tags instead of a user."
	case api.CapProblemJSON:
		return "Structured RFC 7807 error responses."
	case api.CapDisableExpiry:
		return "Mark a machine's key as never expiring."
	case api.CapBackfillIPs:
		return "Give machines missing an address one from the configured prefixes (F)."
	}
	return ""
}

// sinceFor is the first supported version with c, or "".
func sinceFor(c api.Cap) string {
	for _, v := range []string{"0.29.0", "0.29.6", "0.30.0"} {
		if api.CapsForVersion(v).Has(c) {
			return v
		}
	}
	return ""
}

// Matrix builds the capability matrix for this server's capabilities, the
// mode (host is nil in remote mode) and the host's permissions.
func Matrix(caps api.Capabilities, version string, h host.Host) []CapRow {
	var rows []CapRow
	for _, c := range api.AllCaps() {
		r := CapRow{Feature: capTitle(c), Remote: AvailYes, Host: AvailYes, Note: capNote(c)}
		if c == api.CapOAuthClients {
			r.Remote, r.Host = AvailNo, AvailNo
		}
		switch {
		case caps.Has(c):
			r.Now = AvailYes
		case c == api.CapOAuthClients:
			r.Why = "planned"
		case c == api.CapPolicyWritable:
			r.Why = "policy.mode is file"
			if h != nil {
				r.Now, r.Why = AvailPartial, "edit the file on the host"
			}
		default:
			if s := sinceFor(c); s != "" {
				r.Why = "needs headscale " + strings.TrimSuffix(s, ".0")
			} else {
				r.Why = "not on headscale " + version
			}
		}
		rows = append(rows, r)
	}
	var perms host.Perms
	if h != nil {
		perms = h.Env().Perms
	}
	for _, f := range hostFeatures {
		r := CapRow{Feature: f.name, Remote: f.remote, Host: AvailYes, Note: f.note}
		switch {
		case h == nil && f.remote == AvailNo:
			r.Why = "needs host mode"
		case h == nil:
			r.Now, r.Why = AvailPartial, "commands only"
		default:
			if p, missing := perms.Missing(f.needs); missing {
				r.Why = "no access"
				if p.FixCommand != "" {
					r.Note += "\nFix: " + p.FixCommand
				}
			} else {
				r.Now = AvailYes
			}
		}
		rows = append(rows, r)
	}
	return rows
}

func capTitle(c api.Cap) string {
	n := c.String()
	return strings.ToUpper(n[:1]) + n[1:]
}

type capRow struct {
	r  CapRow
	i  int
	gl func(Avail, string) string
}

func (c capRow) Key() string { return c.r.Feature }
func (c capRow) Cell(col string) string {
	switch col {
	case "feature":
		return c.r.Feature
	case "remote":
		return c.gl(c.r.Remote, "")
	case "host":
		return c.gl(c.r.Host, "")
	case "now":
		return c.gl(c.r.Now, c.r.Why)
	}
	return ""
}
func (c capRow) SortValue(col string) string {
	if col == "feature" {
		return c.r.Feature
	}
	return fmt.Sprintf("%03d", c.i)
}

func capColumns() []table.Column {
	return []table.Column{
		{Key: "feature", Title: "Feature", Width: 30, MinWidth: 20, Flex: true},
		{Key: "remote", Title: "Remote", Width: 7, Priority: 2},
		{Key: "host", Title: "Host", Width: 6, Priority: 2},
		{Key: "now", Title: "Here", Width: 26, MinWidth: 10, Priority: 1},
	}
}

func (m *Model) refreshCaps(ctx module.Context) {
	if m.caps == nil {
		return
	}
	s, ic := ctx.Styles(), ctx.Icons()
	glyph := func(a Avail, why string) string {
		var g string
		switch a {
		case AvailYes:
			g = s.OK.Render(ic.Check)
		case AvailPartial:
			g = s.Warn.Render(ic.Warn)
		default:
			g = s.Muted.Render(ic.Cross)
		}
		if why != "" {
			g += " " + s.Muted.Render(why)
		}
		return g
	}
	caps := ctx.Backend().Capabilities()
	rows := Matrix(caps, ctx.Store().Info.Version, ctx.Host())
	trs := make([]table.Row, len(rows))
	for i, r := range rows {
		trs[i] = capRow{r: r, i: i, gl: glyph}
	}
	m.caps.SetRows(trs)
}

func (m *Model) capsView(ctx module.Context, w, h int) string {
	s := ctx.Styles()
	noteH := 4
	if h < 12 {
		noteH = 3
	}
	tableH := h - noteH
	m.caps.SetSize(max(0, w-2), max(0, tableH-2))
	mode := "remote mode"
	if ctx.Host() != nil {
		mode = "host mode"
	}
	ver := ctx.Store().Info.Version
	if ver == "" {
		ver = "unknown version"
	}
	extra := s.Muted.Render(ver + " " + ctx.Icons().Bullet + " " + mode)
	tbl := pane.Render(s, "Capabilities", m.caps.View(), w, tableH, true, pane.NoPad(), pane.WithExtra(extra))

	nw, nh := pane.Inner(w, noteH)
	var lines []string
	if r, ok := m.caps.Cursor(); ok {
		cr := r.(capRow).r
		for _, l := range wrap(cr.Note, nw) {
			if strings.HasPrefix(l, "Fix: ") {
				l = s.Accent.Render(l)
			}
			lines = append(lines, pane.Fit(l, nw))
		}
	}
	if len(lines) > nh {
		lines = lines[:nh]
	}
	return tbl + "\n" + pane.Render(s, "About", strings.Join(lines, "\n"), w, noteH, false)
}
