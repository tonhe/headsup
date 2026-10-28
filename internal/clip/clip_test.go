package clip

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func TestCopy(t *testing.T) {
	tests := []struct {
		name       string
		env        map[string]string
		localErr   error
		wantMethod string
		wantLocal  bool
		wantPrefix string
	}{
		{"local", nil, nil, MethodLocal, true, ""},
		{"ssh tty", map[string]string{"SSH_TTY": "/dev/pts/1"}, nil, MethodOSC52, false, "\x1b]52;c;"},
		{"ssh connection", map[string]string{"SSH_CONNECTION": "1 2 3 4"}, nil, MethodOSC52, false, "\x1b]52;c;"},
		{"local fails", nil, errors.New("no xclip"), MethodOSC52, true, "\x1b]52;c;"},
		{"tmux", map[string]string{"SSH_TTY": "x", "TMUX": "1"}, nil, MethodOSC52, false, "\x1bPtmux;"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			called := false
			c := &Clipboard{
				Out:    &out,
				Getenv: func(k string) string { return tt.env[k] },
				Local:  func(string) error { called = true; return tt.localErr },
			}
			m, err := c.Copy("hello")
			if err != nil {
				t.Fatal(err)
			}
			if m != tt.wantMethod || called != tt.wantLocal {
				t.Fatalf("method=%s localCalled=%v", m, called)
			}
			if tt.wantPrefix == "" {
				if out.Len() != 0 {
					t.Fatal("unexpected OSC52 output")
				}
				return
			}
			if !strings.HasPrefix(out.String(), tt.wantPrefix) ||
				!strings.Contains(out.String(), base64.StdEncoding.EncodeToString([]byte("hello"))) {
				t.Fatalf("output %q", out.String())
			}
		})
	}
}
