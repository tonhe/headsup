// Package styles builds every lipgloss style the UI uses from the active
// theme's semantic roles. UI code takes styles from here and never constructs
// colours itself.
package styles

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/tonhe/headsup/internal/theme"
)

// Styles is rebuilt whenever the theme changes.
type Styles struct {
	Theme theme.Theme
	R     theme.Roles

	App         lipgloss.Style
	Header      lipgloss.Style
	HeaderTitle lipgloss.Style
	HeaderDim   lipgloss.Style
	Footer      lipgloss.Style
	FooterKey   lipgloss.Style
	FooterDesc  lipgloss.Style

	Sidebar       lipgloss.Style
	SidebarItem   lipgloss.Style
	SidebarActive lipgloss.Style
	SidebarDim    lipgloss.Style
	SidebarHead   lipgloss.Style // section header ("1 TAILNET")
	SidebarJump   lipgloss.Style // section header while a section jump waits for its second key
	JumpKey       lipgloss.Style // a page's key during a section jump
	Badge         lipgloss.Style

	Pane        lipgloss.Style
	PaneFocus   lipgloss.Style
	PaneTitle   lipgloss.Style
	PaneTitleFo lipgloss.Style

	TableHeader lipgloss.Style
	Row         lipgloss.Style
	RowSel      lipgloss.Style
	RowCursor   lipgloss.Style
	Cell        lipgloss.Style

	Text, Strong, Muted, Accent lipgloss.Style
	OK, Warn, Danger, Pending   lipgloss.Style
	Online, Idle, Offline       lipgloss.Style
	Expiring, Expired           lipgloss.Style
	IP, Link                    lipgloss.Style
	Tag                         lipgloss.Style

	Modal       lipgloss.Style
	ModalTitle  lipgloss.Style
	ModalDanger lipgloss.Style
	Label       lipgloss.Style
	Input       lipgloss.Style
	InputFocus  lipgloss.Style
	Button      lipgloss.Style
	ButtonFo    lipgloss.Style

	Toast     map[int]lipgloss.Style
	Highlight lipgloss.Style
	Spark     [8]lipgloss.Style
}

// New builds styles from t.
func New(t theme.Theme) *Styles {
	r := t.Roles()
	fg := lipgloss.NewStyle().Foreground
	s := &Styles{Theme: t, R: r}

	s.App = lipgloss.NewStyle().Foreground(r.Fg)
	s.Header = lipgloss.NewStyle().Foreground(r.Fg).Background(r.Panel).Padding(0, 1)
	s.HeaderTitle = lipgloss.NewStyle().Foreground(r.Accent).Background(r.Panel).Bold(true)
	s.HeaderDim = lipgloss.NewStyle().Foreground(r.Muted).Background(r.Panel)
	s.Footer = lipgloss.NewStyle().Foreground(r.Muted).Padding(0, 1)
	s.FooterKey = lipgloss.NewStyle().Foreground(r.Accent).Bold(true)
	s.FooterDesc = fg(r.Muted)

	s.Sidebar = lipgloss.NewStyle().Padding(0, 1)
	s.SidebarItem = fg(r.Fg)
	s.SidebarActive = lipgloss.NewStyle().Foreground(r.Bg).Background(r.Accent).Bold(true)
	s.SidebarDim = fg(r.Muted)
	s.SidebarHead = lipgloss.NewStyle().Foreground(r.Muted).Bold(true)
	s.SidebarJump = lipgloss.NewStyle().Foreground(r.Bg).Background(r.Pending).Bold(true)
	s.JumpKey = lipgloss.NewStyle().Foreground(r.Pending).Bold(true)
	s.Badge = lipgloss.NewStyle().Foreground(r.Bg).Background(r.Pending).Padding(0, 1)

	s.Pane = lipgloss.NewStyle().Foreground(r.Border)
	s.PaneFocus = lipgloss.NewStyle().Foreground(r.BorderFocus)
	s.PaneTitle = lipgloss.NewStyle().Foreground(r.Fg).Bold(true)
	s.PaneTitleFo = lipgloss.NewStyle().Foreground(r.BorderFocus).Bold(true)

	s.TableHeader = lipgloss.NewStyle().Foreground(r.Muted).Bold(true)
	s.Row = fg(r.Fg)
	s.RowSel = lipgloss.NewStyle().Foreground(r.FgStrong).Background(r.Selection)
	s.RowCursor = lipgloss.NewStyle().Foreground(r.Accent).Bold(true)
	s.Cell = fg(r.Fg)

	s.Text = fg(r.Fg)
	s.Strong = lipgloss.NewStyle().Foreground(r.FgStrong).Bold(true)
	s.Muted = fg(r.Muted)
	s.Accent = fg(r.Accent)
	s.OK = fg(r.OK)
	s.Warn = fg(r.Warn)
	s.Danger = fg(r.Danger)
	s.Pending = fg(r.Pending)
	s.Online = fg(r.Online)
	s.Idle = fg(r.Idle)
	s.Offline = fg(r.Offline)
	s.Expiring = fg(r.Expiring)
	s.Expired = fg(r.Expired)
	s.IP = fg(r.IP)
	s.Link = lipgloss.NewStyle().Foreground(r.Link).Underline(true)
	s.Tag = lipgloss.NewStyle().Foreground(r.TagFg).Background(r.TagBg)

	s.Modal = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(r.BorderFocus).Padding(1, 2)
	s.ModalTitle = lipgloss.NewStyle().Foreground(r.Accent).Bold(true)
	s.ModalDanger = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(r.Danger).Padding(1, 2)
	s.Label = fg(r.Muted)
	s.Input = lipgloss.NewStyle().Foreground(r.Fg)
	s.InputFocus = lipgloss.NewStyle().Foreground(r.FgStrong)
	s.Button = lipgloss.NewStyle().Foreground(r.Fg).Background(r.Selection).Padding(0, 2)
	s.ButtonFo = lipgloss.NewStyle().Foreground(r.Bg).Background(r.Accent).Bold(true).Padding(0, 2)

	s.Toast = map[int]lipgloss.Style{
		0: lipgloss.NewStyle().Foreground(r.Fg).Border(lipgloss.RoundedBorder()).BorderForeground(r.Accent).Padding(0, 1),
		1: lipgloss.NewStyle().Foreground(r.Fg).Border(lipgloss.RoundedBorder()).BorderForeground(r.OK).Padding(0, 1),
		2: lipgloss.NewStyle().Foreground(r.Fg).Border(lipgloss.RoundedBorder()).BorderForeground(r.Warn).Padding(0, 1),
		3: lipgloss.NewStyle().Foreground(r.Fg).Border(lipgloss.RoundedBorder()).BorderForeground(r.Danger).Padding(0, 1),
	}
	s.Highlight = lipgloss.NewStyle().Foreground(r.Accent).Bold(true)
	for i, c := range r.Spark {
		s.Spark[i] = fg(c)
	}
	return s
}
