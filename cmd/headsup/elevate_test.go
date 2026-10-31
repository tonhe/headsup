package main

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestElevateOffer(t *testing.T) {
	denied := &hostErr{msg: "host mode is not available: no access to /var/run/headscale", denied: true}
	tests := []struct {
		name     string
		err      error
		euid     int
		terminal bool
		answer   string
		wantExec bool
		wantAsk  bool
	}{
		{"yes", denied, 1000, true, "y\n", true, true},
		{"enter means yes", denied, 1000, true, "\n", true, true},
		{"no", denied, 1000, true, "n\n", false, true},
		{"already root", denied, 0, true, "y\n", false, false},
		{"not a terminal", denied, 1000, false, "y\n", false, false},
		{"not denied", &hostErr{msg: "headscale was not found"}, 1000, true, "y\n", false, false},
		{"other error", errors.New("boom"), 1000, true, "y\n", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			var gotArgv []string
			e := elevator{
				euid: tt.euid, terminal: tt.terminal,
				in: strings.NewReader(tt.answer), out: &out,
				lookPath: func(string) (string, error) { return "/usr/bin/sudo", nil },
				exe:      func() (string, error) { return "/home/u/bin/headsup", nil },
				exec: func(path string, argv, _ []string) error {
					if path != "/usr/bin/sudo" {
						t.Errorf("exec path %q", path)
					}
					gotArgv = argv
					return nil
				},
				args: []string{"--theme", "x"},
			}
			if err := e.offer(tt.err); err != nil {
				t.Fatal(err)
			}
			if asked := strings.Contains(out.String(), "with sudo?"); asked != tt.wantAsk {
				t.Errorf("asked = %v, want %v (%q)", asked, tt.wantAsk, out.String())
			}
			want := []string(nil)
			if tt.wantExec {
				want = []string{"sudo", "--", "/home/u/bin/headsup", "--theme", "x"}
			}
			if !slices.Equal(gotArgv, want) {
				t.Errorf("argv %q, want %q", gotArgv, want)
			}
		})
	}
}

func TestElevateNoSudo(t *testing.T) {
	e := elevator{
		euid: 1000, terminal: true, in: strings.NewReader("y\n"), out: &bytes.Buffer{},
		lookPath: func(string) (string, error) { return "", errors.New("not found") },
		exec: func(string, []string, []string) error {
			t.Fatal("exec without sudo")
			return nil
		},
	}
	if err := e.offer(&hostErr{denied: true}); err != nil {
		t.Fatal(err)
	}
}
