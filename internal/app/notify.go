package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tonhe/headsup/internal/brand"
	"github.com/tonhe/headsup/internal/module"
	"github.com/tonhe/headsup/internal/watch"
)

// Notifier delivers watch events outside the TUI. It is injectable so tests
// never pop real notifications.
type Notifier interface {
	// Desktop shows a desktop notification. Best-effort per OS.
	Desktop(title, body string) error
	// Bell rings the terminal bell.
	Bell() error
}

// SystemNotifier shells out to the platform's notifier: notify-send on
// Linux and the BSDs, osascript on macOS, and a PowerShell balloon on
// Windows. Bell writes BEL to Out (stdout when nil).
type SystemNotifier struct {
	Out io.Writer
	// run executes the command; nil runs it for real (tests replace it).
	run func(name string, args ...string) error
}

// errNoNotifier is returned where the platform has no notifier we know.
var errNoNotifier = errors.New("no desktop notifier found")

// Desktop implements Notifier.
func (n SystemNotifier) Desktop(title, body string) error {
	name, args, err := desktopCommand(runtime.GOOS, title, body, exec.LookPath)
	if err != nil {
		return err
	}
	run := n.run
	if run == nil {
		run = func(name string, args ...string) error {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return exec.CommandContext(ctx, name, args...).Run()
		}
	}
	return run(name, args...)
}

// Bell implements Notifier.
func (n SystemNotifier) Bell() error {
	out := n.Out
	if out == nil {
		out = os.Stdout
	}
	_, err := io.WriteString(out, "\a")
	return err
}

// desktopCommand picks the notifier command for goos. Arguments are passed
// to exec directly (never through a shell); osascript and PowerShell get the
// text as quoted string literals with their quotes escaped.
func desktopCommand(goos, title, body string, look func(string) (string, error)) (string, []string, error) {
	switch goos {
	case "darwin":
		script := fmt.Sprintf("display notification %s with title %s", appleQuote(body), appleQuote(title))
		return "osascript", []string{"-e", script}, nil
	case "windows":
		ps := "[void][Reflection.Assembly]::LoadWithPartialName('System.Windows.Forms');" +
			"$n=New-Object System.Windows.Forms.NotifyIcon;$n.Icon=[System.Drawing.SystemIcons]::Information;$n.Visible=$true;" +
			fmt.Sprintf("$n.ShowBalloonTip(5000,%s,%s,'Info');Start-Sleep -Seconds 6;$n.Dispose()", psQuote(title), psQuote(body))
		return "powershell", []string{"-NoProfile", "-NonInteractive", "-Command", ps}, nil
	}
	if _, err := look("notify-send"); err != nil {
		return "", nil, errNoNotifier
	}
	return "notify-send", []string{"--app-name=" + brand.Display, "--", title, body}, nil
}

func appleQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func psQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// maxToastEvents is how many events get their own line in one toast.
const maxToastEvents = 3

// onEvents turns watch events into a broadcast for modules (the activity
// feed), a toast, and the [notify] desktop and bell notifications for the
// kinds listed in [notify].events.
func (m *Model) onEvents(evs []watch.Event) tea.Cmd {
	if len(evs) == 0 {
		return nil
	}
	cmds := []tea.Cmd{m.broadcast(watch.EventsMsg{Events: evs})}
	cfg := m.opt.Config.Notify
	want := watch.Filter(evs, cfg.Events)
	if len(want) == 0 {
		return tea.Batch(cmds...)
	}
	text := eventSummary(want)
	cmds = append(cmds, m.toasts.Push(module.ToastInfo, text))
	n := m.opt.Notifier
	desktop, bell := cfg.Desktop, cfg.Bell
	if desktop || bell {
		cmds = append(cmds, func() tea.Msg {
			if bell {
				_ = n.Bell()
			}
			if desktop {
				_ = n.Desktop(brand.Display, text)
			}
			return nil
		})
	}
	return tea.Batch(cmds...)
}

// eventSummary is a short one-line description of evs.
func eventSummary(evs []watch.Event) string {
	var ls []string
	for i, e := range evs {
		if i == maxToastEvents {
			ls = append(ls, fmt.Sprintf("and %d more (activity page)", len(evs)-i))
			break
		}
		ls = append(ls, e.Text())
	}
	return strings.Join(ls, "; ")
}
