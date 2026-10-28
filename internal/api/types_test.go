package api

import (
	"fmt"
	"net/netip"
	"testing"
	"time"
)

func pfx(ss ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(ss))
	for i, s := range ss {
		out[i] = netip.MustParsePrefix(s)
	}
	return out
}

func TestNodeDerived(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Hour)
	soon := now.Add(2 * 24 * time.Hour)
	later := now.Add(30 * 24 * time.Hour)

	tests := []struct {
		name     string
		n        Node
		exit     bool
		pending  int
		status   NodeStatus
		expSoon  bool
		disabled bool
	}{
		{"exit needs both defaults", Node{AvailableRoutes: pfx("0.0.0.0/0")}, false, 1, StatusOffline, false, true},
		{"exit node", Node{AvailableRoutes: pfx("0.0.0.0/0", "::/0"), ApprovedRoutes: pfx("0.0.0.0/0")}, true, 1, StatusOffline, false, true},
		{"online", Node{Online: true, Expiry: &later}, false, 0, StatusOnline, false, false},
		{"idle within 24h", Node{LastSeen: now.Add(-2 * time.Hour)}, false, 0, StatusIdle, false, true},
		{"offline after 24h", Node{LastSeen: now.Add(-48 * time.Hour)}, false, 0, StatusOffline, false, true},
		{"expired beats online", Node{Online: true, Expiry: &past}, false, 0, StatusExpired, false, false},
		{"expires soon", Node{Expiry: &soon}, false, 0, StatusOffline, true, false},
		{"zero expiry disabled", Node{Expiry: &time.Time{}}, false, 0, StatusOffline, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.n.IsExitNode(); got != tt.exit {
				t.Errorf("IsExitNode = %v, want %v", got, tt.exit)
			}
			if got := len(tt.n.PendingRoutes()); got != tt.pending {
				t.Errorf("PendingRoutes = %d, want %d", got, tt.pending)
			}
			if got := tt.n.Status(now); got != tt.status {
				t.Errorf("Status = %v, want %v", got, tt.status)
			}
			if got := tt.n.ExpiresSoon(now, 7*24*time.Hour); got != tt.expSoon {
				t.Errorf("ExpiresSoon = %v, want %v", got, tt.expSoon)
			}
			if got := tt.n.ExpiryDisabled(); got != tt.disabled {
				t.Errorf("ExpiryDisabled = %v, want %v", got, tt.disabled)
			}
		})
	}
}

func TestKeyState(t *testing.T) {
	now := time.Now()
	tests := []struct {
		k    PreAuthKey
		want KeyState
	}{
		{PreAuthKey{Expiration: now.Add(time.Hour)}, KeyActive},
		{PreAuthKey{Used: true, Expiration: now.Add(time.Hour)}, KeyUsed},
		{PreAuthKey{Used: true, Reusable: true, Expiration: now.Add(time.Hour)}, KeyActive},
		{PreAuthKey{Expiration: now.Add(-time.Hour)}, KeyExpired},
	}
	for i, tt := range tests {
		if got := tt.k.State(now); got != tt.want {
			t.Errorf("%d: State = %v, want %v", i, got, tt.want)
		}
	}
}

func TestSecretNeverLeaks(t *testing.T) {
	raw := "hskey-auth-abcdefghijklmnop1234"
	s := NewSecret(raw)
	for _, out := range []string{fmt.Sprint(s), fmt.Sprintf("%v %+v %#v %s", s, s, s, s)} {
		if contains(out, "abcdefghijklmnop") {
			t.Fatalf("secret leaked: %q", out)
		}
	}
	if s.Reveal() != raw {
		t.Fatal("Reveal must return the raw value")
	}
	if got := s.Masked(); got != "hskey-auth-…1234" {
		t.Errorf("Masked = %q", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestVersions(t *testing.T) {
	tests := []struct {
		v              string
		at30, supports bool
	}{
		{"v0.29.6", false, true},
		{"0.30.0-beta.1", true, true},
		{"v0.28.0", false, false},
		{"dev", true, true},
	}
	for _, tt := range tests {
		if got := VersionAtLeast(tt.v, 0, 30); got != tt.at30 {
			t.Errorf("%s: AtLeast(0.30) = %v", tt.v, got)
		}
		if got := Supported(tt.v); got != tt.supports {
			t.Errorf("%s: Supported = %v", tt.v, got)
		}
	}
	if CapsForVersion("v0.29.6").Has(CapProblemJSON) {
		t.Error("0.29 must not report problem+json")
	}
	if !CapsForVersion("v0.29.6").Has(CapDisableExpiry) || CapsForVersion("v0.29.5").Has(CapDisableExpiry) {
		t.Error("disable-expiry arrived in 0.29.6")
	}
	if !CapsForVersion("v0.30.0").Has(CapProblemJSON | CapDisableExpiry) {
		t.Error("0.30 caps missing")
	}
}
