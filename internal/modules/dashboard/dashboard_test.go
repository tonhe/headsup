package dashboard

import (
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/tonhe/headsup/internal/api"
	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/store"
)

func TestIssues(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	past, soon, far := now.Add(-time.Hour), now.Add(48*time.Hour), now.Add(90*24*time.Hour)
	p := netip.MustParsePrefix
	st := store.New()
	st.Nodes = []api.Node{
		{ID: 1, GivenName: "ok", Expiry: &far},
		{ID: 2, GivenName: "router", Expiry: &far, AvailableRoutes: []netip.Prefix{p("10.0.0.0/24")}},
		{ID: 3, GivenName: "old", Expiry: &past},
		{ID: 4, GivenName: "soon", Expiry: &soon},
		{ID: 5, GivenName: "exit", AvailableRoutes: []netip.Prefix{p("0.0.0.0/0"), p("::/0")}},
	}
	st.Registrations = []host.RegistrationEntry{{AuthID: "abc", Hostname: "new-laptop"}}
	st.APIKeys = []api.APIKey{{Prefix: "hskey-api-x", Expiration: soon}}

	got := Issues(st, now, 7*24*time.Hour)
	want := []string{
		"router is waiting for approval of 1 route(s)",
		"exit is waiting for approval of exit node use",
		"new-laptop is waiting to join",
		"old has expired",
		"soon expires in 2d",
		"API key hskey-api-x expires",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d issues, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if !strings.Contains(got[i].Text, w) {
			t.Errorf("issue %d = %q, want it to contain %q", i, got[i].Text, w)
		}
	}
	if got[3].Severity != 2 {
		t.Errorf("expired node should be an error, got severity %d", got[3].Severity)
	}
}
