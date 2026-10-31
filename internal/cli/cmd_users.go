package cli

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/tonhe/headsup/internal/api"
	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/store"
)

func resolveUser(st *store.Store, arg string) (api.User, error) {
	if id, err := strconv.ParseUint(arg, 10, 64); err == nil {
		for _, u := range st.Users {
			if u.ID == id {
				return u, nil
			}
		}
	}
	var hits []api.User
	for _, u := range st.Users {
		if u.Name == arg || strings.EqualFold(u.Email, arg) {
			hits = append(hits, u)
		}
	}
	switch len(hits) {
	case 0:
		return api.User{}, errorf(ExitNotFound, "no user %q", arg)
	case 1:
		return hits[0], nil
	}
	var c []string
	for _, u := range hits {
		c = append(c, fmt.Sprintf("%d (%s)", u.ID, u.Name))
	}
	return api.User{}, usagef("%q matches %d users; use an ID: %s", arg, len(hits), strings.Join(c, "; "))
}

func userDTO(u api.User, st *store.Store) userOut {
	return userOut{ID: u.ID, Name: u.Name, DisplayName: u.DisplayName, Email: u.Email, Provider: u.Provider,
		CreatedAt: ts(u.CreatedAt), Nodes: len(st.NodesOf(u.Name))}
}

func usersTable(us []userOut) func(bool) table {
	return func(wide bool) table {
		t := table{head: []string{"ID", "NAME", "DISPLAY NAME", "EMAIL", "NODES"}}
		if wide {
			t.head = append(t.head, "PROVIDER", "CREATED")
		}
		for _, u := range us {
			row := []string{idStr(u.ID), u.Name, u.DisplayName, u.Email, strconv.Itoa(u.Nodes)}
			if wide {
				created := ""
				if u.CreatedAt != nil {
					created = *u.CreatedAt
				}
				row = append(row, u.Provider, created)
			}
			t.add(row...)
		}
		return t
	}
}

func usersLs(r *runner, args []string) error {
	v := r.newFlags("users ls", fmtTable)
	if _, err := v.args(args, 0, 0, "users ls"); err != nil {
		return err
	}
	st, err := r.load(store.SliceUsers | store.SliceNodes)
	if err != nil {
		return err
	}
	us := append([]api.User(nil), st.Users...)
	sort.SliceStable(us, func(i, j int) bool { return us[i].Name < us[j].Name })
	out := make([]userOut, len(us))
	for i, u := range us {
		out[i] = userDTO(u, st)
	}
	return emit(r.stdout, v.out, out, usersTable(out))
}

// printUser prints the changed user when -o was given.
func (r *runner) printUser(v *verbFlags) error {
	if v.out == "" {
		return nil
	}
	u, ok := r.rec.lastUser()
	if !ok {
		return nil
	}
	out := userDTO(u, r.st)
	return emit(r.stdout, v.out, out, usersTable([]userOut{out}))
}

func usersCreate(r *runner, args []string) error {
	v := r.newFlags("users create", "")
	display := v.fs.String("display-name", "", "display name")
	email := v.fs.String("email", "", "email address")
	pos, err := v.args(args, 1, 1, "users create NAME [--display-name N] [--email E]")
	if err != nil {
		return err
	}
	if _, _, err := r.runAction("users", false, store.SliceUsers|store.SliceNodes, nil, call{id: "users.create", yes: v.yes,
		values: module.Values{"name": pos[0], "display": *display, "email": *email}}); err != nil {
		return err
	}
	return r.printUser(v)
}

func (r *runner) userAction(v *verbFlags, arg string, c call, check func(api.User, *store.Store) error) error {
	st, err := r.load(store.SliceUsers | store.SliceNodes | store.SlicePreAuthKeys)
	if err != nil {
		return err
	}
	u, err := resolveUser(st, arg)
	if err != nil {
		return err
	}
	if check != nil {
		if err := check(u, st); err != nil {
			return err
		}
	}
	c.yes = v.yes
	_, _, err = r.runAction("users", false, store.SliceUsers|store.SliceNodes|store.SlicePreAuthKeys, selectID(u.ID), c)
	return err
}

func usersRename(r *runner, args []string) error {
	v := r.newFlags("users rename", "")
	pos, err := v.args(args, 2, 2, "users rename ID|NAME NEW")
	if err != nil {
		return err
	}
	if err := r.userAction(v, pos[0], call{id: "users.rename", values: module.Values{"name": pos[1]}}, nil); err != nil {
		return err
	}
	return r.printUser(v)
}

func usersDelete(r *runner, args []string) error {
	v := r.newFlags("users delete", "")
	pos, err := v.args(args, 1, 1, "users delete ID|NAME [--yes]")
	if err != nil {
		return err
	}
	return r.userAction(v, pos[0], call{id: "users.delete"}, func(u api.User, st *store.Store) error {
		if n := len(st.NodesOf(u.Name)); n > 0 {
			return errorf(ExitInvalid, "%s still owns %d machine(s); delete them or register them to another user first (nodes ls --user %s)", u.Name, n, u.Name)
		}
		return nil
	})
}
