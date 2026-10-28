package wire

import (
	"testing"
	"time"

	"github.com/tonhe/fathom/internal/api"
)

func TestNodeShapes(t *testing.T) {
	tests := []struct {
		name string
		json string
	}{
		{"0.30 camel", `{"id":"7","givenName":"nas","ipAddresses":["100.64.0.7"],"lastSeen":"2026-01-02T03:04:05Z","registerMethod":"REGISTER_METHOD_CLI","user":{"id":"2","name":"bob"},"preAuthKey":{"id":"9"},"expiry":null}`},
		{"0.29 cli snake", `{"id":7,"given_name":"nas","ip_addresses":["100.64.0.7"],"last_seen":{"seconds":1767323045},"register_method":2,"user":{"id":2,"name":"bob"},"pre_auth_key":{"id":9}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o, err := DecodeObj([]byte(tt.json))
			if err != nil {
				t.Fatal(err)
			}
			n := Node(o)
			want := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
			if n.ID != 7 || n.GivenName != "nas" || n.User != (api.UserRef{ID: 2, Name: "bob"}) || !n.LastSeen.Equal(want) ||
				n.RegisterMethod != api.RegisterCLI || n.PreAuthKey == nil || n.PreAuthKey.ID != 9 || !n.ExpiryDisabled() ||
				len(n.IPAddresses) != 1 {
				t.Errorf("got %+v", n)
			}
		})
	}
}

func TestPolicyPos(t *testing.T) {
	tests := []struct {
		msg       string
		line, col int
	}{
		{"hujson: line 3, column 37: invalid character", 3, 37},
		{"error at line 4:9", 4, 9},
		{"policy.hujson:12:5: bad", 12, 5},
		{"unknown group on line 8", 8, 0},
		{"no position", 0, 0},
	}
	for _, tt := range tests {
		if l, c := PolicyPos(tt.msg); l != tt.line || c != tt.col {
			t.Errorf("%q: got %d:%d", tt.msg, l, c)
		}
	}
}

func TestCapsFor(t *testing.T) {
	tests := []struct {
		v    string
		want bool
	}{
		{"v0.29.0", false}, {"v0.29.6", true}, {"0.29.10", true}, {"v0.30.0", true}, {"v0.28.0", false},
	}
	for _, tt := range tests {
		if got := CapsFor(tt.v).Has(api.CapDisableExpiry); got != tt.want {
			t.Errorf("%s: DisableExpiry=%v", tt.v, got)
		}
	}
}
