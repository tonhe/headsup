package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/tonhe/headsup/internal/brand"
)

// hostErr is why host mode failed. denied records that headscale is on this
// machine but hidden from this user, so running as root would get further.
type hostErr struct {
	msg    string
	denied bool
}

func (e *hostErr) Error() string { return e.msg }

// elevator offers to restart headsup under sudo. It is the only place that
// runs sudo, and only after the user says yes (constitution IV).
type elevator struct {
	euid     int
	terminal bool // stdin and stderr are a terminal, so we can ask
	in       io.Reader
	out      io.Writer
	lookPath func(string) (string, error)
	exe      func() (string, error)
	exec     func(path string, argv, env []string) error // replaces the process on success
	args     []string
}

func defaultElevator() elevator {
	return elevator{
		euid:     os.Geteuid(),
		terminal: isTerminal(os.Stdin) && isTerminal(os.Stderr),
		in:       os.Stdin,
		out:      os.Stderr,
		lookPath: lookPath,
		exe:      os.Executable,
		exec:     execve,
		args:     os.Args[1:],
	}
}

// elevate is swapped out by tests.
var elevate = func(err error) error { return defaultElevator().offer(err) }

// offer asks to rerun under sudo when host mode failed for lack of access and
// we are not root. It returns nil when it did not apply or the
// user said no; on yes it does not return unless sudo could not be started.
func (e elevator) offer(err error) error {
	var he *hostErr
	if !errors.As(err, &he) || !he.denied || e.euid == 0 || !e.terminal {
		return nil
	}
	sudo, lerr := e.lookPath("sudo")
	if lerr != nil {
		return nil
	}
	fmt.Fprintf(e.out, "%s: %s\n\nheadscale is installed here, but this user can't reach it.\nRestart %s with sudo? [Y/n] ",
		brand.Name, firstLine(err), brand.Name)
	ans, _ := bufio.NewReader(e.in).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(ans)) {
	case "", "y", "yes":
	default:
		fmt.Fprintln(e.out)
		return nil
	}
	exe, xerr := e.exe()
	if xerr != nil {
		return fmt.Errorf("cannot find own executable to rerun: %w", xerr)
	}
	argv := append([]string{"sudo", "--", exe}, e.args...)
	if xerr := e.exec(sudo, argv, os.Environ()); xerr != nil {
		return fmt.Errorf("running sudo: %w", xerr)
	}
	return nil
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
