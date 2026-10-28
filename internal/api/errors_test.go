package api

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestErrorKindsAndHints(t *testing.T) {
	for _, k := range []ErrorKind{KindServer, KindUnreachable, KindUnauthorized, KindNotFound, KindConflict, KindUnsupported, KindInvalid} {
		if k.String() == "" {
			t.Errorf("kind %d has no name", k)
		}
	}
	e := Errf(KindUnauthorized, "key %s rejected", "abc")
	if !strings.Contains(e.Error(), "unauthorized: key abc rejected") || !strings.Contains(e.Hint, "apikeys create") {
		t.Errorf("Errf = %q hint %q", e.Error(), e.Hint)
	}
	if (&Error{Kind: KindConflict}).Error() != "conflict" {
		t.Error("empty message should print the kind")
	}
	wrapped := fmt.Errorf("listing: %w", e)
	if !IsKind(wrapped, KindUnauthorized) || IsKind(wrapped, KindNotFound) || IsKind(errors.New("x"), KindServer) {
		t.Error("IsKind must see through wrapping and reject foreign errors")
	}
	if AsError(wrapped) != e {
		t.Error("AsError should unwrap to the original *Error")
	}
	foreign := AsError(errors.New("boom"))
	if foreign.Kind != KindServer || !errors.Is(foreign, foreign.Err) || foreign.Unwrap() == nil {
		t.Errorf("foreign error not wrapped: %+v", foreign)
	}
	if AsError(nil) != nil {
		t.Error("AsError(nil) must be nil")
	}
	for _, k := range []ErrorKind{KindUnreachable, KindUnsupported, KindNotFound} {
		if Errf(k, "x").Hint == "" {
			t.Errorf("%v should carry a default hint", k)
		}
	}
}

func TestCapabilitiesSet(t *testing.T) {
	c := NewCapabilities(CapPolicyCheck)
	if !c.Has(CapNone) || !c.Has(CapPolicyCheck) || c.Has(CapAuthApproval) {
		t.Fatal("Has is wrong")
	}
	c = c.With(CapAuthApproval).Without(CapPolicyCheck)
	if c.Has(CapPolicyCheck) || !c.Has(CapAuthApproval) {
		t.Fatal("With/Without is wrong")
	}
	if !strings.Contains(c.String(), "web-auth approval") {
		t.Errorf("String = %q", c.String())
	}
	all := AllCaps()
	if len(all) != len(capNames) {
		t.Fatalf("AllCaps has %d, want %d", len(all), len(capNames))
	}
	for i := 1; i < len(all); i++ {
		if all[i-1] >= all[i] {
			t.Fatal("AllCaps must be sorted")
		}
	}
	if Cap(1<<30).String() != "unknown capability" {
		t.Error("unknown cap name")
	}
}

func TestNodeHelpers(t *testing.T) {
	n := Node{Name: "host", Tags: []string{"tag:web"}, ApprovedRoutes: pfx("0.0.0.0/0", "::/0")}
	if n.DisplayName() != "host" {
		t.Error("DisplayName should fall back to the hostname")
	}
	n.GivenName = "given"
	if n.DisplayName() != "given" || !n.HasTag("web") || !n.HasTag("tag:web") || n.HasTag("db") {
		t.Error("DisplayName/HasTag wrong")
	}
	if !n.ExitApproved() || !IsExitRoute(pfx("::/0")[0]) || IsExitRoute(pfx("10.0.0.0/8")[0]) {
		t.Error("exit helpers wrong")
	}
	for _, s := range []NodeStatus{StatusOffline, StatusIdle, StatusOnline, StatusExpired} {
		if s.String() == "" {
			t.Error("status without a name")
		}
	}
	for _, s := range []KeyState{KeyActive, KeyUsed, KeyExpired} {
		if s.String() == "" {
			t.Error("key state without a name")
		}
	}
	if (User{ID: 3, Name: "a"}).Ref() != (UserRef{ID: 3, Name: "a"}) {
		t.Error("User.Ref wrong")
	}
	if s, _ := NewSecret("short").MarshalText(); string(s) != "••••" {
		t.Errorf("short secret masked as %q", s)
	}
	if !(Secret{}).IsZero() || (Secret{}).Masked() != "" {
		t.Error("zero secret")
	}
}
