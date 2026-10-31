package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/tonhe/headsup/internal/activity"
	"github.com/tonhe/headsup/internal/api"
	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/module"
	pol "github.com/tonhe/headsup/internal/policy"
)

// The policy page's apply is a stateful review flow (draft, check, diff,
// confirm) that has no entry point for a draft from a file, so these verbs
// use the same primitives directly: the server's check, the managed-file
// pipeline in host file mode, the local history, and the activity log.

// livePolicy is the policy headscale enforces: the file in host file mode,
// otherwise the API.
type livePolicy struct {
	raw  string
	at   time.Time
	file *host.File // set in file mode
}

func (r *runner) fileMode() bool {
	return r.conn != nil && r.conn.Host != nil && r.conn.Host.Env().PolicyMode == "file"
}

func (r *runner) livePolicy() (livePolicy, error) {
	c, err := r.connect()
	if err != nil {
		return livePolicy{}, err
	}
	if r.fileMode() {
		f, err := c.Host.Open(host.FilePolicy, "")
		if err != nil {
			return livePolicy{}, err
		}
		return livePolicy{raw: string(f.Raw), file: f}, nil
	}
	ctx, cancel := api.WithTimeout(context.Background())
	defer cancel()
	p, err := c.Backend.GetPolicy(ctx)
	if err != nil {
		return livePolicy{}, err
	}
	return livePolicy{raw: p.Raw, at: p.UpdatedAt}, nil
}

func (r *runner) history() (*pol.History, string) {
	if r.o.PolicyHistoryDir == "" || r.conn == nil {
		return nil, ""
	}
	prof := r.conn.ServerURL
	if r.conn.Host != nil {
		prof = "host-" + r.conn.Host.Env().Hostname
	}
	return pol.NewHistory(r.o.PolicyHistoryDir), prof
}

func readInput(r *runner, path string) (string, error) {
	var b []byte
	var err error
	if path == "-" {
		b, err = io.ReadAll(r.o.Stdin)
	} else {
		b, err = os.ReadFile(path)
	}
	if err != nil {
		return "", errorf(ExitError, "reading %s: %v", path, err)
	}
	return string(b), nil
}

type policyOut struct {
	Raw       string  `json:"raw" yaml:"raw"`
	UpdatedAt *string `json:"updated_at" yaml:"updated_at"`
	Source    string  `json:"source" yaml:"source"`
	Path      string  `json:"path,omitempty" yaml:"path,omitempty"`
}

func policyGet(r *runner, args []string) error {
	v := r.newFlags("policy get", fmtRaw)
	if _, err := v.args(args, 0, 0, "policy get [-o raw|json|yaml]"); err != nil {
		return err
	}
	if err := checkFormat(v.out, fmtRaw, fmtJSON, fmtYAML); err != nil {
		return err
	}
	lp, err := r.livePolicy()
	if err != nil {
		return err
	}
	if v.out == fmtRaw {
		_, err := io.WriteString(r.stdout, lp.raw)
		if len(lp.raw) > 0 && lp.raw[len(lp.raw)-1] != '\n' {
			fmt.Fprintln(r.stdout)
		}
		return err
	}
	out := policyOut{Raw: lp.raw, UpdatedAt: ts(lp.at), Source: "api"}
	if lp.file != nil {
		out.Source, out.Path = "file", lp.file.Path
	}
	return emit(r.stdout, v.out, out, nil)
}

// checkPolicy validates text the way the policy page does: the managed-file
// pipeline in file mode, the server's check when it has one, otherwise the
// local parser. It returns the planned file change in file mode.
func (r *runner) checkPolicy(lp livePolicy, text string) (*host.Change, error) {
	c := r.conn
	ctx, cancel := api.WithTimeout(context.Background())
	defer cancel()
	if lp.file != nil {
		ch, err := c.Host.Plan(ctx, lp.file, []byte(text))
		if err != nil {
			return nil, err
		}
		for _, is := range ch.Issues {
			switch is.Severity {
			case host.SevError:
				return nil, invalidAt(is.Line, is.Col, is.Message)
			case host.SevWarn:
				r.logf("warning: %s", is.Message)
			}
		}
		return ch, nil
	}
	if c.Backend.Capabilities().Has(api.CapPolicyCheck) {
		err := c.Backend.CheckPolicy(ctx, text)
		if api.IsKind(err, api.KindInvalid) {
			ae := api.AsError(err)
			return nil, invalidAt(ae.Line, ae.Col, ae.Message)
		}
		if err != nil && !api.IsKind(err, api.KindUnsupported) {
			return nil, err
		}
		if err == nil {
			return nil, nil
		}
	}
	if err := pol.Check([]byte(text)); err != nil {
		var pe *pol.ParseError
		if errors.As(err, &pe) {
			return nil, invalidAt(pe.Line, pe.Col, pe.Msg)
		}
		return nil, errorf(ExitInvalid, "invalid policy: %v", err)
	}
	return nil, nil
}

func invalidAt(line, col int, msg string) error {
	if line > 0 {
		return errorf(ExitInvalid, "invalid policy: line %d, column %d: %s", line, col, msg)
	}
	return errorf(ExitInvalid, "invalid policy: %s", msg)
}

func policyCheck(r *runner, args []string) error {
	v := r.newFlags("policy check", "")
	pos, err := v.args(args, 1, 1, "policy check FILE (- for stdin)")
	if err != nil {
		return err
	}
	text, err := readInput(r, pos[0])
	if err != nil {
		return err
	}
	lp, err := r.livePolicy()
	if err != nil {
		return err
	}
	if _, err := r.checkPolicy(lp, text); err != nil {
		return err
	}
	r.logf("The policy is valid.")
	return nil
}

func policyDiff(r *runner, args []string) error {
	v := r.newFlags("policy diff", "")
	pos, err := v.args(args, 1, 1, "policy diff FILE (- for stdin)")
	if err != nil {
		return err
	}
	text, err := readInput(r, pos[0])
	if err != nil {
		return err
	}
	lp, err := r.livePolicy()
	if err != nil {
		return err
	}
	fmt.Fprint(r.stdout, pol.Unified("live", pos[0], pol.Diff(lp.raw, text), 3))
	return nil
}

func policySet(r *runner, args []string) error {
	v := r.newFlags("policy set", "")
	pos, err := v.args(args, 1, 1, "policy set FILE [--yes] (- for stdin)")
	if err != nil {
		return err
	}
	text, err := readInput(r, pos[0])
	if err != nil {
		return err
	}
	return r.applyPolicy(text, v.yes, "policy.apply")
}

// applyPolicy checks, confirms and applies text, recording history and the
// activity log as the policy page does.
func (r *runner) applyPolicy(text string, yes bool, actionID string) error {
	if r.flags.ReadOnly {
		return &Error{Code: ExitUnauthorized, Msg: "this profile is read-only; applying a policy is a change"}
	}
	lp, err := r.livePolicy()
	if err != nil {
		return err
	}
	if r.conn.ReadOnly {
		return &Error{Code: ExitUnauthorized, Msg: "this profile is read-only; applying a policy is a change"}
	}
	if pol.Equivalent(lp.raw, text) && lp.raw == text {
		r.logf("The policy is unchanged; nothing to apply.")
		return nil
	}
	ch, err := r.checkPolicy(lp, text)
	if err != nil {
		return err
	}
	added, removed := pol.Changed(pol.Diff(lp.raw, text))
	target := module.Target{Kind: "policy", ID: "policy", Name: "tailnet"}
	if lp.file != nil {
		target = module.Target{Kind: "file", ID: lp.file.Path, Name: lp.file.Path}
	}
	consequence := fmt.Sprintf("Access rules change for every machine in the tailnet (+%d −%d lines).", added, removed)
	if err := r.confirm(module.DangerConfirm, "Apply policy", consequence, []module.Target{target}, yes); err != nil {
		return err
	}
	hist, prof := r.history()
	start := r.now()
	ctx, cancel := context.WithTimeout(context.Background(), 2*api.DefaultTimeout)
	defer cancel()
	if hist != nil {
		_, _, _ = hist.Record(prof, lp.raw, pol.SourcePreApply, lp.at, r.now())
	}
	var applyErr error
	var updated time.Time
	if ch != nil {
		if _, applyErr = r.conn.Host.Apply(ctx, ch); applyErr == nil {
			if svc := r.conn.Host.Service(); svc != nil && svc.Kind() != host.ServiceNone {
				if err := svc.Reload(ctx); err != nil {
					applyErr = fmt.Errorf("the policy file was written, but the reload failed: %w", err)
				}
			} else {
				r.logf("No service manager was detected; send headscale SIGHUP to load the new policy.")
			}
		}
	} else {
		var p api.Policy
		p, applyErr = r.conn.Backend.SetPolicy(ctx, text)
		updated = p.UpdatedAt
	}
	if applyErr == nil && hist != nil {
		_, _, _ = hist.Record(prof, text, pol.SourceApplied, updated, r.now())
	}
	e := activity.Entry{Time: r.now(), Profile: r.conn.Label, ActionID: actionID, Result: activity.OK(),
		DurationMS: r.now().Sub(start).Milliseconds(),
		Targets:    []activity.Target{{Kind: target.Kind, ID: target.ID, Name: target.Name}},
		DiffStats:  &activity.DiffStats{Added: added, Removed: removed}}
	if applyErr != nil {
		ae := api.AsError(applyErr)
		e.Result = activity.Err(ae.Kind.String(), ae.Message)
	}
	_ = r.o.Activity.Log(e)
	if applyErr != nil {
		return applyErr
	}
	r.logf("Policy applied (+%d −%d lines).", added, removed)
	return nil
}

type revisionOut struct {
	N         int     `json:"n" yaml:"n"`
	FetchedAt *string `json:"fetched_at" yaml:"fetched_at"`
	ServerAt  *string `json:"server_updated_at" yaml:"server_updated_at"`
	Source    string  `json:"source" yaml:"source"`
	SHA256    string  `json:"sha256" yaml:"sha256"`
}

func (r *runner) revisions() (*pol.History, []pol.Revision, error) {
	if _, err := r.connect(); err != nil {
		return nil, nil, err
	}
	hist, prof := r.history()
	if hist == nil {
		return nil, nil, errorf(ExitUnsupported, "policy history is not kept for this connection")
	}
	revs, err := hist.List(prof)
	return hist, revs, err
}

func policyHistory(r *runner, args []string) error {
	v := r.newFlags("policy history", fmtTable)
	if _, err := v.args(args, 0, 0, "policy history"); err != nil {
		return err
	}
	_, revs, err := r.revisions()
	if err != nil {
		return err
	}
	out := make([]revisionOut, len(revs))
	for i, rv := range revs {
		out[i] = revisionOut{N: i + 1, FetchedAt: ts(rv.FetchedAt), ServerAt: ts(rv.ServerUpdatedAt), Source: string(rv.Source), SHA256: rv.SHA256}
	}
	return emit(r.stdout, v.out, out, func(bool) table {
		t := table{head: []string{"N", "RECORDED", "SOURCE", "SHA256"}}
		for _, x := range out {
			t.add(strconv.Itoa(x.N), deref(x.FetchedAt), x.Source, x.SHA256[:min(12, len(x.SHA256))])
		}
		return t
	})
}

func policyRollback(r *runner, args []string) error {
	v := r.newFlags("policy rollback", "")
	pos, err := v.args(args, 1, 1, "policy rollback N (from `policy history`)")
	if err != nil {
		return err
	}
	n, err := strconv.Atoi(pos[0])
	if err != nil || n < 1 {
		return usagef("N must be a revision number from `policy history`")
	}
	hist, revs, err := r.revisions()
	if err != nil {
		return err
	}
	if n > len(revs) {
		return errorf(ExitNotFound, "there is no revision %d (%d kept)", n, len(revs))
	}
	text, err := hist.Load(revs[n-1])
	if err != nil {
		return err
	}
	return r.applyPolicy(text, v.yes, "policy.rollback")
}
