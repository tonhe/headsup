// Package capsdoc renders the generated parts of docs/CAPABILITIES.md from
// the code that defines them: the capability flags and their versions
// (package api), the mode matrix the Server page shows (modules/server), the
// REST paths and headscale CLI commands each backend method sends (recorded
// from httpapi and execcli), the capability each method is gated on
// (probed on the fake backend) and every module's actions (the registry).
//
// The document keeps hand-written prose between generated regions marked
// <!-- BEGIN GENERATED: name --> ... <!-- END GENERATED: name -->.
// Regenerate with `make docs` (or go generate); a test fails when the
// committed file is stale.
package capsdoc

//go:generate go test -run TestCapabilitiesDoc -update-docs .

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/tonhe/headsup/internal/api"
	"github.com/tonhe/headsup/internal/api/execcli"
	"github.com/tonhe/headsup/internal/api/fake"
	"github.com/tonhe/headsup/internal/api/httpapi"
	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/modules/registry"
	"github.com/tonhe/headsup/internal/modules/server"

	// Every built-in module, so the actions table is complete.
	_ "github.com/tonhe/headsup/internal/modules/all"
)

// Sentinel arguments, replaced by placeholders in recorded requests.
const (
	sID     = 4242
	sName   = "NEWNAME"
	sUser   = "USERNAME"
	sKey    = "NODEKEY"
	sPrefix = "KEYPREFIX"
	sAuth   = "AUTHID"
)

var placeholders = map[string]string{
	fmt.Sprint(sID): "{id}", sName: "{name}", sUser: "{user}", sKey: "{key}",
	sPrefix: "{prefix}", sAuth: "{auth_id}",
}

// Endpoint is one api.Backend method.
type Endpoint struct {
	Method string
	// Call invokes the method with sentinel arguments.
	Call func(context.Context, api.Backend) error
	// GateCall, when set, is the variant that hits the method's capability
	// gate (e.g. ExpireNode with DisableExpiry).
	GateCall func(context.Context, api.Backend) error
	// UsedBy lists the module IDs (pages) and action IDs that call it, or
	// "app" for the app itself.
	UsedBy []string
	// Note qualifies the row (e.g. which argument the gate applies to).
	Note string
}

func noErr[T any](_ T, err error) error { return err }

var sPolicy = `{"acls": []}`

// Endpoints is the endpoint table: every api.Backend method, in interface order.
var Endpoints = []Endpoint{
	{Method: "Connect", Call: func(c context.Context, b api.Backend) error { return noErr(b.Connect(c)) }, UsedBy: []string{"app"},
		Note: "Version and health at startup; capabilities are derived from the version."},
	{Method: "Capabilities", Call: func(context.Context, api.Backend) error { return nil }, UsedBy: []string{"app"},
		Note: "Local: no request."},
	{Method: "ListNodes", Call: func(c context.Context, b api.Backend) error {
		return noErr(b.ListNodes(c, api.NodeFilter{User: sUser}))
	},
		UsedBy: []string{"dashboard", "machines", "routes"}},
	{Method: "GetNode", Call: func(c context.Context, b api.Backend) error { return noErr(b.GetNode(c, sID)) },
		UsedBy: []string{"machines.detail"}},
	{Method: "RegisterNode", Call: func(c context.Context, b api.Backend) error { return noErr(b.RegisterNode(c, sUser, sKey)) },
		UsedBy: []string{"pending.paste"}},
	{Method: "RenameNode", Call: func(c context.Context, b api.Backend) error { return noErr(b.RenameNode(c, sID, sName)) },
		UsedBy: []string{"machines.rename"}},
	{Method: "SetTags", Call: func(c context.Context, b api.Backend) error { return noErr(b.SetTags(c, sID, []string{"tag:demo"})) },
		UsedBy: []string{"machines.tags"}},
	{Method: "SetApprovedRoutes", Call: func(c context.Context, b api.Backend) error {
		return noErr(b.SetApprovedRoutes(c, sID, []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24")}))
	}, UsedBy: []string{"routes.approve", "routes.revoke", "routes.approveall", "machines.routes"}},
	{Method: "ExpireNode", Call: func(c context.Context, b api.Backend) error { return noErr(b.ExpireNode(c, sID, api.ExpireOpts{})) },
		GateCall: func(c context.Context, b api.Backend) error {
			return noErr(b.ExpireNode(c, sID, api.ExpireOpts{DisableExpiry: true}))
		}, UsedBy: []string{"machines.expire", "machines.noexpire"},
		Note: "The gate applies to disabling expiry only (`disableExpiry`, CLI `-d`)."},
	{Method: "DeleteNode", Call: func(c context.Context, b api.Backend) error { return b.DeleteNode(c, sID) },
		UsedBy: []string{"machines.delete"}},
	{Method: "BackfillIPs", Call: func(c context.Context, b api.Backend) error { return noErr(b.BackfillIPs(c)) },
		UsedBy: []string{"server.backfill"}},
	{Method: "ListUsers", Call: func(c context.Context, b api.Backend) error { return noErr(b.ListUsers(c, api.UserFilter{})) },
		UsedBy: []string{"users", "dashboard"}},
	{Method: "CreateUser", Call: func(c context.Context, b api.Backend) error { return noErr(b.CreateUser(c, api.NewUser{Name: sName})) },
		UsedBy: []string{"users.create"}},
	{Method: "RenameUser", Call: func(c context.Context, b api.Backend) error { return noErr(b.RenameUser(c, sID, sName)) },
		UsedBy: []string{"users.rename"}},
	{Method: "DeleteUser", Call: func(c context.Context, b api.Backend) error { return b.DeleteUser(c, sID) },
		UsedBy: []string{"users.delete"}},
	{Method: "ListPreAuthKeys", Call: func(c context.Context, b api.Backend) error { return noErr(b.ListPreAuthKeys(c, api.KeyFilter{})) },
		UsedBy: []string{"keys", "pending"}},
	{Method: "CreatePreAuthKey", Call: func(c context.Context, b api.Backend) error {
		return noErr(b.CreatePreAuthKey(c, api.NewPreAuthKey{User: sUser, Reusable: true}))
	}, GateCall: func(c context.Context, b api.Backend) error {
		return noErr(b.CreatePreAuthKey(c, api.NewPreAuthKey{ACLTags: []string{"tag:demo"}}))
	}, UsedBy: []string{"keys.new", "pending.newkey"},
		Note: "The user name is resolved to its ID first. The gate applies to tag-only keys (no user)."},
	{Method: "ExpirePreAuthKey", Call: func(c context.Context, b api.Backend) error { return b.ExpirePreAuthKey(c, sID) },
		UsedBy: []string{"keys.expire"}},
	{Method: "DeletePreAuthKey", Call: func(c context.Context, b api.Backend) error { return b.DeletePreAuthKey(c, sID) },
		UsedBy: []string{"keys.delete"}},
	{Method: "ListAPIKeys", Call: func(c context.Context, b api.Backend) error { return noErr(b.ListAPIKeys(c)) },
		UsedBy: []string{"keys"}},
	{Method: "CreateAPIKey", Call: func(c context.Context, b api.Backend) error {
		return noErr(b.CreateAPIKey(c, time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)))
	}, UsedBy: []string{"keys.new"}},
	{Method: "ExpireAPIKey", Call: func(c context.Context, b api.Backend) error { return b.ExpireAPIKey(c, sPrefix) },
		UsedBy: []string{"keys.expire"}},
	{Method: "DeleteAPIKey", Call: func(c context.Context, b api.Backend) error { return b.DeleteAPIKey(c, sPrefix) },
		UsedBy: []string{"keys.delete"}},
	{Method: "GetPolicy", Call: func(c context.Context, b api.Backend) error { return noErr(b.GetPolicy(c)) },
		UsedBy: []string{"policy"}},
	{Method: "CheckPolicy", Call: func(c context.Context, b api.Backend) error { return b.CheckPolicy(c, sPolicy) },
		UsedBy: []string{"policy.check"}},
	{Method: "SetPolicy", Call: func(c context.Context, b api.Backend) error { return noErr(b.SetPolicy(c, sPolicy)) },
		UsedBy: []string{"policy.apply", "policy.rollback"},
		Note:   "Only with `policy.mode: database`. In file mode, host mode writes the policy file instead."},
	{Method: "AuthRegister", Call: func(c context.Context, b api.Backend) error { return b.AuthRegister(c, sAuth, sUser) },
		UsedBy: []string{"pending.approve"}},
	{Method: "AuthApprove", Call: func(c context.Context, b api.Backend) error { return b.AuthApprove(c, sAuth) },
		UsedBy: []string{"pending.approve"}},
	{Method: "AuthReject", Call: func(c context.Context, b api.Backend) error { return b.AuthReject(c, sAuth) },
		UsedBy: []string{"pending.reject"}},
}

// BackendMethods lists api.Backend's methods (sorted; reflection does not
// keep declaration order).
func BackendMethods() []string {
	t := reflect.TypeOf((*api.Backend)(nil)).Elem()
	out := make([]string, t.NumMethod())
	for i := range out {
		out[i] = t.Method(i).Name
	}
	return out
}

// --- recording ------------------------------------------------------------

type httpRecorder struct{ reqs []string }

func (r *httpRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	p := strings.TrimPrefix(req.URL.Path, "/api/v1")
	var segs []string
	for _, s := range strings.Split(p, "/") {
		segs = append(segs, templ(s))
	}
	line := req.Method + " " + strings.Join(segs, "/")
	if q := req.URL.Query(); len(q) > 0 {
		keys := make([]string, 0, len(q))
		for k := range q {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var parts []string
		for _, k := range keys {
			parts = append(parts, k+"="+templ(q.Get(k)))
		}
		line += "?" + strings.Join(parts, "&")
	}
	r.reqs = append(r.reqs, line)
	body := "{}"
	if req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/user") {
		body = fmt.Sprintf(`{"users":[{"id":"%d","name":%q}]}`, sID, sUser)
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
}

// duration matches a seconds count execcli derives from the wall clock.
var duration = regexp.MustCompile(`^-?[0-9]+s$`)

func templ(s string) string {
	if p, ok := placeholders[s]; ok {
		return p
	}
	if strings.HasPrefix(s, os.TempDir()) {
		return "{file}"
	}
	if duration.MatchString(s) {
		return "{duration}"
	}
	return s
}

func recordHTTP(e Endpoint) ([]string, error) {
	rec := &httpRecorder{}
	c, err := httpapi.New("https://headscale.example.com", api.NewSecret("x"), httpapi.WithHTTPClient(&http.Client{Transport: rec}))
	if err != nil {
		return nil, err
	}
	_ = e.Call(context.Background(), c)
	return rec.reqs, nil
}

func recordCLI(e Endpoint) []string {
	var cmds []string
	run := func(_ context.Context, args ...string) ([]byte, []byte, error) {
		var out []string
		for _, a := range args {
			out = append(out, templ(a))
		}
		// Drop the output flag every command carries.
		if n := len(out); n >= 2 && out[n-2] == "-o" && out[n-1] == "json" {
			out = out[:n-2]
		}
		cmds = append(cmds, strings.Join(out, " "))
		if len(args) > 2 && args[1] == "users" && args[2] == "list" {
			return []byte(fmt.Sprintf(`[{"id":%d,"name":%q}]`, sID, sUser)), nil, nil
		}
		if len(args) > 1 && args[1] == "version" {
			return []byte(`{"version":"v0.29.6"}`), nil, nil
		}
		return []byte("{}"), nil, nil
	}
	b := execcli.New(execcli.WithRunner(run), execcli.WithVersion("v0.29.6"))
	_ = e.Call(context.Background(), b)
	return cmds
}

// Gate is the capability whose absence makes the method return
// Unsupported (probed on the fake backend), or CapNone.
func Gate(e Endpoint) api.Cap {
	call := e.Call
	if e.GateCall != nil {
		call = e.GateCall
	}
	all := api.NewCapabilities(api.AllCaps()...)
	for _, c := range api.AllCaps() {
		b := fake.Demo(fake.WithCaps(all.Without(c)))
		if err := call(context.Background(), b); api.IsKind(err, api.KindUnsupported) {
			return c
		}
	}
	return api.CapNone
}

// --- rendering -----------------------------------------------------------

// Versions the version table covers.
var Versions = []string{"0.29.0", "0.29.6", "0.30.0"}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func avail(a server.Avail) string {
	switch a {
	case server.AvailYes:
		return "yes"
	case server.AvailPartial:
		return "partly"
	}
	return "no"
}

func cell(s string) string {
	s = strings.ReplaceAll(s, "|", `\|`)
	return strings.ReplaceAll(s, "\n", " ")
}

func renderVersions() string {
	var b strings.Builder
	b.WriteString("| Capability |")
	for _, v := range Versions {
		b.WriteString(" " + v + " |")
	}
	b.WriteString(" What it allows |\n|---|")
	for range Versions {
		b.WriteString("---|")
	}
	b.WriteString("---|\n")
	notes := map[string]string{}
	for _, r := range server.Matrix(api.NewCapabilities(), "", nil) {
		notes[r.Feature] = r.Note
	}
	for _, c := range api.AllCaps() {
		title := c.String()
		b.WriteString("| " + title + " |")
		for _, v := range Versions {
			b.WriteString(" " + yesNo(api.CapsForVersion(v).Has(c)) + " |")
		}
		b.WriteString(" " + cell(notes[strings.ToUpper(title[:1])+title[1:]]) + " |\n")
	}
	return b.String()
}

func renderModes() string {
	var b strings.Builder
	b.WriteString("| Feature | Remote mode | Host mode | Notes |\n|---|---|---|---|\n")
	for _, r := range server.Matrix(api.CapsForVersion(Versions[len(Versions)-1]), "", nil) {
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", cell(r.Feature), avail(r.Remote), avail(r.Host), cell(r.Note))
	}
	return b.String()
}

func code(lines []string) string {
	if len(lines) == 0 {
		return "none"
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = "`" + strings.ReplaceAll(l, "|", `\|`) + "`"
	}
	return strings.Join(out, ", then ")
}

func renderEndpoints() (string, error) {
	var b strings.Builder
	b.WriteString("| Backend method | REST `/api/v1` | headscale CLI (host mode) | Gate | Used by | Notes |\n|---|---|---|---|---|---|\n")
	for _, e := range Endpoints {
		reqs, err := recordHTTP(e)
		if err != nil {
			return "", err
		}
		gate := "none"
		if g := Gate(e); g != api.CapNone {
			gate = g.String()
		}
		used := make([]string, len(e.UsedBy))
		for i, u := range e.UsedBy {
			used[i] = "`" + u + "`"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n", e.Method, code(reqs), code(recordCLI(e)), gate,
			strings.Join(used, ", "), cell(e.Note))
	}
	return b.String(), nil
}

// NeedNames names each host permission.
var NeedNames = map[host.Need]string{
	host.NeedSocket: "local API", host.NeedConfigR: "read config", host.NeedConfigW: "write config",
	host.NeedPolicyW: "write policy", host.NeedRecordsW: "write DNS records", host.NeedService: "control service",
	host.NeedLogs: "read logs", host.NeedDebug: "debug/metrics", host.NeedDBRead: "read database",
}

func needs(n host.Need) string {
	var out []string
	for bit := host.Need(1); bit != 0 && bit <= n; bit <<= 1 {
		if n&bit != 0 {
			out = append(out, NeedNames[bit])
		}
	}
	return strings.Join(out, ", ")
}

func modeName(m module.Modes) string {
	return [...]string{"remote and host", "host only", "remote and host (more on the host)", "local"}[m]
}

func danger(a module.Action) string {
	switch a.Danger {
	case module.DangerCritical:
		return "type the name"
	case module.DangerConfirm:
		return "y/N"
	}
	return ""
}

func renderActions() string {
	mods := registry.All(nil)
	sort.SliceStable(mods, func(i, j int) bool {
		si, sj := module.SectionOf(mods[i]), module.SectionOf(mods[j])
		if si != sj {
			return si < sj
		}
		return mods[i].Order() < mods[j].Order()
	})
	var b strings.Builder
	for _, m := range mods {
		fmt.Fprintf(&b, "### %s (`%s`)\n\n%s section, %s.\n\n", m.Title(), m.ID(), module.SectionOf(m).Title(), modeName(m.Modes()))
		acts := m.Actions()
		if len(acts) == 0 {
			b.WriteString("No actions of its own.\n\n")
			continue
		}
		b.WriteString("| Action | Title | Keys | Confirm | Needs |\n|---|---|---|---|---|\n")
		for _, a := range acts {
			var keys []string
			for _, k := range a.Keys {
				keys = append(keys, "`"+strings.ReplaceAll(k, "|", `\|`)+"`")
			}
			var req []string
			if a.Cap != api.CapNone {
				req = append(req, a.Cap.String())
			}
			if a.Needs != host.NeedNone {
				req = append(req, "host: "+needs(a.Needs))
			}
			fmt.Fprintf(&b, "| `%s` | %s | %s | %s | %s |\n", a.ID, cell(a.Title), strings.Join(keys, " "), danger(a), strings.Join(req, "; "))
		}
		b.WriteString("\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// Regions renders every generated region by name.
func Regions() (map[string]string, error) {
	ep, err := renderEndpoints()
	if err != nil {
		return nil, err
	}
	return map[string]string{
		"versions":  renderVersions(),
		"modes":     renderModes(),
		"endpoints": ep,
		"actions":   renderActions(),
	}, nil
}

// Update replaces each generated region in doc. A region named in doc but
// not rendered, or rendered but missing from doc, is an error.
func Update(doc string) (string, error) {
	regs, err := Regions()
	if err != nil {
		return "", err
	}
	for name, body := range regs {
		begin := "<!-- BEGIN GENERATED: " + name + " -->\n"
		end := "<!-- END GENERATED: " + name + " -->"
		i := strings.Index(doc, begin)
		j := strings.Index(doc, end)
		if i < 0 || j < i {
			return "", fmt.Errorf("region %q is missing from the document", name)
		}
		doc = doc[:i+len(begin)] + body + doc[j:]
	}
	return doc, nil
}

// DocPath is docs/CAPABILITIES.md relative to the repository root.
var DocPath = filepath.Join("docs", "CAPABILITIES.md")
