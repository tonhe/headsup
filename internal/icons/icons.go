// Package icons provides glyph sets so the UI works with Unicode, Nerd Fonts,
// or plain ASCII terminals.
package icons

// Set is a named collection of glyphs.
type Set struct {
	Name string

	Online, Idle, Offline, Expired  string
	Check, Cross, Pending, Warn     string
	Exit, Route, Tag, User, Key     string
	Machine, Policy, Server, DNS    string
	DERP, Service, Log, Activity    string
	Dashboard, Settings, Lock       string
	Bullet, Arrow, Select, Ellipsis string
	SortAsc, SortDesc               string
	Primary                         string
}

var Unicode = Set{
	Name:   "unicode",
	Online: "●", Idle: "◐", Offline: "○", Expired: "⊘",
	Check: "✓", Cross: "✗", Pending: "◌", Warn: "▲",
	Exit: "⇱", Route: "⇄", Tag: "#", User: "◆", Key: "⚿",
	Machine: "▣", Policy: "§", Server: "◎", DNS: "≡",
	DERP: "⇋", Service: "⏻", Log: "☰", Activity: "↻",
	Dashboard: "◫", Settings: "⚙", Lock: "⊡",
	Bullet: "•", Arrow: "▸", Select: "▌", Ellipsis: "…",
	SortAsc: "▲", SortDesc: "▼",
	Primary: "★",
}

var Nerd = Set{
	Name:   "nerd",
	Online: "", Idle: "", Offline: "", Expired: "",
	Check: "", Cross: "", Pending: "", Warn: "",
	Exit: "", Route: "", Tag: "", User: "", Key: "",
	Machine: "", Policy: "", Server: "", DNS: "",
	DERP: "", Service: "", Log: "", Activity: "",
	Dashboard: "", Settings: "", Lock: "",
	Bullet: "", Arrow: "", Select: "▌", Ellipsis: "…",
	SortAsc: "", SortDesc: "",
	Primary: "",
}

var ASCII = Set{
	Name:   "ascii",
	Online: "*", Idle: "~", Offline: "o", Expired: "x",
	Check: "+", Cross: "x", Pending: "?", Warn: "!",
	Exit: "E", Route: "R", Tag: "#", User: "@", Key: "K",
	Machine: "M", Policy: "P", Server: "S", DNS: "D",
	DERP: "d", Service: "s", Log: "L", Activity: "A",
	Dashboard: "H", Settings: "%", Lock: "L",
	Bullet: "-", Arrow: ">", Select: ">", Ellipsis: "...",
	SortAsc: "^", SortDesc: "v",
	Primary: "*",
}

// ByName returns the named set, defaulting to Unicode.
func ByName(name string) Set {
	switch name {
	case "nerd":
		return Nerd
	case "ascii":
		return ASCII
	default:
		return Unicode
	}
}
