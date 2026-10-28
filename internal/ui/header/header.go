// Package header renders the one-line application header.
package header

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/tonhe/headsup/internal/icons"
	"github.com/tonhe/headsup/internal/ui/styles"
)

// Header is what the header shows. Icons defaults to icons.Unicode.
type Header struct {
	App       string // "headsup"
	Profile   string
	ModeLabel string // "host:hs01" or "remote"
	Version   string // headscale version, "v0.29.6"
	Healthy   bool
	Online    int
	Total     int
	Stale     bool // data is stale (refresh failing)
	Icons     icons.Set
}

// Render draws h as exactly one line of w cells on the header background.
func Render(s *styles.Styles, h Header, w int) string {
	if w <= 0 {
		return ""
	}
	ic := h.Icons
	if ic.Name == "" {
		ic = icons.Unicode
	}
	bg := s.HeaderDim
	on := func(st lipgloss.Style) lipgloss.Style { return st.Inherit(bg) }
	sep := bg.Render(" ─ ")

	app := s.HeaderTitle.Render(h.App)
	var prof string
	if h.Profile != "" {
		prof = on(s.Text).Render(h.Profile)
	}
	if h.ModeLabel != "" && h.ModeLabel != h.Profile {
		ml := bg.Render(h.ModeLabel)
		if prof != "" {
			ml = bg.Render(" (") + ml + bg.Render(")")
		}
		prof += ml
	}
	var ver string
	if h.Version != "" {
		dot := on(s.OK).Render(ic.Online)
		if !h.Healthy {
			dot = on(s.Danger).Render(ic.Offline)
		}
		ver = bg.Render("headscale "+h.Version+" ") + dot
	}
	var stale string
	if h.Stale {
		stale = on(s.Warn).Render(ic.Warn + " stale")
	}
	var counts string
	if h.Total > 0 || h.Online > 0 {
		counts = on(s.Online).Render(fmt.Sprint(h.Online)) + bg.Render(fmt.Sprintf(" online / %d", h.Total))
	}

	build := func(parts []string) string {
		var nz []string
		for _, p := range parts {
			if p != "" {
				nz = append(nz, p)
			}
		}
		return strings.Join(nz, sep)
	}
	// Drop segments (least important first) until it fits.
	candidates := [][]string{
		{app, prof, ver, stale},
		{app, prof, stale},
		{app, stale},
		{app},
	}
	rights := []string{counts, counts, counts, ""}
	inner := w - 2
	var left, right string
	for i, c := range candidates {
		left, right = build(c), rights[i]
		need := lipgloss.Width(left)
		if right != "" {
			need += 1 + lipgloss.Width(right)
		}
		if need <= inner {
			break
		}
	}
	if lipgloss.Width(left)+boolW(right != "", 1+lipgloss.Width(right)) > inner {
		right = ""
		if lipgloss.Width(left) > inner {
			left = ansi.Truncate(left, max(0, inner), "") + ansi.ResetStyle
		}
	}
	gap := max(0, inner-lipgloss.Width(left)-lipgloss.Width(right))
	line := bg.Render(" ") + left + bg.Render(strings.Repeat(" ", gap)) + right + bg.Render(" ")
	if lipgloss.Width(line) > w {
		line = ansi.Truncate(line, w, "") + ansi.ResetStyle
	}
	return line
}

func boolW(b bool, n int) int {
	if b {
		return n
	}
	return 0
}
