package keys

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/tonhe/headsup/internal/api"
	"github.com/tonhe/headsup/internal/icons"
	"github.com/tonhe/headsup/internal/ui/badge"
	"github.com/tonhe/headsup/internal/ui/humanize"
	"github.com/tonhe/headsup/internal/ui/styles"
	"github.com/tonhe/headsup/internal/ui/table"
)

// band is a step of the "expires in" colour ramp.
type band int

const (
	bandNever    band = iota // no expiry
	bandFine                 // more than 30 days
	bandNear                 // within 30 days
	bandSoon                 // within ExpiryWarn (7 days)
	bandCritical             // within a day
	bandExpired
)

func (b band) String() string {
	return [...]string{"never", "fine", "near", "soon", "critical", "expired"}[b]
}

func expiredAt(exp, now time.Time) bool { return !exp.IsZero() && !exp.After(now) }

// rampBand places an expiry on the colour ramp (T120).
func rampBand(exp, now time.Time) band {
	if exp.IsZero() {
		return bandNever
	}
	d := exp.Sub(now)
	switch {
	case d <= 0:
		return bandExpired
	case d < 24*time.Hour:
		return bandCritical
	case d < ExpiryWarn:
		return bandSoon
	case d < 30*24*time.Hour:
		return bandNear
	}
	return bandFine
}

func rampStyle(s *styles.Styles, b band) lipgloss.Style {
	switch b {
	case bandExpired:
		return s.Expired
	case bandCritical:
		return s.Danger
	case bandSoon:
		return s.Expiring
	case bandNear:
		return s.Warn
	case bandNever:
		return s.Muted
	}
	return s.Text
}

// expiryCell renders "expires in" on the ramp.
func expiryCell(s *styles.Styles, exp, now time.Time) string {
	b := rampBand(exp, now)
	text := "never"
	switch b {
	case bandExpired:
		text = humanize.Ago(exp, now)
	case bandNever:
	default:
		text = humanize.Short(exp.Sub(now))
	}
	return rampStyle(s, b).Render(text)
}

func expirySort(exp time.Time) string {
	if exp.IsZero() {
		return "9999999999"
	}
	return fmt.Sprintf("%010d", exp.Unix())
}

func preAuthColumns() []table.Column {
	return []table.Column{
		{Key: "status", Title: "", Width: 1},
		{Key: "key", Title: "KEY", Width: 18, Priority: 0},
		{Key: "user", Title: "USER", Width: 10, Priority: 0},
		{Key: "flags", Title: "FLAGS", Width: 14, Priority: 4},
		{Key: "state", Title: "STATE", Width: 7, Priority: 1},
		{Key: "tags", Title: "TAGS", Width: 10, Flex: true, Priority: 3},
		{Key: "created", Title: "CREATED", Width: 10, Priority: 5},
		{Key: "expires", Title: "EXPIRES", Width: 12, Priority: 2},
	}
}

func apiColumns() []table.Column {
	return []table.Column{
		{Key: "status", Title: "", Width: 1},
		{Key: "prefix", Title: "PREFIX", Width: 16, MinWidth: 10, Flex: true, Priority: 0},
		{Key: "created", Title: "CREATED", Width: 10, Priority: 3},
		{Key: "expires", Title: "EXPIRES", Width: 12, Priority: 1},
		{Key: "seen", Title: "LAST USED", Width: 9, Priority: 2},
		{Key: "note", Title: "", Width: 10, Priority: 0},
	}
}

// preRow adapts a pre-auth key to the table and filter.
type preRow struct {
	k   api.PreAuthKey
	s   *styles.Styles
	ic  icons.Set
	now time.Time
}

func (r preRow) Key() string { return strconv.FormatUint(r.k.ID, 10) }

func (r preRow) state() api.KeyState {
	if expiredAt(r.k.Expiration, r.now) {
		return api.KeyExpired
	}
	return r.k.State(r.now)
}

func (r preRow) owner() string {
	if r.k.User == nil {
		return ""
	}
	return r.k.User.Name
}

func (r preRow) Cell(col string) string {
	k, s := r.k, r.s
	st := s.OK
	switch r.state() {
	case api.KeyUsed:
		st = s.Muted
	case api.KeyExpired:
		st = s.Expired
	}
	switch col {
	case "status":
		return st.Render(r.ic.Key)
	case "key":
		return k.Key.Masked()
	case "user":
		if o := r.owner(); o != "" {
			return o
		}
		return s.Muted.Render("tag-only")
	case "flags":
		var f []string
		if k.Reusable {
			f = append(f, "reusable")
		}
		if k.Ephemeral {
			f = append(f, "ephemeral")
		}
		if k.Used {
			f = append(f, "used")
		}
		if len(f) == 0 {
			return s.Muted.Render("-")
		}
		return strings.Join(f, ",")
	case "state":
		return st.Render(r.state().String())
	case "tags":
		if len(k.ACLTags) == 0 {
			return s.Muted.Render("-")
		}
		return badge.Tags(s, k.ACLTags, 30)
	case "created":
		return humanize.Ago(k.CreatedAt, r.now)
	case "expires":
		return expiryCell(s, k.Expiration, r.now)
	}
	return ""
}

func (r preRow) SortValue(col string) string {
	switch col {
	case "created":
		return fmt.Sprintf("%010d", r.k.CreatedAt.Unix())
	case "expires":
		return expirySort(r.k.Expiration)
	case "user":
		return r.owner()
	case "state":
		return strconv.Itoa(int(r.state()))
	case "key":
		return r.k.Key.Masked()
	}
	return ""
}

func (r preRow) FilterText() string {
	return strings.ToLower(strings.Join(append([]string{r.k.Key.Masked(), r.owner()}, r.k.ACLTags...), " "))
}

func (r preRow) FilterField(key string) []string {
	switch key {
	case "user":
		return []string{r.owner()}
	case "tag":
		return r.k.ACLTags
	case "id":
		return []string{r.Key()}
	}
	return nil
}

func (r preRow) FilterIs(state string) bool {
	switch state {
	case "active":
		return r.state() == api.KeyActive
	case "used":
		return r.k.Used
	case "expired":
		return r.state() == api.KeyExpired
	case "reusable":
		return r.k.Reusable
	case "ephemeral":
		return r.k.Ephemeral
	case "expiring":
		b := rampBand(r.k.Expiration, r.now)
		return r.state() == api.KeyActive && (b == bandSoon || b == bandCritical)
	case "tagonly":
		return r.k.User == nil
	}
	return false
}

// apiRow adapts an API key, addressed by prefix.
type apiRow struct {
	k    api.APIKey
	s    *styles.Styles
	ic   icons.Set
	now  time.Time
	self bool
}

func (r apiRow) Key() string { return r.k.Prefix }

func (r apiRow) Cell(col string) string {
	k, s := r.k, r.s
	switch col {
	case "status":
		if expiredAt(k.Expiration, r.now) {
			return s.Expired.Render(r.ic.Lock)
		}
		return s.OK.Render(r.ic.Lock)
	case "prefix":
		return k.Prefix
	case "created":
		return humanize.Ago(k.CreatedAt, r.now)
	case "expires":
		return expiryCell(s, k.Expiration, r.now)
	case "seen":
		if k.LastSeen.IsZero() {
			return s.Muted.Render("never")
		}
		return humanize.Ago(k.LastSeen, r.now)
	case "note":
		if r.self {
			return s.Accent.Render(r.ic.Arrow + " this app")
		}
	}
	return ""
}

func (r apiRow) SortValue(col string) string {
	switch col {
	case "created":
		return fmt.Sprintf("%010d", r.k.CreatedAt.Unix())
	case "expires":
		return expirySort(r.k.Expiration)
	case "seen":
		return fmt.Sprintf("%010d", r.k.LastSeen.Unix())
	}
	return r.k.Prefix
}

func (r apiRow) FilterText() string { return strings.ToLower(r.k.Prefix) }

func (r apiRow) FilterField(key string) []string {
	if key == "prefix" {
		return []string{r.k.Prefix}
	}
	return nil
}

func (r apiRow) FilterIs(state string) bool {
	switch state {
	case "expired":
		return expiredAt(r.k.Expiration, r.now)
	case "active":
		return !expiredAt(r.k.Expiration, r.now)
	case "self":
		return r.self
	}
	return false
}
