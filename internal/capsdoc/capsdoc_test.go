package capsdoc

import (
	"flag"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/tonhe/headsup/internal/api"
	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/modules/registry"
)

var (
	update = flag.Bool("update-docs", false, "rewrite the generated regions of docs/CAPABILITIES.md")
	// updateGoldens lets `make golden-update` refresh the doc too.
	updateGoldens = flag.Bool("update-goldens", false, "same as -update-docs")
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// TestCapabilitiesDoc fails when docs/CAPABILITIES.md is out of date with
// the code. Run `make docs` to regenerate it.
func TestCapabilitiesDoc(t *testing.T) {
	path := filepath.Join(repoRoot(t), DocPath)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cur := strings.ReplaceAll(string(b), "\r\n", "\n")
	next, err := Update(cur)
	if err != nil {
		t.Fatal(err)
	}
	if *update || *updateGoldens {
		if next != cur {
			if err := os.WriteFile(path, []byte(next), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return
	}
	if next != cur {
		t.Fatalf("%s is stale: run `make docs` and commit the result", DocPath)
	}
}

// TestEndpointsCoverBackend keeps the endpoint table in step with api.Backend.
func TestEndpointsCoverBackend(t *testing.T) {
	var got []string
	for _, e := range Endpoints {
		if slices.Contains(got, e.Method) {
			t.Errorf("%s listed twice", e.Method)
		}
		got = append(got, e.Method)
	}
	want := BackendMethods()
	for _, m := range want {
		if !slices.Contains(got, m) {
			t.Errorf("api.Backend.%s has no row in Endpoints", m)
		}
	}
	for _, m := range got {
		if !slices.Contains(want, m) {
			t.Errorf("Endpoints lists %s, which api.Backend does not have", m)
		}
	}
}

// TestUsedByExists checks every module and action ID the table names.
func TestUsedByExists(t *testing.T) {
	known := map[string]bool{"app": true}
	for _, m := range registry.All(nil) {
		known[m.ID()] = true
		for _, a := range m.Actions() {
			known[a.ID] = true
		}
	}
	for _, e := range Endpoints {
		if len(e.UsedBy) == 0 {
			t.Errorf("%s: UsedBy is empty", e.Method)
		}
		for _, u := range e.UsedBy {
			if !known[u] {
				t.Errorf("%s: %q is not a module or action ID", e.Method, u)
			}
		}
	}
}

// TestEveryEndpointIsRecorded makes sure each method (other than the local
// ones) sends something on both transports, so a broken recorder cannot
// produce an empty table.
func TestEveryEndpointIsRecorded(t *testing.T) {
	for _, e := range Endpoints {
		if e.Method == "Capabilities" {
			continue
		}
		reqs, err := recordHTTP(e)
		if err != nil {
			t.Fatal(err)
		}
		if len(reqs) == 0 {
			t.Errorf("%s sent no HTTP request", e.Method)
		}
		if e.Method != "Connect" && len(recordCLI(e)) == 0 {
			t.Errorf("%s ran no headscale command", e.Method)
		}
	}
}

func TestGates(t *testing.T) {
	want := map[string]api.Cap{
		"BackfillIPs": api.CapBackfillIPs, "DeletePreAuthKey": api.CapPreAuthKeyDelete,
		"DeleteAPIKey": api.CapAPIKeyDelete, "CheckPolicy": api.CapPolicyCheck, "SetPolicy": api.CapPolicyWritable,
		"AuthApprove": api.CapAuthApproval, "ExpireNode": api.CapDisableExpiry, "CreatePreAuthKey": api.CapTagOnlyKeys,
		"ListNodes": api.CapNone,
	}
	for _, e := range Endpoints {
		if w, ok := want[e.Method]; ok {
			if g := Gate(e); g != w {
				t.Errorf("Gate(%s) = %v, want %v", e.Method, g, w)
			}
		}
	}
}

func TestNeedNamesComplete(t *testing.T) {
	for bit := host.NeedSocket; bit <= host.NeedDBRead; bit <<= 1 {
		if NeedNames[bit] == "" {
			t.Errorf("need %d has no name", bit)
		}
	}
	if got := needs(host.NeedConfigR | host.NeedService); got != "read config, control service" {
		t.Errorf("needs() = %q", got)
	}
}

func TestUpdateMissingRegion(t *testing.T) {
	if _, err := Update("no markers here"); err == nil {
		t.Fatal("want an error for a document without regions")
	}
}
