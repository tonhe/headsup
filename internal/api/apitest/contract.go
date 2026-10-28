// Package apitest holds the shared behavioural contract every api.Backend
// must pass, plus wire encoders that test simulators use to speak each
// headscale version's JSON shape.
package apitest

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tonhe/headsup/internal/api"
)

// Facts about the standard seed (internal/api/fake/seed.json) that the
// contract relies on.
const (
	SeedNodes       = 51
	SeedOnline      = 42
	SeedUsers       = 4
	SeedPendingNode = 2 // nodes with unapproved subnet routes
	SeedSoonKeys    = 3 // active pre-auth keys expiring within 7 days
	SeedMinAPIKeys  = 2
)

// SeedUserNames are the seeded users.
var SeedUserNames = []string{"alice", "bob", "carol", "ops"}

// SeedPendingAuthID is a seeded interactive auth session.
const SeedPendingAuthID = "pXq7Rz2Lm9Kd"

var haRoute = netip.MustParsePrefix("10.0.0.0/24")

// RunContract runs the shared suite. newBackend must return a fresh backend
// seeded with the standard seed for every call.
func RunContract(t *testing.T, newBackend func(t *testing.T) api.Backend) {
	t.Helper()
	ctx := context.Background()

	setup := func(t *testing.T) api.Backend {
		t.Helper()
		b := newBackend(t)
		info, err := b.Connect(ctx)
		if err != nil {
			t.Fatalf("Connect: %v", err)
		}
		if !info.Healthy {
			t.Fatalf("Connect: server not healthy")
		}
		return b
	}
	need := func(t *testing.T, b api.Backend, c api.Cap) {
		t.Helper()
		if !b.Capabilities().Has(c) {
			t.Skipf("backend lacks %s", c)
		}
	}

	t.Run("Connect", func(t *testing.T) {
		b := newBackend(t)
		info, err := b.Connect(ctx)
		mustOK(t, "Connect", err)
		if info.Version == "" {
			t.Errorf("Connect: empty version")
		}
		if info.Caps != b.Capabilities() {
			t.Errorf("Connect caps %v != Capabilities() %v", info.Caps, b.Capabilities())
		}
	})

	t.Run("ListNodes", func(t *testing.T) {
		b := setup(t)
		nodes, err := b.ListNodes(ctx, api.NodeFilter{})
		mustOK(t, "ListNodes", err)
		if len(nodes) != SeedNodes {
			t.Fatalf("ListNodes: got %d nodes, want %d", len(nodes), SeedNodes)
		}
		var online, pending, exits, ha int
		for _, n := range nodes {
			if n.ID == 0 || n.Name == "" || n.User.Name == "" {
				t.Errorf("node %+v missing id/name/user", n)
			}
			if len(n.IPAddresses) == 0 || !n.IPAddresses[0].Is4() ||
				!netip.MustParsePrefix("100.64.0.0/10").Contains(n.IPAddresses[0]) {
				t.Errorf("node %s: unexpected addresses %v", n.Name, n.IPAddresses)
			}
			if n.Online {
				online++
			}
			if len(n.PendingRoutes()) > 0 {
				pending++
			}
			if n.IsExitNode() && n.ExitApproved() {
				exits++
			}
			if slices.Contains(n.AvailableRoutes, haRoute) {
				ha++
			}
		}
		check(t, "online", online, SeedOnline)
		check(t, "nodes with pending routes", pending, SeedPendingNode)
		check(t, "approved exit nodes", exits, 1)
		check(t, "HA route advertisers", ha, 2)

		alice, err := b.ListNodes(ctx, api.NodeFilter{User: "alice"})
		mustOK(t, "ListNodes(alice)", err)
		if len(alice) == 0 || len(alice) >= SeedNodes {
			t.Errorf("ListNodes(alice): got %d nodes", len(alice))
		}
		for _, n := range alice {
			if n.User.Name != "alice" {
				t.Errorf("ListNodes(alice) returned node of %q", n.User.Name)
			}
		}
	})

	t.Run("ListUsers", func(t *testing.T) {
		b := setup(t)
		users, err := b.ListUsers(ctx, api.UserFilter{})
		mustOK(t, "ListUsers", err)
		var names []string
		for _, u := range users {
			names = append(names, u.Name)
		}
		slices.Sort(names)
		if !slices.Equal(names, SeedUserNames) {
			t.Errorf("ListUsers: got %v, want %v", names, SeedUserNames)
		}
		bob, err := b.ListUsers(ctx, api.UserFilter{Name: "bob"})
		mustOK(t, "ListUsers(bob)", err)
		if len(bob) != 1 || bob[0].Name != "bob" || bob[0].ID == 0 {
			t.Errorf("ListUsers(bob): got %+v", bob)
		}
	})

	t.Run("ListKeys", func(t *testing.T) {
		b := setup(t)
		keys, err := b.ListPreAuthKeys(ctx, api.KeyFilter{})
		mustOK(t, "ListPreAuthKeys", err)
		now := time.Now()
		soon := 0
		for _, k := range keys {
			if k.ID == 0 || k.Key.IsZero() {
				t.Errorf("pre-auth key %d missing id or key", k.ID)
			}
			if k.State(now) == api.KeyActive && k.Expiration.Sub(now) < 7*24*time.Hour {
				soon++
			}
		}
		check(t, "keys expiring within 7d", soon, SeedSoonKeys)

		ak, err := b.ListAPIKeys(ctx)
		mustOK(t, "ListAPIKeys", err)
		if len(ak) < SeedMinAPIKeys {
			t.Errorf("ListAPIKeys: got %d, want >= %d", len(ak), SeedMinAPIKeys)
		}
		for _, k := range ak {
			if k.Prefix == "" {
				t.Errorf("API key %d has no prefix", k.ID)
			}
		}
	})

	t.Run("RenameThenGet", func(t *testing.T) {
		b := setup(t)
		n := firstNode(t, b)
		got, err := b.RenameNode(ctx, n.ID, "renamed-node")
		mustOK(t, "RenameNode", err)
		if got.GivenName != "renamed-node" {
			t.Errorf("RenameNode returned given name %q", got.GivenName)
		}
		again, err := b.GetNode(ctx, n.ID)
		mustOK(t, "GetNode", err)
		if again.GivenName != "renamed-node" || again.ID != n.ID {
			t.Errorf("GetNode after rename: %q (id %d)", again.GivenName, again.ID)
		}
	})

	t.Run("SetTagsReplaces", func(t *testing.T) {
		b := setup(t)
		n := firstNode(t, b)
		_, err := b.SetTags(ctx, n.ID, []string{"tag:server", "tag:web"})
		mustOK(t, "SetTags", err)
		got, err := b.SetTags(ctx, n.ID, []string{"tag:ci"})
		mustOK(t, "SetTags", err)
		if !slices.Equal(got.Tags, []string{"tag:ci"}) {
			t.Errorf("SetTags returned %v, want [tag:ci]", got.Tags)
		}
		again, err := b.GetNode(ctx, n.ID)
		mustOK(t, "GetNode", err)
		if !slices.Equal(again.Tags, []string{"tag:ci"}) {
			t.Errorf("GetNode tags %v, want [tag:ci]", again.Tags)
		}
	})

	t.Run("ApproveRoutes", func(t *testing.T) {
		b := setup(t)
		nodes, err := b.ListNodes(ctx, api.NodeFilter{})
		mustOK(t, "ListNodes", err)
		var target *api.Node
		for i := range nodes {
			if len(nodes[i].PendingRoutes()) > 0 {
				target = &nodes[i]
				break
			}
		}
		if target == nil {
			t.Fatal("no node with pending routes in seed")
		}
		got, err := b.SetApprovedRoutes(ctx, target.ID, target.AvailableRoutes)
		mustOK(t, "SetApprovedRoutes", err)
		if len(got.PendingRoutes()) != 0 {
			t.Errorf("still pending after approve: %v", got.PendingRoutes())
		}
		again, err := b.GetNode(ctx, target.ID)
		mustOK(t, "GetNode", err)
		if len(again.PendingRoutes()) != 0 || len(again.ApprovedRoutes) != len(target.AvailableRoutes) {
			t.Errorf("GetNode approved %v available %v", again.ApprovedRoutes, again.AvailableRoutes)
		}
		// Revoke everything: the list is replaced, not merged.
		got, err = b.SetApprovedRoutes(ctx, target.ID, nil)
		mustOK(t, "SetApprovedRoutes(nil)", err)
		if len(got.ApprovedRoutes) != 0 {
			t.Errorf("approved after revoke: %v", got.ApprovedRoutes)
		}
	})

	t.Run("Expire", func(t *testing.T) {
		b := setup(t)
		n := firstNode(t, b)
		got, err := b.ExpireNode(ctx, n.ID, api.ExpireOpts{})
		mustOK(t, "ExpireNode", err)
		if !got.Expired(time.Now().Add(2 * time.Second)) {
			t.Errorf("node not expired: expiry %v", got.Expiry)
		}
		at := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Second)
		got, err = b.ExpireNode(ctx, n.ID, api.ExpireOpts{At: at})
		mustOK(t, "ExpireNode(at)", err)
		if got.Expiry == nil || !got.Expiry.Equal(at) {
			t.Errorf("expiry %v, want %v", got.Expiry, at)
		}
		if b.Capabilities().Has(api.CapDisableExpiry) {
			got, err = b.ExpireNode(ctx, n.ID, api.ExpireOpts{DisableExpiry: true})
			mustOK(t, "ExpireNode(disable)", err)
			if !got.ExpiryDisabled() {
				t.Errorf("expiry not disabled: %v", got.Expiry)
			}
		}
	})

	t.Run("Delete", func(t *testing.T) {
		b := setup(t)
		n := firstNode(t, b)
		mustOK(t, "DeleteNode", b.DeleteNode(ctx, n.ID))
		_, err := b.GetNode(ctx, n.ID)
		mustKind(t, "GetNode(deleted)", err, api.KindNotFound)
		nodes, err := b.ListNodes(ctx, api.NodeFilter{})
		mustOK(t, "ListNodes", err)
		check(t, "nodes after delete", len(nodes), SeedNodes-1)
	})

	t.Run("Users", func(t *testing.T) {
		b := setup(t)
		u, err := b.CreateUser(ctx, api.NewUser{Name: "dave", DisplayName: "Dave", Email: "dave@example.com"})
		mustOK(t, "CreateUser", err)
		if u.ID == 0 || u.Name != "dave" {
			t.Fatalf("CreateUser returned %+v", u)
		}
		r, err := b.RenameUser(ctx, u.ID, "dave2")
		mustOK(t, "RenameUser", err)
		if r.Name != "dave2" {
			t.Errorf("RenameUser returned %q", r.Name)
		}
		found, err := b.ListUsers(ctx, api.UserFilter{Name: "dave2"})
		mustOK(t, "ListUsers(dave2)", err)
		if len(found) != 1 || found[0].ID != u.ID {
			t.Errorf("ListUsers(dave2): %+v", found)
		}
		mustOK(t, "DeleteUser", b.DeleteUser(ctx, u.ID))
		all, err := b.ListUsers(ctx, api.UserFilter{})
		mustOK(t, "ListUsers", err)
		check(t, "users after delete", len(all), SeedUsers)
	})

	t.Run("PreAuthKeyLifecycle", func(t *testing.T) {
		b := setup(t)
		exp := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
		k, err := b.CreatePreAuthKey(ctx, api.NewPreAuthKey{User: "alice", Reusable: true, Expiration: exp, ACLTags: []string{"tag:ci"}})
		mustOK(t, "CreatePreAuthKey", err)
		if k.ID == 0 || k.Key.IsZero() || !k.Reusable {
			t.Fatalf("CreatePreAuthKey returned %+v", k)
		}
		if k.User == nil || k.User.Name != "alice" {
			t.Errorf("CreatePreAuthKey user %+v", k.User)
		}
		if strings.Contains(k.Key.String(), k.Key.Reveal()) {
			t.Errorf("Secret.String leaks the key")
		}
		mustOK(t, "ExpirePreAuthKey", b.ExpirePreAuthKey(ctx, k.ID))
		keys, err := b.ListPreAuthKeys(ctx, api.KeyFilter{})
		mustOK(t, "ListPreAuthKeys", err)
		got, ok := findKey(keys, k.ID)
		if !ok {
			t.Fatalf("created key %d not listed", k.ID)
		}
		if got.State(time.Now().Add(2*time.Second)) != api.KeyExpired {
			t.Errorf("key not expired: %v", got.Expiration)
		}
		if b.Capabilities().Has(api.CapPreAuthKeyDelete) {
			mustOK(t, "DeletePreAuthKey", b.DeletePreAuthKey(ctx, k.ID))
			keys, err = b.ListPreAuthKeys(ctx, api.KeyFilter{})
			mustOK(t, "ListPreAuthKeys", err)
			if _, ok := findKey(keys, k.ID); ok {
				t.Errorf("deleted key %d still listed", k.ID)
			}
		}
	})

	t.Run("TagOnlyKeyGate", func(t *testing.T) {
		b := setup(t)
		_, err := b.CreatePreAuthKey(ctx, api.NewPreAuthKey{ACLTags: []string{"tag:server"}, Expiration: time.Now().Add(time.Hour)})
		if b.Capabilities().Has(api.CapTagOnlyKeys) {
			mustOK(t, "CreatePreAuthKey(tags only)", err)
		} else {
			mustKind(t, "CreatePreAuthKey(tags only)", err, api.KindUnsupported)
		}
	})

	t.Run("APIKeyLifecycle", func(t *testing.T) {
		b := setup(t)
		before, err := b.ListAPIKeys(ctx)
		mustOK(t, "ListAPIKeys", err)
		sec, err := b.CreateAPIKey(ctx, time.Now().Add(24*time.Hour))
		mustOK(t, "CreateAPIKey", err)
		if sec.IsZero() {
			t.Fatal("CreateAPIKey returned an empty secret")
		}
		after, err := b.ListAPIKeys(ctx)
		mustOK(t, "ListAPIKeys", err)
		check(t, "API keys after create", len(after), len(before)+1)
		var prefix string
		for _, k := range after {
			if !slices.ContainsFunc(before, func(o api.APIKey) bool { return o.Prefix == k.Prefix }) {
				prefix = k.Prefix
			}
		}
		if prefix == "" || !strings.Contains(sec.Reveal(), prefix) {
			t.Fatalf("new key prefix %q not found in secret", prefix)
		}
		mustOK(t, "ExpireAPIKey", b.ExpireAPIKey(ctx, prefix))
		if b.Capabilities().Has(api.CapAPIKeyDelete) {
			mustOK(t, "DeleteAPIKey", b.DeleteAPIKey(ctx, prefix))
			final, err := b.ListAPIKeys(ctx)
			mustOK(t, "ListAPIKeys", err)
			check(t, "API keys after delete", len(final), len(before))
		}
	})

	t.Run("Policy", func(t *testing.T) {
		b := setup(t)
		p, err := b.GetPolicy(ctx)
		mustOK(t, "GetPolicy", err)
		if !strings.Contains(p.Raw, "tagOwners") || !strings.Contains(p.Raw, "//") {
			t.Fatalf("GetPolicy: unexpected policy %.80q", p.Raw)
		}
		if b.Capabilities().Has(api.CapPolicyCheck) {
			mustOK(t, "CheckPolicy(valid)", b.CheckPolicy(ctx, p.Raw))
			err = b.CheckPolicy(ctx, "{\n  \"acls\": [\n    {\"action\": \"accept\",,}\n  ]\n}\n")
			mustKind(t, "CheckPolicy(invalid)", err, api.KindInvalid)
			if e := api.AsError(err); e != nil && e.Line == 0 {
				t.Errorf("CheckPolicy(invalid): no line in %v", e)
			}
		}
		if !b.Capabilities().Has(api.CapPolicyWritable) {
			t.Skip("policy not writable")
		}
		raw := p.Raw + "\n// edited by contract test\n"
		set, err := b.SetPolicy(ctx, raw)
		mustOK(t, "SetPolicy", err)
		if set.Raw != raw {
			t.Errorf("SetPolicy returned different raw policy")
		}
		got, err := b.GetPolicy(ctx)
		mustOK(t, "GetPolicy", err)
		if got.Raw != raw {
			t.Errorf("GetPolicy after set returned old policy")
		}
		_, err = b.SetPolicy(ctx, "{ not json")
		mustKind(t, "SetPolicy(invalid)", err, api.KindInvalid)
	})

	t.Run("NotFound", func(t *testing.T) {
		b := setup(t)
		const missing = 999999
		_, err := b.GetNode(ctx, missing)
		mustKind(t, "GetNode", err, api.KindNotFound)
		_, err = b.RenameNode(ctx, missing, "nobody")
		mustKind(t, "RenameNode", err, api.KindNotFound)
		_, err = b.SetTags(ctx, missing, []string{"tag:ci"})
		mustKind(t, "SetTags", err, api.KindNotFound)
		mustKind(t, "DeleteNode", b.DeleteNode(ctx, missing), api.KindNotFound)
		_, err = b.RenameUser(ctx, missing, "nobody")
		mustKind(t, "RenameUser", err, api.KindNotFound)
		mustKind(t, "DeleteUser", b.DeleteUser(ctx, missing), api.KindNotFound)
		if b.Capabilities().Has(api.CapAuthApproval) {
			mustKind(t, "AuthApprove", b.AuthApprove(ctx, "no-such-auth-id"), api.KindNotFound)
		}
	})

	t.Run("AuthApproval", func(t *testing.T) {
		b := setup(t)
		need(t, b, api.CapAuthApproval)
		mustOK(t, "AuthApprove", b.AuthApprove(ctx, SeedPendingAuthID))
		mustKind(t, "AuthApprove(again)", b.AuthApprove(ctx, SeedPendingAuthID), api.KindNotFound)
	})

	t.Run("BackfillIPs", func(t *testing.T) {
		b := setup(t)
		need(t, b, api.CapBackfillIPs)
		_, err := b.BackfillIPs(ctx)
		mustOK(t, "BackfillIPs", err)
	})

	t.Run("CancelledContext", func(t *testing.T) {
		b := setup(t)
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		_, err := b.ListNodes(cctx, api.NodeFilter{})
		if err == nil {
			t.Fatal("ListNodes with cancelled ctx succeeded")
		}
		var e *api.Error
		if !errors.As(err, &e) {
			t.Errorf("error %T is not *api.Error", err)
		}
	})
}

func firstNode(t *testing.T, b api.Backend) api.Node {
	t.Helper()
	nodes, err := b.ListNodes(context.Background(), api.NodeFilter{})
	mustOK(t, "ListNodes", err)
	if len(nodes) == 0 {
		t.Fatal("no nodes")
	}
	return nodes[0]
}

func findKey(keys []api.PreAuthKey, id uint64) (api.PreAuthKey, bool) {
	for _, k := range keys {
		if k.ID == id {
			return k, true
		}
	}
	return api.PreAuthKey{}, false
}

func check(t *testing.T, what string, got, want int) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %d, want %d", what, got, want)
	}
}

func mustOK(t *testing.T, what string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected error: %v", what, err)
	}
}

// mustKind asserts err is an *api.Error of the given kind.
func mustKind(t *testing.T, what string, err error, kind api.ErrorKind) {
	t.Helper()
	if err == nil {
		t.Errorf("%s: expected %s error, got nil", what, kind)
		return
	}
	var e *api.Error
	if !errors.As(err, &e) {
		t.Errorf("%s: error %T (%v) is not *api.Error", what, err, err)
		return
	}
	if e.Kind != kind {
		t.Errorf("%s: kind %s, want %s (%v)", what, e.Kind, kind, e)
	}
}
