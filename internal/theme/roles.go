package theme

import (
	"math"

	"github.com/charmbracelet/lipgloss"
	"github.com/lucasb-eyer/go-colorful"
)

// Roles maps Base16 slots to the semantic colours the UI uses. UI code reads
// roles, never Base16 slots, so a theme file can retune meaning (e.g. accent)
// without touching syntax colours.
type Roles struct {
	Bg, Panel, Selection        lipgloss.Color
	Fg, FgStrong, Muted, Border lipgloss.Color
	BorderFocus, Accent         lipgloss.Color
	Online, Offline, Idle       lipgloss.Color
	Expiring, Expired           lipgloss.Color
	Pending, Warn, Danger, OK   lipgloss.Color
	TagFg, TagBg                lipgloss.Color
	IP, Link                    lipgloss.Color
	Spark                       [8]lipgloss.Color
}

// RoleOverrides lets a user theme replace individual roles. Keys are role
// names (snake_case, e.g. "accent", "tag_bg"); values are hex or a base key.
type RoleOverrides map[string]string

// Roles derives semantic roles from the palette.
func (t Theme) Roles() Roles {
	r := Roles{
		Bg:          t.Base00,
		Panel:       t.Base01,
		Selection:   t.Base02,
		Fg:          t.Base05,
		FgStrong:    t.Base07,
		Muted:       t.Base03,
		Border:      t.Base03,
		BorderFocus: t.Base0D,
		Accent:      t.Base0D,
		Online:      t.Base0B,
		Offline:     t.Base08,
		Idle:        t.Base04,
		Expiring:    t.Base09,
		Expired:     t.Base08,
		Pending:     t.Base0A,
		Warn:        t.Base0A,
		Danger:      t.Base08,
		OK:          t.Base0B,
		TagFg:       t.Base0E,
		TagBg:       blend(t.Base00, t.Base0E, 0.18),
		IP:          t.Base0C,
		Link:        t.Base0C,
		Spark:       t.SparkGradient(),
	}
	if t.Light {
		// Base03 is often too faint on light backgrounds for help text.
		r.Muted = t.Base04
	}
	for k, v := range t.Overrides {
		c := t.resolve(v)
		if c == "" {
			continue
		}
		if p := r.slot(k); p != nil {
			*p = c
		}
	}
	return r
}

func (r *Roles) slot(name string) *lipgloss.Color {
	switch name {
	case "bg":
		return &r.Bg
	case "panel":
		return &r.Panel
	case "selection":
		return &r.Selection
	case "fg":
		return &r.Fg
	case "fg_strong":
		return &r.FgStrong
	case "muted":
		return &r.Muted
	case "border":
		return &r.Border
	case "border_focus":
		return &r.BorderFocus
	case "accent":
		return &r.Accent
	case "online":
		return &r.Online
	case "offline":
		return &r.Offline
	case "idle":
		return &r.Idle
	case "expiring":
		return &r.Expiring
	case "expired":
		return &r.Expired
	case "pending":
		return &r.Pending
	case "warn":
		return &r.Warn
	case "danger":
		return &r.Danger
	case "ok":
		return &r.OK
	case "tag_fg":
		return &r.TagFg
	case "tag_bg":
		return &r.TagBg
	case "ip":
		return &r.IP
	case "link":
		return &r.Link
	}
	return nil
}

// resolve turns "base0E" or "#rrggbb" into a colour; "" if invalid.
func (t Theme) resolve(v string) lipgloss.Color {
	if len(v) == 7 && v[0] == '#' {
		if _, err := colorful.Hex(v); err == nil {
			return lipgloss.Color(v)
		}
		return ""
	}
	if c, ok := t.base(v); ok {
		return c
	}
	return ""
}

func (t Theme) base(key string) (lipgloss.Color, bool) {
	switch normaliseBaseKey(key) {
	case "base00":
		return t.Base00, true
	case "base01":
		return t.Base01, true
	case "base02":
		return t.Base02, true
	case "base03":
		return t.Base03, true
	case "base04":
		return t.Base04, true
	case "base05":
		return t.Base05, true
	case "base06":
		return t.Base06, true
	case "base07":
		return t.Base07, true
	case "base08":
		return t.Base08, true
	case "base09":
		return t.Base09, true
	case "base0a":
		return t.Base0A, true
	case "base0b":
		return t.Base0B, true
	case "base0c":
		return t.Base0C, true
	case "base0d":
		return t.Base0D, true
	case "base0e":
		return t.Base0E, true
	case "base0f":
		return t.Base0F, true
	}
	return "", false
}

func normaliseBaseKey(k string) string {
	b := []byte(k)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

// blend mixes a toward b by frac in HCL space.
func blend(a, b lipgloss.Color, frac float64) lipgloss.Color {
	ca, err1 := colorful.Hex(string(a))
	cb, err2 := colorful.Hex(string(b))
	if err1 != nil || err2 != nil {
		return a
	}
	return lipgloss.Color(colorfulToHex(ca.BlendHcl(cb, frac).Clamped()))
}

// Contrast returns the WCAG contrast ratio between two hex colours.
func Contrast(a, b lipgloss.Color) float64 {
	ca, err1 := colorful.Hex(string(a))
	cb, err2 := colorful.Hex(string(b))
	if err1 != nil || err2 != nil {
		return 0
	}
	la, lb := luminance(ca), luminance(cb)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func luminance(c colorful.Color) float64 {
	lin := func(v float64) float64 {
		if v <= 0.03928 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c.R) + 0.7152*lin(c.G) + 0.0722*lin(c.B)
}

// Register adds or replaces a theme (used for user themes). Returns true if
// it replaced an existing slug.
func Register(t Theme) bool {
	_, existed := themes[t.Slug]
	themes[t.Slug] = t
	if existed {
		for i := range themeOrder {
			if themeOrder[i].Slug == t.Slug {
				themeOrder[i] = t
			}
		}
	} else {
		themeOrder = append(themeOrder, t)
	}
	return existed
}

// Default is the slug used when no theme is configured.
const Default = "tokyo-night"
