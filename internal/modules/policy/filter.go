package policy

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tonhe/headsup/internal/module"
	pol "github.com/tonhe/headsup/internal/policy"
	"github.com/tonhe/headsup/internal/ui/pane"
)

// filterView shows the packet filter headscale compiled from the policy
// (/debug/filter, host mode), so what the policy appears to allow can be
// checked against what the server enforces. The access preview (T154) is
// meant to sit next to it.
type filterView struct {
	rules   []pol.FilterRule
	err     error
	loading bool
	offset  int
}

type filterMsg struct {
	rules []pol.FilterRule
	err   error
}

// filterer is implemented by debug clients that can read /debug/filter.
type filterer interface {
	Filter(ctx context.Context) ([]byte, error)
}

var errNoFilter = errors.New("headscale's debug listener (metrics_listen_addr) is not reachable, so the compiled filter cannot be read")

func (m *Model) openFilter(ctx module.Context) tea.Cmd {
	m.back, m.mode, m.filt.offset = m.mode, modeFilter, 0
	h := ctx.Host()
	if h == nil {
		m.filt.err = errNoFilter
		return nil
	}
	dbg := h.Debug()
	f, ok := dbg.(filterer)
	if dbg == nil || !ok || !dbg.Available() {
		m.filt.err = errNoFilter
		return nil
	}
	m.filt.loading, m.filt.err = true, nil
	return func() tea.Msg {
		var b []byte
		err := call(func(c context.Context) (err error) { b, err = f.Filter(c); return })
		if err != nil {
			return filterMsg{err: err}
		}
		rules, err := pol.ParseFilter(b)
		return filterMsg{rules: rules, err: err}
	}
}

// nodeNames maps each node address to its name.
func nodeNames(ctx module.Context) map[netip.Addr]string {
	out := map[netip.Addr]string{}
	for _, n := range ctx.Store().Nodes {
		for _, a := range n.IPAddresses {
			out[a] = n.DisplayName()
		}
	}
	return out
}

func describeIP(s string, names map[netip.Addr]string) string {
	if s == "*" {
		return "*"
	}
	if p, err := netip.ParsePrefix(s); err == nil && p.IsSingleIP() {
		if n, ok := names[p.Addr()]; ok {
			return n + " (" + p.Addr().String() + ")"
		}
		return p.Addr().String()
	}
	if a, err := netip.ParseAddr(s); err == nil {
		if n, ok := names[a]; ok {
			return n + " (" + s + ")"
		}
	}
	return s
}

func (m *Model) filterView(ctx module.Context, w, h int) string {
	s := ctx.Styles()
	f := &m.filt
	iw, ih := pane.Inner(w, h)
	var lines []string
	switch {
	case f.loading:
		lines = append(lines, s.Muted.Render("Reading /debug/filter…"))
	case f.err != nil:
		lines = append(lines, s.Danger.Render(f.err.Error()))
	case len(f.rules) == 0:
		lines = append(lines, s.Muted.Render("headscale compiled an empty filter: no traffic is allowed."))
	default:
		lines = append(lines, s.Muted.Render("What headscale actually enforces, compiled from the live policy. Read-only."), "")
		names := nodeNames(ctx)
		for i, r := range f.rules {
			var src []string
			for _, ip := range r.SrcIPs {
				src = append(src, describeIP(ip, names))
			}
			kind := r.ProtoString()
			if r.CapGrant {
				kind = "app capability grant"
			}
			from := s.Strong.Render(fmt.Sprintf("#%d", i+1)) + " " + s.Muted.Render("from") + " " + strings.Join(src, ", ")
			lines = append(lines, strings.Split(lipgloss.NewStyle().Width(iw).Render(from), "\n")...)
			for _, d := range r.DstPorts {
				lines = append(lines, "     "+s.Muted.Render("to")+" "+s.IP.Render(describeIP(d.IP, names))+":"+s.Accent.Render(d.Ports)+"  "+s.Muted.Render(kind))
			}
			if len(r.DstPorts) == 0 && r.CapGrant {
				lines = append(lines, "     "+s.Muted.Render(kind))
			}
		}
	}
	f.offset = max(0, min(m.offset, len(lines)-ih))
	m.offset = f.offset
	vis := lines[f.offset:min(len(lines), f.offset+ih)]
	for i := range vis {
		vis[i] = pane.Fit(vis[i], iw)
	}
	return pane.Render(s, "Compiled packet filter", strings.Join(vis, "\n"), w, h, true,
		pane.WithExtra("/debug/filter"), pane.WithRight(fmt.Sprintf("%d rules", len(f.rules))))
}
