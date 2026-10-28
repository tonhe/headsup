package pending

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

// KeyKind distinguishes the two things a client can print for an admin.
type KeyKind int

const (
	// KindRegisterKey is a node key from `/register/<key>` (CLI registration).
	KindRegisterKey KeyKind = iota
	// KindAuthID is a web-auth request ID from `/auth/<id>` (approve/reject flow).
	KindAuthID
)

// Parsed is what was extracted from pasted text.
type Parsed struct {
	Kind  KeyKind
	Value string
}

var (
	errEmpty   = errors.New("nothing to register: paste the URL or key the client printed")
	errUnknown = errors.New("could not find a registration key in that text")

	// A bare key or ID: URL-safe characters, long enough to not be a typo.
	bareRe = regexp.MustCompile(`^[A-Za-z0-9_\-:.]{8,}$`)
	// headscale prints "--key <key>" in its instructions.
	flagRe = regexp.MustCompile(`--key\s+([A-Za-z0-9_\-:.]+)`)
)

// Parse extracts a registration key or auth ID from whatever the admin pasted:
// a full URL, a `headscale nodes register` command line, or a bare value.
// Whitespace and line breaks from terminal copy/paste are ignored.
func Parse(text string) (Parsed, error) {
	s := strings.Join(strings.Fields(text), " ")
	if s == "" {
		return Parsed{}, errEmpty
	}
	if m := flagRe.FindStringSubmatch(s); m != nil {
		return Parsed{Kind: KindRegisterKey, Value: m[1]}, nil
	}
	for _, tok := range strings.Fields(s) {
		if p, ok := fromURL(tok); ok {
			return p, nil
		}
	}
	// A URL split across lines by the terminal: try the joined form.
	if p, ok := fromURL(strings.ReplaceAll(s, " ", "")); ok {
		return p, nil
	}
	if bareRe.MatchString(s) {
		return Parsed{Kind: KindRegisterKey, Value: s}, nil
	}
	return Parsed{}, errUnknown
}

func fromURL(tok string) (Parsed, bool) {
	if !strings.Contains(tok, "/") {
		return Parsed{}, false
	}
	u, err := url.Parse(strings.Trim(tok, `"'<>()`))
	if err != nil {
		return Parsed{}, false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i := 0; i+1 < len(parts); i++ {
		v := parts[i+1]
		if !bareRe.MatchString(v) {
			continue
		}
		switch parts[i] {
		case "register":
			return Parsed{Kind: KindRegisterKey, Value: v}, true
		case "auth":
			return Parsed{Kind: KindAuthID, Value: v}, true
		}
	}
	return Parsed{}, false
}

// TailscaleUpCommand is the client command for joining with a pre-auth key.
func TailscaleUpCommand(serverURL, key string) string {
	return "tailscale up --login-server=" + serverURL + " --authkey=" + key
}
