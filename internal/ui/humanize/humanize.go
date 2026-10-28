// Package humanize formats times and durations for compact table cells.
package humanize

import (
	"fmt"
	"time"
)

// Ago renders t relative to now: "now", "42s ago", "5m ago", "3h ago", "2d ago".
// A zero time renders as "never".
func Ago(t, now time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := now.Sub(t)
	if d < 0 {
		return In(t, now)
	}
	if d < 5*time.Second {
		return "now"
	}
	return Short(d) + " ago"
}

// In renders a future time relative to now: "in 5m", "in 3d". Past times use Ago.
func In(t, now time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := t.Sub(now)
	if d < 0 {
		return Ago(t, now)
	}
	return "in " + Short(d)
}

// Short renders a duration with one unit: 42s, 5m, 3h, 2d, 6w, 2y.
func Short(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 14*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dw", int(d.Hours()/(24*7)))
	default:
		return fmt.Sprintf("%dy", int(d.Hours()/(24*365)))
	}
}
