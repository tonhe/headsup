package app

import (
	"testing"

	"github.com/tonhe/fathom/internal/api"
	"github.com/tonhe/fathom/internal/host"
	"github.com/tonhe/fathom/internal/module"
)

func TestGate(t *testing.T) {
	perms := host.Perms{
		host.NeedSocket:  {OK: true},
		host.NeedConfigW: {OK: false, Reason: "config.yaml is owned by root", FixCommand: "sudo fathom"},
	}
	caps := api.NewCapabilities(api.CapPolicyCheck)
	tests := []struct {
		name    string
		a       module.Action
		env     GateEnv
		allowed bool
		fix     string
	}{
		{"read-only blocks mutation", module.Action{Mutating: true}, GateEnv{ReadOnly: true}, false, ""},
		{"read-only allows reads", module.Action{}, GateEnv{ReadOnly: true}, true, ""},
		{"missing cap", module.Action{Cap: api.CapAuthApproval}, GateEnv{Caps: caps}, false, ""},
		{"present cap", module.Action{Cap: api.CapPolicyCheck}, GateEnv{Caps: caps}, true, ""},
		{"host need in remote mode", module.Action{Needs: host.NeedSocket}, GateEnv{}, false, ""},
		{"host need granted", module.Action{Needs: host.NeedSocket}, GateEnv{HostMode: true, Perms: perms}, true, ""},
		{"host need missing carries fix", module.Action{Needs: host.NeedConfigW}, GateEnv{HostMode: true, Perms: perms}, false, "sudo fathom"},
		{"enabled false", module.Action{Enabled: func(module.Context) (bool, string) { return false, "nope" }}, GateEnv{}, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := Gate(tt.a, nil, tt.env)
			if v.Allowed != tt.allowed {
				t.Fatalf("Allowed = %v (%s), want %v", v.Allowed, v.Reason, tt.allowed)
			}
			if !v.Allowed && v.Reason == "" {
				t.Error("a blocked action must explain why")
			}
			if v.FixCommand != tt.fix {
				t.Errorf("FixCommand = %q, want %q", v.FixCommand, tt.fix)
			}
		})
	}
}

func TestNeedsConfirm(t *testing.T) {
	form := func(module.Context) *module.FormSpec { return &module.FormSpec{} }
	tests := []struct {
		name string
		a    module.Action
		want module.Danger
	}{
		{"read action", module.Action{}, module.DangerNone},
		{"mutating without form is forced to confirm", module.Action{Mutating: true}, module.DangerConfirm},
		{"mutating with form: submit is the explicit step", module.Action{Mutating: true, Params: form}, module.DangerNone},
		{"critical stays critical", module.Action{Mutating: true, Danger: module.DangerCritical, Params: form}, module.DangerCritical},
	}
	for _, tt := range tests {
		if got := NeedsConfirm(tt.a); got != tt.want {
			t.Errorf("%s: got %v, want %v", tt.name, got, tt.want)
		}
	}
}
