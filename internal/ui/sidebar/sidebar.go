// Package sidebar renders the module navigation list: pages grouped under
// section headers ("1 TAILNET"), each page with its direct chord ("g m")
// dimmed at the right edge. While a section jump waits for its second key,
// that section's header is highlighted and each of its pages shows the key
// that picks it.
package sidebar

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/tonhe/headsup/internal/icons"
	"github.com/tonhe/headsup/internal/ui/styles"
)

// Item is one navigation entry. Key is the page's key within its section
// jump ("m"), shown only while that jump is pending; Chord is its direct
// chord ("g m"), shown dimmed when the row has room. Badge is a short count
// or state shown right-aligned; Dim marks unavailable modules.
type Item struct {
	ID, Title, Icon, Badge string
	Dim                    bool
	Key                    string
	Chord                  string
}

// Group is a titled section of items. Key is the digit that starts its jump.
type Group struct {
	Key, Title string
	Items      []Item
}

// Row says what one sidebar line shows: a group header (Item -1), an item,
// or a blank spacer (Group -1).
type Row struct{ Group, Item int }

// Rows lays out groups as lines: a header, its items, and a blank line
// between groups. Empty groups are skipped. Render draws exactly these rows,
// so a mouse click can be mapped back with Rows()[y].
func Rows(groups []Group) []Row {
	var out []Row
	for g, gr := range groups {
		if len(gr.Items) == 0 {
			continue
		}
		if len(out) > 0 {
			out = append(out, Row{Group: -1, Item: -1})
		}
		out = append(out, Row{Group: g, Item: -1})
		for i := range gr.Items {
			out = append(out, Row{Group: g, Item: i})
		}
	}
	return out
}

// Render draws groups as exactly w x h. The active item is highlighted;
// jump is the index of the group whose jump is pending, or -1.
func Render(s *styles.Styles, ic icons.Set, groups []Group, active string, jump, w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	blank := strings.Repeat(" ", w)
	lines := make([]string, 0, h)
	for _, r := range Rows(groups) {
		if len(lines) == h {
			break
		}
		switch {
		case r.Group < 0:
			lines = append(lines, blank)
		case r.Item < 0:
			lines = append(lines, header(s, groups[r.Group], r.Group == jump, w))
		default:
			it := groups[r.Group].Items[r.Item]
			mode := rowNormal
			if jump >= 0 {
				mode = rowFaded
				if r.Group == jump {
					mode = rowJump
				}
			}
			lines = append(lines, line(s, ic, it, it.ID == active, mode, w))
		}
	}
	for len(lines) < h {
		lines = append(lines, blank)
	}
	return strings.Join(lines, "\n")
}

func header(s *styles.Styles, g Group, jump bool, w int) string {
	txt := " " + strings.TrimSpace(g.Key+" "+strings.ToUpper(g.Title))
	if jump {
		if hint := "pick one "; lipgloss.Width(txt)+1+lipgloss.Width(hint) <= w {
			txt += strings.Repeat(" ", w-lipgloss.Width(txt)-lipgloss.Width(hint)) + hint
		}
		return s.SidebarJump.Render(fit(txt, w))
	}
	return fit(s.SidebarHead.Render(fit(txt, min(w, lipgloss.Width(txt)))), w)
}

type rowMode int

const (
	rowNormal rowMode = iota
	rowFaded          // another section's jump is pending
	rowJump           // this section's jump is pending: show the page keys
)

func line(s *styles.Styles, ic icons.Set, it Item, act bool, mode rowMode, w int) string {
	marker := " "
	if act {
		marker = ic.Arrow
	}
	left := marker + " "
	keyCol := ""
	if mode == rowJump {
		// The key takes the icon's place so titles stay aligned.
		k := it.Key
		if k == "" {
			k = " "
		}
		keyCol = k + " "
	} else if it.Icon != "" {
		left += it.Icon + " "
	}
	title := it.Title
	badge := it.Badge
	bw := 0
	if badge != "" {
		bw = lipgloss.Width(badge) + 2
	}
	room := w - 1 // trailing space
	leftW := lipgloss.Width(left) + lipgloss.Width(keyCol) + lipgloss.Width(title)
	if bw > 0 && leftW+1+bw > room {
		if lipgloss.Width(marker+" "+title)+1+bw > room {
			badge, bw = "", 0
		}
	}
	chord := ""
	if mode == rowNormal && it.Chord != "" {
		need := leftW + 1 + lipgloss.Width(it.Chord)
		if bw > 0 {
			need += bw + 1
		}
		if need <= room {
			chord = it.Chord
		}
	}
	cw := 0
	if chord != "" {
		cw = lipgloss.Width(chord) + 1
	}
	tw := room - bw - cw - lipgloss.Width(left) - lipgloss.Width(keyCol)
	if bw > 0 {
		tw--
	}
	if lipgloss.Width(title) > tw {
		title = ansi.Truncate(title, max(0, tw), ic.Ellipsis)
		if lipgloss.Width(title) > tw {
			title = ansi.Truncate(title, max(0, tw), "")
		}
	}
	plainLeft := left + keyCol + title
	gap := max(0, w-lipgloss.Width(plainLeft)-bw-cw)
	if badge != "" && chord != "" {
		gap = max(0, gap-1) // the space between the badge and the chord
	}
	if act {
		txt := plainLeft + strings.Repeat(" ", gap)
		if badge != "" {
			txt += " " + badge + " "
			if chord != "" {
				txt += " "
			}
		}
		if chord != "" {
			txt += chord + " "
		}
		return s.SidebarActive.Render(fit(txt, w))
	}
	st := s.SidebarItem
	if it.Dim || mode == rowFaded {
		st = s.SidebarDim
	}
	out := st.Render(left)
	if keyCol != "" {
		out += s.JumpKey.Render(strings.TrimSuffix(keyCol, " ")) + " "
	}
	out += st.Render(title) + strings.Repeat(" ", gap)
	if badge != "" {
		out += s.Badge.Render(badge)
		if chord != "" {
			out += " "
		}
	}
	if chord != "" {
		out += s.Muted.Render(chord) + " "
	}
	return fit(out, w)
}

func fit(s string, w int) string {
	if lipgloss.Width(s) > w {
		s = ansi.Truncate(s, w, "") + ansi.ResetStyle
	}
	if d := w - lipgloss.Width(s); d > 0 {
		s += strings.Repeat(" ", d)
	}
	return s
}
