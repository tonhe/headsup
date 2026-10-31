package cli

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/tonhe/headsup/internal/api"
	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/store"
	"github.com/tonhe/headsup/internal/ui/qr"
)

const keySlices = store.SlicePreAuthKeys | store.SliceAPIKeys | store.SliceUsers

func keysPreauth(r *runner, args []string) error {
	return sub(r, "keys preauth", args, group{
		"ls": preLs, "list": preLs, "create": preCreate, "expire": keyOp(false, "keys.expire"), "delete": keyOp(false, "keys.delete"), "rm": keyOp(false, "keys.delete"),
	})
}

func keysAPI(r *runner, args []string) error {
	return sub(r, "keys api", args, group{
		"ls": apiLs, "list": apiLs, "create": apiCreate, "expire": keyOp(true, "keys.expire"), "delete": keyOp(true, "keys.delete"), "rm": keyOp(true, "keys.delete"),
	})
}

func preTable(ks []preKeyOut) func(bool) table {
	return func(wide bool) table {
		t := table{head: []string{"ID", "USER", "KEY", "REUSABLE", "EPHEMERAL", "STATE", "EXPIRES", "TAGS"}}
		if wide {
			t.head = append(t.head, "USED", "CREATED")
		}
		for _, k := range ks {
			user := ""
			if k.User != nil {
				user = k.User.Name
			}
			row := []string{idStr(k.ID), user, k.Key, yesNo(k.Reusable), yesNo(k.Ephemeral), k.State, deref(k.Expiration), strings.Join(k.Tags, ",")}
			if wide {
				row = append(row, yesNo(k.Used), deref(k.CreatedAt))
			}
			t.add(row...)
		}
		return t
	}
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func preLs(r *runner, args []string) error {
	v := r.newFlags("keys preauth ls", fmtTable)
	user := v.fs.String("user", "", "only this user's keys")
	if _, err := v.args(args, 0, 0, "keys preauth ls [--user U]"); err != nil {
		return err
	}
	st, err := r.load(keySlices)
	if err != nil {
		return err
	}
	ks := append([]api.PreAuthKey(nil), st.PreAuthKeys...)
	sort.SliceStable(ks, func(i, j int) bool { return ks[i].ID < ks[j].ID })
	out := []preKeyOut{}
	for _, k := range ks {
		if *user != "" && (k.User == nil || k.User.Name != *user) {
			continue
		}
		out = append(out, preKeyDTO(k, r.now(), false))
	}
	return emit(r.stdout, v.out, out, preTable(out))
}

func apiTable(ks []apiKeyOut) func(bool) table {
	return func(wide bool) table {
		t := table{head: []string{"ID", "PREFIX", "EXPIRES", "EXPIRED"}}
		if wide {
			t.head = append(t.head, "CREATED", "LAST SEEN")
		}
		for _, k := range ks {
			row := []string{idStr(k.ID), k.Prefix, deref(k.Expiration), yesNo(k.Expired)}
			if wide {
				row = append(row, deref(k.CreatedAt), deref(k.LastSeen))
			}
			t.add(row...)
		}
		return t
	}
}

func apiLs(r *runner, args []string) error {
	v := r.newFlags("keys api ls", fmtTable)
	if _, err := v.args(args, 0, 0, "keys api ls"); err != nil {
		return err
	}
	st, err := r.load(keySlices)
	if err != nil {
		return err
	}
	ks := append([]api.APIKey(nil), st.APIKeys...)
	sort.SliceStable(ks, func(i, j int) bool { return ks[i].ID < ks[j].ID })
	out := make([]apiKeyOut, len(ks))
	for i, k := range ks {
		out[i] = apiKeyDTO(k, r.now())
	}
	return emit(r.stdout, v.out, out, apiTable(out))
}

// keysSession opens the keys page on the right tab.
func (r *runner) keysSession(apiTab bool) (*session, error) {
	s, err := r.session("keys", false, keySlices)
	if err != nil {
		return nil, err
	}
	if apiTab {
		s.key("tab")
	}
	return s, nil
}

func preCreate(r *runner, args []string) error {
	v := r.newFlags("keys preauth create", "")
	user := v.fs.String("user", "", "owner (required unless the server allows tag-only keys)")
	reusable := v.fs.Bool("reusable", false, "can register more than one device")
	ephemeral := v.fs.Bool("ephemeral", false, "devices are removed when they go offline")
	exp := v.fs.String("expiration", "24h", "lifetime, e.g. 1h, 24h, 7d")
	tags := v.fs.String("tags", "", "comma-separated ACL tags")
	showQR := v.fs.Bool("qr", false, "also print a QR code of the tailscale up command (stderr)")
	if _, err := v.args(args, 0, 0, "keys preauth create --user U [--reusable] [--ephemeral] [--expiration 24h] [--tags a,b] [--qr]"); err != nil {
		return err
	}
	s, err := r.keysSession(false)
	if err != nil {
		return err
	}
	vals := module.Values{"reusable": strconv.FormatBool(*reusable), "ephemeral": strconv.FormatBool(*ephemeral),
		"expiration": *exp, "tags": *tags}
	if *user != "" {
		vals["user"] = *user
	}
	if _, err := s.run(call{id: "keys.new", values: vals, yes: v.yes}); err != nil {
		return err
	}
	k := r.rec.preKey
	if k == nil {
		return errorf(ExitError, "the server returned no key")
	}
	if *showQR {
		cmd := "tailscale up --login-server=" + r.conn.ServerURL + " --authkey=" + k.Key.Reveal()
		if q, err := qr.Render(cmd); err == nil {
			fmt.Fprintln(r.stderr, q)
		}
	}
	if v.out == "" {
		fmt.Fprintln(r.stdout, k.Key.Reveal())
		return nil
	}
	out := preKeyDTO(*k, r.now(), true)
	return emit(r.stdout, v.out, out, preTable([]preKeyOut{out}))
}

func apiCreate(r *runner, args []string) error {
	v := r.newFlags("keys api create", "")
	exp := v.fs.String("expiration", "90d", "lifetime, e.g. 30d, 90d")
	if _, err := v.args(args, 0, 0, "keys api create [--expiration 90d]"); err != nil {
		return err
	}
	s, err := r.keysSession(true)
	if err != nil {
		return err
	}
	if _, err := s.run(call{id: "keys.new", values: module.Values{"expiration": *exp}, yes: v.yes}); err != nil {
		return err
	}
	k := r.rec.apiKey
	if k == nil {
		return errorf(ExitError, "the server returned no key")
	}
	if v.out == "" {
		fmt.Fprintln(r.stdout, k.Reveal())
		return nil
	}
	type created struct {
		Key string `json:"key" yaml:"key"`
	}
	return emit(r.stdout, v.out, created{Key: k.Reveal()}, func(bool) table { return kvTable([2]string{"key", k.Reveal()}) })
}

// keyOp expires or deletes a pre-auth key (by ID) or an API key (by prefix
// or ID) through the keys page, selecting the row first.
func keyOp(apiTab bool, id string) handler {
	return func(r *runner, args []string) error {
		kind, usage := "pre-auth key", "keys preauth "
		if apiTab {
			kind, usage = "API key", "keys api "
		}
		verb := strings.TrimPrefix(id, "keys.")
		v := r.newFlags(usage+verb, "")
		pos, err := v.args(args, 1, 1, usage+verb+" ID")
		if err != nil {
			return err
		}
		s, err := r.keysSession(apiTab)
		if err != nil {
			return err
		}
		want := pos[0]
		if apiTab {
			// The API tab's targets are prefixes; accept an ID too.
			for _, k := range r.st.APIKeys {
				if idStr(k.ID) == want {
					want = k.Prefix
				}
			}
		}
		if err := s.seek(id, kind, want); err != nil {
			return err
		}
		res, err := s.run(call{id: id, yes: v.yes})
		if err == nil && res.Message != "" {
			r.logf("%s", res.Message)
		}
		return err
	}
}
