package app

import (
	"strings"
	"testing"

	"github.com/tonhe/fathom/internal/api"
	"github.com/tonhe/fathom/internal/api/fake"
)

func TestUnauthorizedOpensConnectionEditor(t *testing.T) {
	b := fake.Demo()
	b.FailNext("Connect", api.KindUnauthorized)
	var saved ProfileInput
	conn := Connector{
		Save: func(p ProfileInput) (string, error) { saved = p; return p.Name, nil },
		Open: func(string) (Conn, error) { return Conn{Backend: fake.Demo(), Label: "fixed"}, nil },
	}
	d := newDriver(t, 120, 40, Options{Backend: b, Connector: conn, ServerURL: "https://hs.example.com"})
	if d.m.form == nil || d.m.formID != formConnEdit {
		t.Fatalf("connection editor should open after an unauthorized error:\n%s", d.view())
	}
	if !strings.Contains(d.view(), "unauthorized") {
		t.Errorf("error toast missing:\n%s", d.view())
	}
	// Fill the API key field: tab past name and URL, type the key, submit.
	d.key("tab")
	d.key("tab")
	d.key("hskey-api-abcdef")
	d.send(ctrlS())
	if saved.Key.Reveal() != "hskey-api-abcdef" || saved.URL != "https://hs.example.com" {
		t.Fatalf("saved profile = %+v (key %q)", saved, saved.Key.Reveal())
	}
	if !d.m.connected || d.m.opt.Profile != "fixed" {
		t.Fatalf("should reconnect with the new profile; connected=%v label=%q", d.m.connected, d.m.opt.Profile)
	}
	if !strings.Contains(d.view(), "42 / 51") {
		t.Errorf("dashboard should load after reconnect:\n%s", d.view())
	}
}

func TestProfileSwitcher(t *testing.T) {
	opened := ""
	conn := Connector{
		Profiles: func() []string { return []string{"homelab", "work"} },
		Open: func(target string) (Conn, error) {
			opened = target
			return Conn{Backend: fake.Demo(), Label: target, ReadOnly: target == "work"}, nil
		},
	}
	d := newDriver(t, 120, 40, Options{Connector: conn})
	d.key("P")
	if d.m.formID != formSwitch {
		t.Fatal("P should open the switcher")
	}
	d.key("right") // cycle the select to "work"
	d.send(ctrlS())
	if opened != "work" || !d.m.opt.ReadOnly {
		t.Fatalf("opened %q readOnly=%v, want work/true", opened, d.m.opt.ReadOnly)
	}
}
