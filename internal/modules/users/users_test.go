package users

import (
	"fmt"
	"strings"
	"testing"

	"github.com/tonhe/headsup/internal/icons"
	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/store"
	"github.com/tonhe/headsup/internal/ui/uitest"
)

func setup(t *testing.T) (*Model, *testCtx) {
	t.Helper()
	return setupIcons(t, icons.Unicode)
}

func setupIcons(t *testing.T, ic icons.Set) (*Model, *testCtx) {
	t.Helper()
	c := newCtx(t)
	c.ic = ic
	m := New()
	m.Init(c)
	m.Update(c, store.UpdatedMsg{Slices: store.SliceAll})
	return m, c
}

func selectUser(t *testing.T, m *Model, c *testCtx, name string) {
	t.Helper()
	for _, u := range c.st.Users {
		if u.Name == name {
			m.Update(c, module.NavigateMsg{Args: module.SelectArgs{ID: u.ID}})
			if got, _ := m.cursor(); got.Name != name {
				t.Fatalf("cursor on %q, want %q", got.Name, name)
			}
			return
		}
	}
	t.Fatalf("no user %q", name)
}

// T098: deleting a user who owns machines is blocked and offers "show machines".
func TestDeleteBlockedWhileUserOwnsMachines(t *testing.T) {
	m, c := setup(t)
	selectUser(t, m, c, "alice")
	if len(c.st.NodesOf("alice")) == 0 {
		t.Fatal("seed: alice should own machines")
	}

	del := action(t, m, "users.delete")
	if del.Mutating || del.Danger != module.DangerNone {
		t.Fatalf("blocked delete must not be a mutating, confirmable action: %+v", del)
	}
	if res := run(t, c, del, nil); res.ActionID != "" {
		t.Fatalf("blocked delete produced a result: %+v", res)
	}
	for _, call := range c.b.Calls() {
		if call.Method == "DeleteUser" {
			t.Fatal("DeleteUser was called for a user who owns machines")
		}
	}
	view := m.View(c, module.Rect{W: 120, H: 30})
	if !strings.Contains(view, "cannot be deleted") || !strings.Contains(view, "show machines") {
		t.Fatalf("view should explain the block and offer show machines:\n%s", view)
	}
	hints := m.Hints()
	if hints[0].Key != "enter" || hints[0].Desc != "show machines" {
		t.Fatalf("hints should lead with show machines, got %+v", hints)
	}

	// enter: show machines navigates to machines filtered by the user.
	show := action(t, m, "users.machines")
	run(t, c, show, nil)
	if len(c.navs) != 1 || c.navs[0].id != "machines" {
		t.Fatalf("navigations = %+v", c.navs)
	}
	if fa, ok := c.navs[0].args.(module.FilterArgs); !ok || fa.Query != "user:alice" {
		t.Fatalf("navigate args = %#v, want FilterArgs{user:alice}", c.navs[0].args)
	}
	if m.blocked != nil {
		t.Fatal("showing machines should dismiss the block notice")
	}
}

func TestBlockedGolden(t *testing.T) {
	blocked := func(t *testing.T, ic icons.Set) (*Model, *testCtx) {
		m, c := setupIcons(t, ic)
		selectUser(t, m, c, "ops")
		run(t, c, action(t, m, "users.delete"), nil)
		return m, c
	}
	uitest.Screen(t, func(t *testing.T, ic icons.Set) string {
		m, c := blocked(t, ic)
		out := m.View(c, module.Rect{W: 98, H: 37})
		if w := uitest.MaxWidth(out); w > 98 {
			t.Fatalf("view is %d wide", w)
		}
		return out
	})
	m, c := blocked(t, icons.Unicode)
	// Moving the cursor away drops the notice.
	m.Update(c, keyMsg("k"))
	if m.blocked != nil {
		t.Fatal("moving off the blocked user should drop the notice")
	}
}

func TestDeleteBlockDismissedByEsc(t *testing.T) {
	m, c := setup(t)
	selectUser(t, m, c, "bob")
	run(t, c, action(t, m, "users.delete"), nil)
	if m.blocked == nil {
		t.Fatal("expected a block notice")
	}
	if m.Capturing() {
		t.Fatal("the block notice must not swallow global keys")
	}
	m.Update(c, keyMsg("esc"))
	if m.blocked != nil {
		t.Fatal("esc should dismiss the notice")
	}
}

func TestDeleteUserWithoutMachines(t *testing.T) {
	m, c := setup(t)
	run(t, c, action(t, m, "users.create"), module.Values{"name": "dave", "display": "Dave", "email": "dave@example.com"})
	c.reload()
	m.Update(c, store.UpdatedMsg{Slices: store.SliceUsers})
	selectUser(t, m, c, "dave")

	del := action(t, m, "users.delete")
	if !del.Mutating || del.Danger != module.DangerCritical {
		t.Fatalf("delete should be a critical mutating action: %+v", del)
	}
	if tg := del.Targets(c); len(tg) != 1 || tg[0].Name != "dave" || tg[0].Kind != "user" {
		t.Fatalf("targets = %+v", tg)
	}
	res := run(t, c, del, nil)
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if res.Refresh&store.SliceUsers == 0 {
		t.Fatal("delete should refresh users")
	}
	c.reload()
	for _, u := range c.st.Users {
		if u.Name == "dave" {
			t.Fatal("dave still exists")
		}
	}
}

func TestCreateAndRename(t *testing.T) {
	m, c := setup(t)
	create := action(t, m, "users.create")
	spec := create.Params(c)
	if len(spec.Fields) != 3 || spec.Fields[0].ID != "name" || !spec.Fields[0].Required {
		t.Fatalf("create form = %+v", spec)
	}
	res := run(t, c, create, module.Values{"name": "erin"})
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	c.reload()
	m.Update(c, store.UpdatedMsg{Slices: store.SliceUsers})
	selectUser(t, m, c, "erin")

	rename := action(t, m, "users.rename")
	if ok, _ := rename.Enabled(c); !ok {
		t.Fatal("rename should be enabled with a cursor")
	}
	if f := rename.Params(c).Fields[0]; f.Value != "erin" {
		t.Fatalf("rename default = %q", f.Value)
	}
	res = run(t, c, rename, module.Values{"name": "erin2"})
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if res.Refresh&store.SliceNodes == 0 {
		t.Fatal("rename should refresh nodes (they embed the owner name)")
	}
	c.reload()
	if len(c.st.Users) == 0 || !hasUser(c, "erin2") || hasUser(c, "erin") {
		t.Fatalf("users after rename: %+v", c.st.Users)
	}

	// Duplicate names surface the backend's conflict.
	if res := run(t, c, create, module.Values{"name": "alice"}); res.Err == nil {
		t.Fatal("creating a duplicate user should fail")
	}
}

func hasUser(c *testCtx, name string) bool {
	for _, u := range c.st.Users {
		if u.Name == name {
			return true
		}
	}
	return false
}

func TestShowKeysNavigates(t *testing.T) {
	m, c := setup(t)
	selectUser(t, m, c, "ops")
	run(t, c, action(t, m, "users.keys"), nil)
	if len(c.navs) != 1 || c.navs[0].id != "keys" {
		t.Fatalf("navs = %+v", c.navs)
	}
	if fa := c.navs[0].args.(module.FilterArgs); fa.Query != "user:ops" {
		t.Fatalf("query = %q", fa.Query)
	}
}

func TestUserQuery(t *testing.T) {
	tests := []struct{ name, want string }{
		{"alice", "user:alice"},
		{"alice@example.com", "user:alice@example.com"},
		{"two words", `user:"two words"`},
		{`q"uote`, `user:"q\"uote"`},
	}
	for _, tt := range tests {
		if got := UserQuery(tt.name); got != tt.want {
			t.Errorf("UserQuery(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestValidateName(t *testing.T) {
	tests := []struct {
		in string
		ok bool
	}{
		{"alice", true},
		{"alice.smith", true},
		{"a_b-c", true},
		{"alice@example.com", true},
		{"", false},
		{"a", false},
		{"1alice", false},
		{"al ice", false},
		{"a@b@c", false},
		{"al!ce", false},
	}
	for _, tt := range tests {
		if err := ValidateName(tt.in); (err == nil) != tt.ok {
			t.Errorf("ValidateName(%q) = %v, want ok=%v", tt.in, err, tt.ok)
		}
	}
}

func TestFilterAndCounts(t *testing.T) {
	m, c := setup(t)
	m.Update(c, module.NavigateMsg{Args: module.FilterArgs{Query: "provider:oidc"}})
	if len(m.rows) != 2 {
		t.Fatalf("oidc users = %d, want 2", len(m.rows))
	}
	m.Update(c, module.NavigateMsg{Args: module.FilterArgs{Query: "is:local"}})
	for _, r := range m.rows {
		if r.u.Provider != "" {
			t.Fatalf("is:local matched %s", r.u.Name)
		}
	}
	m.Update(c, module.NavigateMsg{Args: module.FilterArgs{Query: ""}})
	for _, r := range m.rows {
		if r.u.Name == "ops" {
			if r.nodes != len(c.st.NodesOf("ops")) || r.keys == 0 {
				t.Fatalf("ops counts nodes=%d keys=%d", r.nodes, r.keys)
			}
		}
	}
	// esc clears an applied filter.
	m.Update(c, module.NavigateMsg{Args: module.FilterArgs{Query: "alice"}})
	m.Update(c, keyMsg("esc"))
	if m.bar.Value() != "" {
		t.Fatal("esc should clear the filter")
	}
}

func TestViewGolden(t *testing.T) {
	for _, sz := range [][2]int{{98, 37}, {58, 21}, {140, 37}} {
		t.Run(sizeName(sz), func(t *testing.T) {
			uitest.Screen(t, func(t *testing.T, ic icons.Set) string {
				m, c := setupIcons(t, ic)
				selectUser(t, m, c, "alice")
				out := m.View(c, module.Rect{W: sz[0], H: sz[1]})
				if w := uitest.MaxWidth(out); w > sz[0] {
					t.Fatalf("view is %d wide, want <= %d", w, sz[0])
				}
				if h := len(uitest.Lines(out)); h > sz[1] {
					t.Fatalf("view is %d tall, want <= %d", h, sz[1])
				}
				return out
			})
		})
	}
}

func sizeName(sz [2]int) string { return fmt.Sprintf("%dx%d", sz[0], sz[1]) }

func TestEmptyAndLoading(t *testing.T) {
	c := newCtx(t)
	m := New()
	m.Init(c)
	if v := m.View(c, module.Rect{W: 80, H: 20}); !strings.Contains(v, "Loading") {
		t.Fatalf("before the first fetch the view should say loading:\n%s", v)
	}
	if m.Badge(c.st) != "" {
		t.Fatal("users has no badge")
	}
}
