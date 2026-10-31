package users

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/tonhe/headsup/internal/api"
	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/store"
	"github.com/tonhe/headsup/internal/ui/humanize"
	"github.com/tonhe/headsup/internal/ui/styles"
	"github.com/tonhe/headsup/internal/ui/table"
)

func columns() []table.Column {
	return []table.Column{
		{Key: "name", Title: "NAME", Width: 14, MinWidth: 8, Flex: true, Priority: 0},
		{Key: "display", Title: "DISPLAY NAME", Width: 16, Flex: true, Priority: 3},
		{Key: "email", Title: "EMAIL", Width: 20, Flex: true, Priority: 4},
		{Key: "provider", Title: "PROVIDER", Width: 8, Priority: 2},
		{Key: "created", Title: "CREATED", Width: 10, Priority: 5},
		{Key: "machines", Title: "MACHINES", Width: 8, Priority: 1, Align: lipgloss.Right},
		{Key: "keys", Title: "KEYS", Width: 4, Priority: 6, Align: lipgloss.Right},
	}
}

// row adapts a user, with derived machine and key counts, to the table and filter.
type row struct {
	u     api.User
	nodes int
	keys  int
	s     *styles.Styles
	now   time.Time
}

func newRows(st *store.Store, ctx module.Context) []row {
	nodes := map[uint64]int{}
	for _, n := range st.Nodes {
		nodes[n.User.ID]++
	}
	keys := map[uint64]int{}
	for _, k := range st.PreAuthKeys {
		if k.User != nil {
			keys[k.User.ID]++
		}
	}
	out := make([]row, 0, len(st.Users))
	for _, u := range st.Users {
		out = append(out, row{u: u, nodes: nodes[u.ID], keys: keys[u.ID], s: ctx.Styles(), now: ctx.Now()})
	}
	return out
}

func (r row) Key() string { return strconv.FormatUint(r.u.ID, 10) }

func (r row) Cell(col string) string {
	u := r.u
	muted := func(s string) string {
		if s == "" {
			return r.s.Muted.Render("-")
		}
		return s
	}
	switch col {
	case "name":
		return u.Name
	case "display":
		return muted(u.DisplayName)
	case "email":
		return muted(u.Email)
	case "provider":
		if u.Provider == "" {
			return r.s.Muted.Render("local")
		}
		return r.s.Accent.Render(u.Provider)
	case "created":
		if u.CreatedAt.IsZero() {
			return r.s.Muted.Render("-")
		}
		return humanize.Ago(u.CreatedAt, r.now)
	case "machines":
		if r.nodes == 0 {
			return r.s.Muted.Render("0")
		}
		return strconv.Itoa(r.nodes)
	case "keys":
		if r.keys == 0 {
			return r.s.Muted.Render("0")
		}
		return strconv.Itoa(r.keys)
	}
	return ""
}

func (r row) SortValue(col string) string {
	switch col {
	case "name":
		return strings.ToLower(r.u.Name)
	case "created":
		return fmt.Sprintf("%010d", r.u.CreatedAt.Unix())
	case "machines":
		return strconv.Itoa(r.nodes)
	case "keys":
		return strconv.Itoa(r.keys)
	case "provider":
		return r.u.Provider
	case "display":
		return strings.ToLower(r.u.DisplayName)
	case "email":
		return strings.ToLower(r.u.Email)
	}
	return ""
}

// FilterText is what free-text search matches: name, display name, email.
func (r row) FilterText() string {
	return strings.ToLower(strings.Join([]string{r.u.Name, r.u.DisplayName, r.u.Email}, " "))
}

func (r row) FilterField(key string) []string {
	switch key {
	case "name", "user":
		return []string{r.u.Name}
	case "email":
		return []string{r.u.Email}
	case "provider":
		if r.u.Provider == "" {
			return []string{"local"}
		}
		return []string{r.u.Provider}
	case "id":
		return []string{r.Key()}
	}
	return nil
}

func (r row) FilterIs(state string) bool {
	switch state {
	case "local":
		return r.u.Provider == ""
	case "oidc":
		return r.u.Provider == "oidc"
	case "empty":
		return r.nodes == 0
	}
	return false
}
