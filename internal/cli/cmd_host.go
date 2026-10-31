package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tonhe/headsup/internal/activity"
	"github.com/tonhe/headsup/internal/api"
	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/host/backup"
	"github.com/tonhe/headsup/internal/host/derpmap"
	"github.com/tonhe/headsup/internal/host/dnsrec"
	"github.com/tonhe/headsup/internal/host/doctor"
	"github.com/tonhe/headsup/internal/host/hsconfig"
	"github.com/tonhe/headsup/internal/module"
	pol "github.com/tonhe/headsup/internal/policy"
)

// Host verbs need host mode: exit code 8 when it is unavailable and 9 on a
// missing permission, with the fix command on stderr. Service actions,
// backup, restore, upgrade checks and config migration run the module
// Actions; reads and the single-key config writes use the host adapter's
// managed-file pipeline (validate, back up, write atomically) directly,
// because the pages edit through a cursor and a staged review.

// --- host -------------------------------------------------------------------

type permOut struct {
	Need   string `json:"need" yaml:"need"`
	OK     bool   `json:"ok" yaml:"ok"`
	Reason string `json:"reason" yaml:"reason"`
	Fix    string `json:"fix" yaml:"fix"`
}

type envOut struct {
	Usable     bool      `json:"usable" yaml:"usable"`
	Hostname   string    `json:"hostname" yaml:"hostname"`
	Config     string    `json:"config" yaml:"config"`
	Version    string    `json:"version" yaml:"version"`
	Transport  string    `json:"transport" yaml:"transport"`
	Socket     string    `json:"socket" yaml:"socket"`
	Container  string    `json:"container" yaml:"container"`
	Service    string    `json:"service" yaml:"service"`
	Logs       string    `json:"logs" yaml:"logs"`
	Database   string    `json:"database" yaml:"database"`
	DBPath     string    `json:"database_path" yaml:"database_path"`
	Debug      string    `json:"debug" yaml:"debug"`
	ServerURL  string    `json:"server_url" yaml:"server_url"`
	PolicyMode string    `json:"policy_mode" yaml:"policy_mode"`
	PolicyPath string    `json:"policy_path" yaml:"policy_path"`
	Perms      []permOut `json:"permissions" yaml:"permissions"`
	Notes      []string  `json:"notes" yaml:"notes"`
}

var needNames = []struct {
	n    host.Need
	name string
}{
	{host.NeedSocket, "local API"}, {host.NeedConfigR, "read config"}, {host.NeedConfigW, "write config"},
	{host.NeedPolicyW, "write policy"}, {host.NeedRecordsW, "write DNS records"}, {host.NeedService, "control service"},
	{host.NeedLogs, "read logs"}, {host.NeedDebug, "debug/metrics"}, {host.NeedDBRead, "read database"},
}

func permsOf(env host.Env) []permOut {
	out := []permOut{}
	for _, n := range needNames {
		p, ok := env.Perms[n.n]
		if !ok {
			continue
		}
		out = append(out, permOut{Need: n.name, OK: p.OK, Reason: p.Reason, Fix: p.FixCommand})
	}
	return out
}

func envDTO(env host.Env) envOut {
	return envOut{Usable: env.Usable(), Hostname: env.Hostname, Config: env.ConfigPath, Version: env.Version,
		Transport: env.Transport.String(), Socket: env.SocketPath,
		Container: strings.TrimSpace(env.ContainerRuntime + " " + env.Container), Service: env.Service.String(),
		Logs: env.LogSource.String(), Database: env.DB.String(), DBPath: env.DBPath, Debug: env.DebugAddr,
		ServerURL: env.ServerURL, PolicyMode: env.PolicyMode, PolicyPath: env.PolicyPath,
		Perms: permsOf(env), Notes: nonNil(env.Notes)}
}

// env is what host detection found: the demo host with --demo, otherwise a
// fresh detection that does not need a working connection.
func (r *runner) env() (host.Env, error) {
	if r.flags.Demo || r.o.DetectEnv == nil {
		c, err := r.hostConn()
		if err != nil {
			return host.Env{}, err
		}
		return c.Host.Env(), nil
	}
	return r.o.DetectEnv(r.flags), nil
}

func hostInfo(r *runner, args []string) error {
	v := r.newFlags("host info", "")
	if _, err := v.args(args, 0, 0, "host info"); err != nil {
		return err
	}
	env, err := r.env()
	if err != nil {
		return err
	}
	d := envDTO(env)
	if v.out != "" {
		if err := emit(r.stdout, v.out, d, func(bool) table { return envTable(d) }); err != nil {
			return err
		}
	} else {
		w := r.stdout
		_ = envTable(d).write(w)
		fmt.Fprintln(w)
		for _, n := range needNames {
			p, ok := env.Perms[n.n]
			switch {
			case !ok:
				fmt.Fprintf(w, "  -  %-18s not applicable\n", n.name)
			case p.OK:
				fmt.Fprintf(w, "  ok %-18s\n", n.name)
			default:
				fmt.Fprintf(w, "  no %-18s %s\n", n.name, p.Reason)
				if p.FixCommand != "" {
					fmt.Fprintf(w, "     %-18s fix: %s\n", "", p.FixCommand)
				}
			}
		}
		for _, n := range env.Notes {
			fmt.Fprintln(w, "note:", n)
		}
	}
	if !env.Usable() {
		e := &Error{Code: ExitNoHost, Msg: "host mode is not available on this machine"}
		if p, missing := env.Perms.Missing(host.NeedSocket); missing {
			e.Fix = p.FixCommand
			if p.Denied {
				e.Code = ExitPermission
			}
		}
		return e
	}
	return nil
}

func envTable(d envOut) table {
	return kvTable([2]string{"hostname", d.Hostname}, [2]string{"config", d.Config}, [2]string{"version", d.Version},
		[2]string{"transport", d.Transport}, [2]string{"socket", d.Socket}, [2]string{"container", d.Container},
		[2]string{"service", d.Service}, [2]string{"logs", d.Logs},
		[2]string{"database", strings.TrimSpace(d.Database + " " + d.DBPath)}, [2]string{"debug", d.Debug},
		[2]string{"server_url", d.ServerURL}, [2]string{"policy", strings.TrimSpace(d.PolicyMode + " " + d.PolicyPath)})
}

func hostPerms(r *runner, args []string) error {
	v := r.newFlags("host perms", fmtTable)
	if _, err := v.args(args, 0, 0, "host perms"); err != nil {
		return err
	}
	env, err := r.env()
	if err != nil {
		return err
	}
	ps := permsOf(env)
	return emit(r.stdout, v.out, ps, func(bool) table {
		t := table{head: []string{"PERMISSION", "OK", "REASON", "FIX"}}
		for _, p := range ps {
			t.add(p.Need, yesNo(p.OK), p.Reason, p.Fix)
		}
		return t
	})
}

type findingOut struct {
	ID       string `json:"id" yaml:"id"`
	Check    string `json:"check" yaml:"check"`
	Severity string `json:"severity" yaml:"severity"`
	Title    string `json:"title" yaml:"title"`
	Detail   string `json:"detail" yaml:"detail"`
	Fix      string `json:"fix" yaml:"fix"`
}

func (r *runner) simulated() bool {
	if r.conn == nil {
		return false
	}
	if s, ok := r.conn.Host.(interface{ Simulated() bool }); ok && s.Simulated() {
		return true
	}
	return r.rec != nil && r.rec.Simulated()
}

func hostDoctor(r *runner, args []string) error {
	v := r.newFlags("host doctor", fmtTable)
	clock := v.fs.Bool("clock", false, "also check clock skew against an outside server")
	if _, err := v.args(args, 0, 0, "host doctor [--clock]"); err != nil {
		return err
	}
	c, err := r.hostConn()
	if err != nil {
		return err
	}
	p := doctor.OSProbes()
	if r.simulated() {
		p = doctor.SimulatedProbes(c.Host, r.now)
	}
	if !*clock {
		p.ClockURL = ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fs := doctor.Run(ctx, c.Host, p, doctor.FromConfig(r.o.Config.Host.Doctor))
	out := make([]findingOut, len(fs))
	for i, f := range fs {
		out[i] = findingOut{ID: f.ID, Check: f.Check, Severity: f.Severity.String(), Title: f.Title, Detail: f.Detail, Fix: f.Fix}
	}
	if err := emit(r.stdout, v.out, out, func(wide bool) table {
		t := table{head: []string{"SEVERITY", "CHECK", "FINDING", "FIX"}}
		if wide {
			t.head = append(t.head, "ID", "DETAIL")
		}
		for _, f := range out {
			row := []string{f.Severity, f.Check, f.Title, f.Fix}
			if wide {
				row = append(row, f.ID, f.Detail)
			}
			t.add(row...)
		}
		return t
	}); err != nil {
		return err
	}
	if _, errs := doctor.Counts(fs); errs > 0 {
		return errorf(ExitError, "the doctor found %d error(s)", errs)
	}
	return nil
}

// --- svc --------------------------------------------------------------------

func svcOp(id string) handler {
	return func(r *runner, args []string) error {
		name := "svc " + strings.TrimPrefix(id, "service.")
		v := r.newFlags(name, "")
		if _, err := v.args(args, 0, 0, name+" [--yes]"); err != nil {
			return err
		}
		if _, err := r.hostConn(); err != nil {
			return err
		}
		_, _, err := r.runAction("service", false, 0, nil, call{id: id, yes: v.yes})
		return err
	}
}

type svcOut struct {
	Manager string  `json:"manager" yaml:"manager"`
	State   string  `json:"state" yaml:"state"`
	Running bool    `json:"running" yaml:"running"`
	Since   *string `json:"since" yaml:"since"`
	PID     int     `json:"pid" yaml:"pid"`
	Memory  uint64  `json:"memory_bytes" yaml:"memory_bytes"`
	Version string  `json:"version" yaml:"version"`
}

func svcStatus(r *runner, args []string) error {
	v := r.newFlags("svc status", fmtTable)
	if _, err := v.args(args, 0, 0, "svc status"); err != nil {
		return err
	}
	c, err := r.hostConn()
	if err != nil {
		return err
	}
	svc := c.Host.Service()
	if svc == nil || svc.Kind() == host.ServiceNone {
		return errorf(ExitUnsupported, "no service manager was detected for headscale")
	}
	ctx, cancel := api.WithTimeout(context.Background())
	defer cancel()
	st, err := svc.Status(ctx)
	if err != nil {
		return err
	}
	o := svcOut{Manager: svc.Kind().String(), State: st.State, Running: st.Running, Since: ts(st.Since), PID: st.PID, Memory: st.Memory, Version: st.Version}
	if err := emit(r.stdout, v.out, o, func(bool) table {
		return kvTable([2]string{"manager", o.Manager}, [2]string{"state", o.State}, [2]string{"running", yesNo(o.Running)},
			[2]string{"since", deref(o.Since)}, [2]string{"pid", strconv.Itoa(o.PID)}, [2]string{"version", o.Version})
	}); err != nil {
		return err
	}
	return nil
}

func parseLevel(s string) (host.LogLevel, error) {
	switch strings.ToLower(s) {
	case "", "trace", "trc":
		return host.LevelTrace, nil
	case "debug", "dbg":
		return host.LevelDebug, nil
	case "info", "inf":
		return host.LevelInfo, nil
	case "warn", "warning", "wrn":
		return host.LevelWarn, nil
	case "error", "err":
		return host.LevelError, nil
	case "fatal", "ftl":
		return host.LevelFatal, nil
	}
	return 0, usagef("unknown log level %q (trace, debug, info, warn, error)", s)
}

func svcLogs(r *runner, args []string) error {
	v := r.newFlags("svc logs", "")
	follow := v.fs.Bool("f", false, "follow new lines")
	v.fs.BoolVar(follow, "follow", false, "follow new lines")
	level := v.fs.String("level", "", "minimum level: debug, info, warn, error")
	grep := v.fs.String("grep", "", "only lines matching this regular expression")
	tail := v.fs.Int("tail", 100, "lines of history to show first")
	if _, err := v.args(args, 0, 0, "svc logs [-f] [--level warn] [--grep RE] [--tail N] [-o json]"); err != nil {
		return err
	}
	if err := checkFormat(v.out, "", fmtJSON); err != nil {
		return err
	}
	min, err := parseLevel(*level)
	if err != nil {
		return err
	}
	var re *regexp.Regexp
	if *grep != "" {
		if re, err = regexp.Compile(*grep); err != nil {
			return usagef("--grep: %v", err)
		}
	}
	if err := r.need(host.NeedLogs); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ch, err := r.conn.Host.Logs(ctx, host.LogQuery{Follow: *follow, Tail: *tail})
	if err != nil {
		return err
	}
	enc := json.NewEncoder(r.stdout)
	for e := range ch {
		if e.Level < min {
			continue
		}
		line := e.Raw
		if line == "" {
			line = fmt.Sprintf("%s %s %s", e.Time.UTC().Format(time.RFC3339), e.Level, e.Msg)
		}
		if re != nil && !re.MatchString(line) {
			continue
		}
		if v.out == fmtJSON {
			_ = enc.Encode(struct {
				Time   *string           `json:"time"`
				Level  string            `json:"level"`
				Msg    string            `json:"msg"`
				Fields map[string]string `json:"fields"`
			}{ts(e.Time), strings.ToLower(e.Level.String()), e.Msg, e.Fields})
			continue
		}
		fmt.Fprintln(r.stdout, line)
	}
	return nil
}

// --- conf -------------------------------------------------------------------

func (r *runner) openConfig() (*host.File, *hsconfig.Doc, error) {
	if err := r.need(host.NeedConfigR); err != nil {
		return nil, nil, err
	}
	f, err := r.conn.Host.Open(host.FileConfig, "")
	if err != nil {
		return nil, nil, err
	}
	d, err := hsconfig.Parse(f.Raw)
	if err != nil {
		return nil, nil, errorf(ExitInvalid, "%s does not parse: %v", f.Path, err)
	}
	return f, d, nil
}

func confGet(r *runner, args []string) error {
	v := r.newFlags("conf get", "")
	pos, err := v.args(args, 1, 1, "conf get KEY (e.g. dns.base_domain)")
	if err != nil {
		return err
	}
	_, d, err := r.openConfig()
	if err != nil {
		return err
	}
	key := pos[0]
	type out struct {
		Key   string   `json:"key" yaml:"key"`
		Value *string  `json:"value" yaml:"value"`
		List  []string `json:"list,omitempty" yaml:"list,omitempty"`
	}
	o := out{Key: key}
	if l, ok := d.List(key); ok {
		o.List = nonNil(l)
	} else if s, ok := d.Get(key); ok {
		o.Value = &s
	} else {
		return errorf(ExitNotFound, "%s is not set in the config (headscale uses its default)", key)
	}
	if v.out == "" {
		if o.Value != nil {
			fmt.Fprintln(r.stdout, *o.Value)
		}
		for _, x := range o.List {
			fmt.Fprintln(r.stdout, x)
		}
		return nil
	}
	return emit(r.stdout, v.out, o, func(bool) table {
		val := strings.Join(o.List, ", ")
		if o.Value != nil {
			val = *o.Value
		}
		return kvTable([2]string{key, val})
	})
}

func confSet(r *runner, args []string) error {
	v := r.newFlags("conf set", "")
	noRestart := v.fs.Bool("no-restart", false, "write the file but do not restart or reload headscale")
	pos, err := v.args(args, 2, 2, "conf set KEY VALUE [--yes] [--no-restart]")
	if err != nil {
		return err
	}
	return r.setConfigKeys(map[string]string{pos[0]: pos[1]}, v.yes, *noRestart, "conf.set")
}

// setConfigKeys edits config.yaml through the managed-file pipeline: plan
// (validate and diff), confirm by the change's danger, apply (backup and
// atomic write), then restart or reload when the change needs it.
func (r *runner) setConfigKeys(kv map[string]string, yes, noRestart bool, actionID string) error {
	f, d, err := r.openConfig()
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(kv))
	for k := range kv {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if err := d.Set(k, kv[k]); err != nil {
			return errorf(ExitInvalid, "%s: %v", k, err)
		}
	}
	return r.writeFile(f, d.Bytes(), yes, noRestart, actionID, "Write "+f.Path)
}

// writeFile plans, confirms and applies a managed-file change.
func (r *runner) writeFile(f *host.File, next []byte, yes, noRestart bool, actionID, title string) error {
	need := host.NeedConfigW
	switch f.Kind {
	case host.FilePolicy:
		need = host.NeedPolicyW
	case host.FileExtraRecords:
		need = host.NeedRecordsW
	}
	if r.conn.ReadOnly {
		return &Error{Code: ExitUnauthorized, Msg: "this profile is read-only; writing " + f.Path + " is a change"}
	}
	if err := r.need(need); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	ch, err := r.conn.Host.Plan(ctx, f, next)
	if err != nil {
		return err
	}
	if len(ch.Diff) == 0 && string(next) == string(f.Raw) {
		r.logf("No change to %s.", f.Path)
		return nil
	}
	for _, is := range ch.Issues {
		if is.Severity == host.SevWarn {
			r.logf("warning: %s", is.Message)
		}
	}
	if !ch.Valid() {
		return &host.ErrInvalid{Issues: ch.Issues}
	}
	added, removed := 0, 0
	for _, l := range ch.Diff {
		switch l.Op {
		case '+':
			added++
			fmt.Fprintf(r.stderr, "+%s\n", l.Text)
		case '-':
			removed++
			fmt.Fprintf(r.stderr, "-%s\n", l.Text)
		}
	}
	consequence := "A backup is kept. The change takes effect " + ch.Effect.String() + "."
	if ch.Effect == host.EffectRestart && !noRestart {
		consequence += " headscale is restarted afterwards; connected clients reconnect."
	}
	danger := module.Danger(max(ch.Danger, host.DangerConfirm))
	tg := []module.Target{{Kind: "file", ID: f.Path, Name: f.Path}}
	if err := r.confirm(danger, title, consequence, tg, yes); err != nil {
		return err
	}
	start := r.now()
	a, err := r.conn.Host.Apply(ctx, ch)
	e := activity.Entry{Time: r.now(), Profile: r.conn.Label, ActionID: actionID, Result: activity.OK(),
		DurationMS: r.now().Sub(start).Milliseconds(), BackupID: a.BackupPath,
		Targets:   []activity.Target{{Kind: "file", ID: f.Path, Name: f.Path}},
		DiffStats: &activity.DiffStats{Added: added, Removed: removed}}
	if err != nil {
		ae := api.AsError(err)
		e.Result = activity.Err(ae.Kind.String(), ae.Message)
	}
	_ = r.o.Activity.Log(e)
	if err != nil {
		return err
	}
	r.logf("Wrote %s (backup: %s).", f.Path, a.BackupPath)
	if noRestart {
		if ch.Effect != host.EffectLive {
			r.logf("The change takes effect %s; it was not applied (--no-restart).", ch.Effect)
		}
		return nil
	}
	switch ch.Effect {
	case host.EffectRestart:
		_, _, err = r.runAction("service", false, 0, nil, call{id: "service.restart", confirmed: true})
	case host.EffectReload:
		_, _, err = r.runAction("service", false, 0, nil, call{id: "service.reload", confirmed: true})
	}
	return err
}

func confDiff(r *runner, args []string) error {
	v := r.newFlags("conf diff", "")
	pos, err := v.args(args, 1, 1, "conf diff FILE (compare the live config with FILE)")
	if err != nil {
		return err
	}
	f, _, err := r.openConfig()
	if err != nil {
		return err
	}
	b, err := readInput(r, pos[0])
	if err != nil {
		return err
	}
	fmt.Fprint(r.stdout, pol.Unified(f.Path, pos[0], pol.Diff(string(f.Raw), b), 3))
	return nil
}

// confMigrate drives Server Settings: Migrate deprecated keys stages the
// moves and opens the review; "Write" applies it; a restart follows when
// the change needs one.
func confMigrate(r *runner, args []string) error {
	v := r.newFlags("conf migrate", "")
	noRestart := v.fs.Bool("no-restart", false, "do not restart headscale after writing")
	if _, err := v.args(args, 0, 0, "conf migrate [--yes] [--no-restart]"); err != nil {
		return err
	}
	if err := r.need(host.NeedConfigR); err != nil {
		return err
	}
	s, err := r.session("serverconfig", true, 0)
	if err != nil {
		return err
	}
	if _, err := s.run(call{id: "serverconfig.migrate"}); err != nil && !errors.Is(err, errNoResult) {
		return err
	}
	for _, t := range s.ctx.toasts {
		r.logf("%s", t.text)
	}
	res, err := s.run(call{id: "serverconfig.apply", yes: v.yes})
	if err != nil {
		return err
	}
	if res.Message != "" {
		r.logf("%s", res.Message)
	}
	if *noRestart {
		return nil
	}
	if a, ok := s.action("serverconfig.restart"); ok && a.Enabled != nil {
		if ok, _ := a.Enabled(s.ctx); ok {
			res, err := s.run(call{id: "serverconfig.restart", confirmed: true})
			if err == nil && res.Message != "" {
				r.logf("%s", res.Message)
			}
			return err
		}
	}
	return nil
}

// --- dns --------------------------------------------------------------------

type dnsOut struct {
	MagicDNS         bool                `json:"magic_dns" yaml:"magic_dns"`
	BaseDomain       string              `json:"base_domain" yaml:"base_domain"`
	OverrideLocalDNS bool                `json:"override_local_dns" yaml:"override_local_dns"`
	Nameservers      []string            `json:"nameservers" yaml:"nameservers"`
	Split            map[string][]string `json:"split" yaml:"split"`
	SearchDomains    []string            `json:"search_domains" yaml:"search_domains"`
	ExtraRecordsPath string              `json:"extra_records_path" yaml:"extra_records_path"`
	ExtraRecords     int                 `json:"extra_records" yaml:"extra_records"`
}

func dnsShow(r *runner, args []string) error {
	v := r.newFlags("dns show", fmtTable)
	if _, err := v.args(args, 0, 0, "dns show"); err != nil {
		return err
	}
	_, d, err := r.openConfig()
	if err != nil {
		return err
	}
	c := dnsrec.FromDoc(d)
	recs, _, err := r.records(c)
	if err != nil {
		return err
	}
	o := dnsOut{MagicDNS: c.MagicDNS, BaseDomain: c.BaseDomain, OverrideLocalDNS: c.OverrideLocalDNS,
		Nameservers: nonNil(c.Global), Split: c.Split, SearchDomains: nonNil(c.Search),
		ExtraRecordsPath: c.ExtraRecordsPath, ExtraRecords: len(recs)}
	return emit(r.stdout, v.out, o, func(bool) table {
		var split []string
		for _, dom := range c.SplitOrder {
			split = append(split, dom+"="+strings.Join(c.Split[dom], "+"))
		}
		return kvTable([2]string{"magic_dns", yesNo(o.MagicDNS)}, [2]string{"base_domain", o.BaseDomain},
			[2]string{"override_local_dns", yesNo(o.OverrideLocalDNS)}, [2]string{"nameservers", strings.Join(o.Nameservers, ", ")},
			[2]string{"split", strings.Join(split, ", ")}, [2]string{"search_domains", strings.Join(o.SearchDomains, ", ")},
			[2]string{"extra_records_path", o.ExtraRecordsPath}, [2]string{"extra_records", strconv.Itoa(o.ExtraRecords)})
	})
}

// records returns the extra records: from the records file when one is
// configured (and the file itself), otherwise the inline list.
func (r *runner) records(c dnsrec.Config) ([]dnsrec.Record, *dnsrec.File, error) {
	if c.ExtraRecordsPath == "" {
		return c.ExtraRecords, nil, nil
	}
	hf, err := r.conn.Host.Open(host.FileExtraRecords, c.ExtraRecordsPath)
	if err != nil {
		return nil, nil, err
	}
	var rf *dnsrec.File
	if hf.New || len(strings.TrimSpace(string(hf.Raw))) == 0 {
		rf, err = dnsrec.NewFile(hf.Path, nil)
	} else {
		rf, err = dnsrec.ParseFile(hf.Path, hf.Raw)
	}
	if err != nil {
		return nil, nil, errorf(ExitInvalid, "%s: %v", hf.Path, err)
	}
	return rf.Records, rf, nil
}

type recordOut struct {
	Name  string `json:"name" yaml:"name"`
	Type  string `json:"type" yaml:"type"`
	Value string `json:"value" yaml:"value"`
}

func dnsRecords(r *runner, args []string) error {
	return sub(r, "dns records", args, group{"ls": dnsRecLs, "list": dnsRecLs, "add": dnsRecAdd, "rm": dnsRecRm, "delete": dnsRecRm})
}

func dnsRecLs(r *runner, args []string) error {
	v := r.newFlags("dns records ls", fmtTable)
	if _, err := v.args(args, 0, 0, "dns records ls"); err != nil {
		return err
	}
	_, d, err := r.openConfig()
	if err != nil {
		return err
	}
	recs, _, err := r.records(dnsrec.FromDoc(d))
	if err != nil {
		return err
	}
	out := make([]recordOut, len(recs))
	for i, x := range recs {
		out[i] = recordOut{x.Name, x.Type, x.Value}
	}
	return emit(r.stdout, v.out, out, func(bool) table {
		t := table{head: []string{"NAME", "TYPE", "VALUE"}}
		for _, x := range out {
			t.add(x.Name, x.Type, x.Value)
		}
		return t
	})
}

// editRecords applies fn to the records and writes them back: the records
// file when configured (live, no restart), otherwise the inline list in
// config.yaml (needs a restart).
func (r *runner) editRecords(v *verbFlags, noRestart bool, actionID string, fn func([]dnsrec.Record) ([]dnsrec.Record, error)) error {
	cf, d, err := r.openConfig()
	if err != nil {
		return err
	}
	c := dnsrec.FromDoc(d)
	recs, rf, err := r.records(c)
	if err != nil {
		return err
	}
	next, err := fn(append([]dnsrec.Record(nil), recs...))
	if err != nil {
		return err
	}
	for _, is := range dnsrec.ValidateRecords(next) {
		if is.Severity == host.SevError {
			return errorf(ExitInvalid, "%s", is.Message)
		}
	}
	if rf == nil {
		if err := dnsrec.SetInline(d, next); err != nil {
			return errorf(ExitInvalid, "%v", err)
		}
		return r.writeFile(cf, d.Bytes(), v.yes, noRestart, actionID, "Write "+cf.Path)
	}
	hf, err := r.conn.Host.Open(host.FileExtraRecords, c.ExtraRecordsPath)
	if err != nil {
		return err
	}
	for len(rf.Records) > 0 {
		if err := rf.Delete(len(rf.Records) - 1); err != nil {
			return err
		}
	}
	for _, x := range next {
		if err := rf.Add(x); err != nil {
			return errorf(ExitInvalid, "%v", err)
		}
	}
	b, err := rf.Bytes()
	if err != nil {
		return err
	}
	return r.writeFile(hf, b, v.yes, noRestart, actionID, "Write "+hf.Path)
}

func dnsRecAdd(r *runner, args []string) error {
	v := r.newFlags("dns records add", "")
	noRestart := v.fs.Bool("no-restart", false, "do not restart headscale (inline records only)")
	pos, err := v.args(args, 3, 3, "dns records add NAME TYPE VALUE")
	if err != nil {
		return err
	}
	rec := dnsrec.Record{Name: pos[0], Type: strings.ToUpper(pos[1]), Value: pos[2]}
	if err := rec.Validate(); err != nil {
		return errorf(ExitInvalid, "%v", err)
	}
	return r.editRecords(v, *noRestart, "dns.add", func(rs []dnsrec.Record) ([]dnsrec.Record, error) {
		for _, x := range rs {
			if x == rec {
				return nil, errorf(ExitInvalid, "%s already exists", rec)
			}
		}
		return append(rs, rec), nil
	})
}

func dnsRecRm(r *runner, args []string) error {
	v := r.newFlags("dns records rm", "")
	noRestart := v.fs.Bool("no-restart", false, "do not restart headscale (inline records only)")
	typ := v.fs.String("type", "", "only records of this type")
	pos, err := v.args(args, 1, 1, "dns records rm NAME [--type A]")
	if err != nil {
		return err
	}
	return r.editRecords(v, *noRestart, "dns.delete", func(rs []dnsrec.Record) ([]dnsrec.Record, error) {
		var keep []dnsrec.Record
		for _, x := range rs {
			if strings.EqualFold(x.Name, pos[0]) && (*typ == "" || strings.EqualFold(x.Type, *typ)) {
				continue
			}
			keep = append(keep, x)
		}
		if len(keep) == len(rs) {
			return nil, errorf(ExitNotFound, "no record named %s", pos[0])
		}
		return keep, nil
	})
}

func dnsSet(r *runner, args []string) error {
	v := r.newFlags("dns set", "")
	noRestart := v.fs.Bool("no-restart", false, "write the file but do not restart headscale")
	pos, err := v.args(args, 2, 2, "dns set KEY VALUE (e.g. magic_dns false, base_domain tail.example.com)")
	if err != nil {
		return err
	}
	key := strings.TrimPrefix(pos[0], "dns.")
	return r.setConfigKeys(map[string]string{"dns." + key: pos[1]}, v.yes, *noRestart, "dns.edit")
}

// --- derp -------------------------------------------------------------------

type derpOut struct {
	ServerEnabled bool     `json:"server_enabled" yaml:"server_enabled"`
	RegionID      string   `json:"server_region_id" yaml:"server_region_id"`
	RegionCode    string   `json:"server_region_code" yaml:"server_region_code"`
	STUNAddr      string   `json:"stun_listen_addr" yaml:"stun_listen_addr"`
	URLs          []string `json:"urls" yaml:"urls"`
	Paths         []string `json:"paths" yaml:"paths"`
	AutoUpdate    string   `json:"auto_update_enabled" yaml:"auto_update_enabled"`
}

func derpShow(r *runner, args []string) error {
	v := r.newFlags("derp show", fmtTable)
	if _, err := v.args(args, 0, 0, "derp show"); err != nil {
		return err
	}
	_, d, err := r.openConfig()
	if err != nil {
		return err
	}
	get := func(k string) string { s, _ := d.Get(k); return s }
	list := func(k string) []string { l, _ := d.List(k); return nonNil(l) }
	o := derpOut{ServerEnabled: get("derp.server.enabled") == "true", RegionID: get("derp.server.region_id"),
		RegionCode: get("derp.server.region_code"), STUNAddr: get("derp.server.stun_listen_addr"),
		URLs: list("derp.urls"), Paths: list("derp.paths"), AutoUpdate: get("derp.auto_update_enabled")}
	return emit(r.stdout, v.out, o, func(bool) table {
		return kvTable([2]string{"server_enabled", yesNo(o.ServerEnabled)}, [2]string{"server_region", strings.TrimSpace(o.RegionID + " " + o.RegionCode)},
			[2]string{"stun_listen_addr", o.STUNAddr}, [2]string{"urls", strings.Join(o.URLs, ", ")},
			[2]string{"paths", strings.Join(o.Paths, ", ")}, [2]string{"auto_update", o.AutoUpdate})
	})
}

type regionOut struct {
	ID     int      `json:"id" yaml:"id"`
	Code   string   `json:"code" yaml:"code"`
	Name   string   `json:"name" yaml:"name"`
	Nodes  []string `json:"nodes" yaml:"nodes"`
	Source string   `json:"source" yaml:"source"`
}

func derpRegions(r *runner, args []string) error {
	return sub(r, "derp regions", args, group{"ls": derpRegionsLs, "list": derpRegionsLs})
}

// derpRegionsLs lists the regions of the local DERP map files (derp.paths).
func derpRegionsLs(r *runner, args []string) error {
	v := r.newFlags("derp regions ls", fmtTable)
	if _, err := v.args(args, 0, 0, "derp regions ls"); err != nil {
		return err
	}
	_, d, err := r.openConfig()
	if err != nil {
		return err
	}
	paths, _ := d.List("derp.paths")
	out := []regionOut{}
	for _, p := range paths {
		hf, err := r.conn.Host.Open(host.FileDERPMap, p)
		if err != nil {
			r.logf("%s: %v", p, err)
			continue
		}
		mf, err := derpmap.ParseFile(hf.Path, hf.Raw)
		if err != nil {
			r.logf("%s: %v", p, err)
			continue
		}
		for _, rg := range mf.Map.Regions {
			var ns []string
			for _, n := range rg.Nodes {
				ns = append(ns, n.HostName)
			}
			out = append(out, regionOut{ID: rg.RegionID, Code: rg.RegionCode, Name: rg.RegionName, Nodes: nonNil(ns), Source: p})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return emit(r.stdout, v.out, out, func(bool) table {
		t := table{head: []string{"ID", "CODE", "NAME", "NODES", "SOURCE"}}
		for _, x := range out {
			t.add(strconv.Itoa(x.ID), x.Code, x.Name, strings.Join(x.Nodes, ","), x.Source)
		}
		return t
	})
}

// --- backup -------------------------------------------------------------------

func (r *runner) backupRoot() string {
	if r.simulated() {
		return backup.DemoRoot()
	}
	return backup.DefaultRoot(r.o.Config.Host.BackupDir)
}

func backupCreate(r *runner, args []string) error {
	v := r.newFlags("backup create", "")
	if _, err := v.args(args, 0, 0, "backup create"); err != nil {
		return err
	}
	if _, err := r.hostConn(); err != nil {
		return err
	}
	_, _, err := r.runAction("server", false, 0, nil, call{id: "server.backup", yes: v.yes})
	return err
}

type bundleOut struct {
	ID      string  `json:"id" yaml:"id"`
	Created *string `json:"created" yaml:"created"`
	Files   int     `json:"files" yaml:"files"`
	Missing int     `json:"missing" yaml:"missing"`
	DB      string  `json:"database" yaml:"database"`
	Dir     string  `json:"dir" yaml:"dir"`
}

func backupLs(r *runner, args []string) error {
	v := r.newFlags("backup ls", fmtTable)
	if _, err := v.args(args, 0, 0, "backup ls"); err != nil {
		return err
	}
	if _, err := r.hostConn(); err != nil {
		return err
	}
	bs, err := backup.List(r.backupRoot())
	if err != nil {
		return err
	}
	out := make([]bundleOut, len(bs))
	for i, b := range bs {
		out[i] = bundleOut{ID: b.ID, Created: ts(b.Created), Files: b.Files, Missing: b.Missing, DB: b.DB, Dir: b.Dir}
	}
	return emit(r.stdout, v.out, out, func(wide bool) table {
		t := table{head: []string{"ID", "CREATED", "FILES", "DATABASE"}}
		if wide {
			t.head = append(t.head, "MISSING", "DIR")
		}
		for _, b := range out {
			row := []string{b.ID, deref(b.Created), strconv.Itoa(b.Files), b.DB}
			if wide {
				row = append(row, strconv.Itoa(b.Missing), b.Dir)
			}
			t.add(row...)
		}
		return t
	})
}

// backupRestore opens Server › Backups, selects the bundle and runs its
// Restore action (which takes a fresh backup first). Only files are ever
// restored; the database steps are printed, so --files-only is the default.
func backupRestore(r *runner, args []string) error {
	v := r.newFlags("backup restore", "")
	v.fs.Bool("files-only", true, "restore files only (always; the database is never written)")
	pos, err := v.args(args, 1, 1, "backup restore ID [--files-only] [--yes]")
	if err != nil {
		return err
	}
	if _, err := r.hostConn(); err != nil {
		return err
	}
	s, err := r.session("server", true, 0)
	if err != nil {
		return err
	}
	if _, err := s.run(call{id: "server.backups"}); err != nil && !errors.Is(err, errNoResult) {
		return err
	}
	if err := s.seek("server.restore", "backup", pos[0]); err != nil {
		return err
	}
	res, err := s.run(call{id: "server.restore", yes: v.yes})
	if err == nil && res.Message != "" {
		r.logf("%s", res.Message)
	}
	return err
}

// --- upgrade ------------------------------------------------------------------

func upgradeCheck(r *runner, args []string) error {
	v := r.newFlags("upgrade check", "")
	if _, err := v.args(args, 0, 0, "upgrade check"); err != nil {
		return err
	}
	_, _, err := r.runAction("server", false, 0, nil, call{id: "server.upgrade"})
	return err
}
