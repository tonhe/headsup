package cli

import (
	"github.com/tonhe/headsup/internal/api"
	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/store"
)

// --- auth -------------------------------------------------------------------

// authApprove queues the request on the pending page (as pasting an auth URL
// does), selects it, and runs its Approve action.
func authApprove(r *runner, args []string) error {
	v := r.newFlags("auth approve", "")
	user := v.fs.String("user", "", "owner of the device (default: the first user)")
	pos, err := v.args(args, 1, 1, "auth approve AUTH_ID|URL [--user U]")
	if err != nil {
		return err
	}
	vals := module.Values{}
	if *user != "" {
		vals["user"] = *user
	}
	return r.authOp(pos[0], call{id: "pending.approve", values: vals, yes: v.yes})
}

func authReject(r *runner, args []string) error {
	v := r.newFlags("auth reject", "")
	pos, err := v.args(args, 1, 1, "auth reject AUTH_ID|URL")
	if err != nil {
		return err
	}
	return r.authOp(pos[0], call{id: "pending.reject", yes: v.yes})
}

func (r *runner) authOp(text string, c call) error {
	s, err := r.session("pending", false, store.SliceUsers|store.SliceHost)
	if err != nil {
		return err
	}
	if _, err := s.run(call{id: "pending.queue", values: module.Values{"text": text}}); err != nil {
		return err
	}
	id := lastQueued(s, text)
	if err := s.seek(c.id, "login request", id); err != nil {
		return err
	}
	res, err := s.run(c)
	if err == nil && res.Message != "" {
		r.logf("%s", res.Message)
	}
	return err
}

// lastQueued is the auth ID the queue action parsed from text: the target
// the approve action reports once the cursor is on it. Pasted URLs end in
// the ID, so the last path segment is used.
func lastQueued(_ *session, text string) string {
	for i := len(text) - 1; i >= 0; i-- {
		if text[i] == '/' {
			return text[i+1:]
		}
	}
	return text
}

// --- server -----------------------------------------------------------------

type serverOut struct {
	Version      string   `json:"version" yaml:"version"`
	Healthy      bool     `json:"healthy" yaml:"healthy"`
	URL          string   `json:"url" yaml:"url"`
	Mode         string   `json:"mode" yaml:"mode"`
	Connection   string   `json:"connection" yaml:"connection"`
	ReadOnly     bool     `json:"read_only" yaml:"read_only"`
	Supported    bool     `json:"supported_version" yaml:"supported_version"`
	Capabilities []string `json:"capabilities" yaml:"capabilities"`
}

func serverInfo(r *runner, args []string) error {
	v := r.newFlags("server info", fmtTable)
	if _, err := v.args(args, 0, 0, "server info"); err != nil {
		return err
	}
	c, err := r.connect()
	if err != nil {
		return err
	}
	info := r.st.Info
	caps := c.Backend.Capabilities()
	out := serverOut{Version: info.Version, Healthy: info.Healthy, URL: c.ServerURL, Mode: "remote",
		Connection: c.Label, ReadOnly: c.ReadOnly, Supported: api.Supported(info.Version), Capabilities: []string{}}
	if c.Host != nil {
		out.Mode = "host"
	}
	for _, cp := range api.AllCaps() {
		if caps.Has(cp) {
			out.Capabilities = append(out.Capabilities, cp.String())
		}
	}
	return emit(r.stdout, v.out, out, func(bool) table {
		return kvTable([2]string{"version", out.Version}, [2]string{"healthy", yesNo(out.Healthy)},
			[2]string{"url", out.URL}, [2]string{"mode", out.Mode}, [2]string{"connection", out.Connection},
			[2]string{"read_only", yesNo(out.ReadOnly)}, [2]string{"supported_version", yesNo(out.Supported)})
	})
}

type capOut struct {
	Name      string `json:"name" yaml:"name"`
	Supported bool   `json:"supported" yaml:"supported"`
}

func serverCaps(r *runner, args []string) error {
	v := r.newFlags("server caps", fmtTable)
	if _, err := v.args(args, 0, 0, "server caps"); err != nil {
		return err
	}
	c, err := r.connect()
	if err != nil {
		return err
	}
	caps := c.Backend.Capabilities()
	var out []capOut
	for _, cp := range api.AllCaps() {
		out = append(out, capOut{Name: cp.String(), Supported: caps.Has(cp)})
	}
	return emit(r.stdout, v.out, out, func(bool) table {
		t := table{head: []string{"CAPABILITY", "SUPPORTED"}}
		for _, x := range out {
			t.add(x.Name, yesNo(x.Supported))
		}
		return t
	})
}

func serverBackfill(r *runner, args []string) error {
	v := r.newFlags("server backfill-ips", "")
	if _, err := v.args(args, 0, 0, "server backfill-ips [--yes]"); err != nil {
		return err
	}
	if _, _, err := r.runAction("server", false, store.SliceNodes, nil, call{id: "server.backfill", yes: v.yes}); err != nil {
		return err
	}
	ch := nonNil(r.rec.changes)
	if v.out == "" {
		for _, c := range ch {
			r.logf("  %s", c)
		}
		return nil
	}
	type out struct {
		Changes []string `json:"changes" yaml:"changes"`
	}
	return emit(r.stdout, v.out, out{Changes: ch}, func(bool) table {
		t := table{head: []string{"CHANGE"}}
		for _, c := range ch {
			t.add(c)
		}
		return t
	})
}
