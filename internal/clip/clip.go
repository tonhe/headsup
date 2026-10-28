// Package clip copies text to the clipboard: the local system clipboard when
// available, otherwise (or over SSH) the terminal's OSC52 escape sequence.
package clip

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/atotto/clipboard"
	osc52 "github.com/aymanbagabas/go-osc52/v2"
)

// Copy methods reported to the caller (e.g. for a toast).
const (
	MethodLocal = "clipboard"
	MethodOSC52 = "osc52"
)

// Clipboard copies text. The zero value is not usable; use New.
type Clipboard struct {
	Out    io.Writer               // OSC52 destination (default os.Stderr)
	Getenv func(string) string     // environment lookup (default os.Getenv)
	Local  func(text string) error // local clipboard writer (default atotto)
}

// New returns a Clipboard wired to the real environment.
func New() *Clipboard {
	return &Clipboard{Out: os.Stderr, Getenv: os.Getenv, Local: clipboard.WriteAll}
}

var std = New()

// Copy copies s using the process-wide default Clipboard.
func Copy(s string) (method string, err error) { return std.Copy(s) }

// Copy copies s and reports which method was used.
func (c *Clipboard) Copy(s string) (string, error) {
	env := c.Getenv
	if env == nil {
		env = os.Getenv
	}
	overSSH := env("SSH_TTY") != "" || env("SSH_CONNECTION") != ""
	if !overSSH && c.Local != nil {
		if err := c.Local(s); err == nil {
			return MethodLocal, nil
		}
	}
	if err := c.osc52(s, env); err != nil {
		return MethodOSC52, fmt.Errorf("copy via OSC52: %w", err)
	}
	return MethodOSC52, nil
}

func (c *Clipboard) osc52(s string, env func(string) string) error {
	out := c.Out
	if out == nil {
		out = os.Stderr
	}
	seq := osc52.New(s)
	switch {
	case env("TMUX") != "":
		seq = seq.Tmux()
	case strings.HasPrefix(env("TERM"), "screen"):
		seq = seq.Screen()
	}
	_, err := seq.WriteTo(out)
	return err
}
