package cli

import (
	"bufio"
	"fmt"
	"strings"

	"github.com/tonhe/headsup/internal/module"
)

// confirm applies the TUI's confirmation rules to a CLI verb:
//
//   - DangerNone runs straight away;
//   - --yes skips the prompt;
//   - on a terminal, DangerConfirm asks y/N and DangerCritical asks for the
//     target's name to be typed;
//   - without a terminal it refuses with exit code 7, so a script never
//     makes a destructive change it did not ask for explicitly.
func (r *runner) confirm(d module.Danger, title, consequence string, targets []module.Target, yes bool) error {
	if d == module.DangerNone || yes {
		return nil
	}
	what := describeTargets(targets)
	if !r.o.Interactive {
		return &Error{Code: ExitAborted,
			Msg: fmt.Sprintf("%s %s needs confirmation; pass --yes to run it without a terminal", strings.ToLower(title), what)}
	}
	fmt.Fprintf(r.stderr, "%s: %s\n", title, what)
	if consequence != "" {
		fmt.Fprintln(r.stderr, consequence)
	}
	in := r.reader()
	if d == module.DangerCritical {
		name := criticalName(targets)
		fmt.Fprintf(r.stderr, "Type %q to confirm: ", name)
		ans, _ := in.ReadString('\n')
		if strings.TrimSpace(ans) != name {
			return &Error{Code: ExitAborted, Msg: "not confirmed; nothing was changed"}
		}
		return nil
	}
	fmt.Fprint(r.stderr, "Continue? [y/N] ")
	ans, _ := in.ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(ans)) {
	case "y", "yes":
		return nil
	}
	return &Error{Code: ExitAborted, Msg: "not confirmed; nothing was changed"}
}

// reader shares one buffered reader over stdin, so several prompts in one
// run do not lose input to each other's buffers.
func (r *runner) reader() *bufio.Reader {
	if r.in == nil {
		r.in = bufio.NewReader(r.o.Stdin)
	}
	return r.in
}

func describeTargets(ts []module.Target) string {
	if len(ts) == 0 {
		return ""
	}
	names := make([]string, 0, len(ts))
	for _, t := range ts {
		n := t.Name
		if n == "" {
			n = t.ID
		}
		names = append(names, n)
	}
	if len(names) > 3 {
		return fmt.Sprintf("%s and %d more", strings.Join(names[:3], ", "), len(names)-3)
	}
	return strings.Join(names, ", ")
}

// criticalName is what must be typed for a critical action: the single
// target's name, or the count for several.
func criticalName(ts []module.Target) string {
	switch len(ts) {
	case 0:
		return "yes"
	case 1:
		if ts[0].Name != "" {
			return ts[0].Name
		}
		return ts[0].ID
	}
	return fmt.Sprint(len(ts))
}
