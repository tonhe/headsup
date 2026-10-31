package uitest

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// DefaultBudget is how much fake time the commands started by one driver
// operation get before the ones still waiting count as timers.
const DefaultBudget = 2 * time.Second

// Driver runs a Bubble Tea model headlessly for flow tests.
//
// The model and every command it returns run inside a testing/synctest
// bubble, so the time package uses a fake clock that only moves when every
// goroutine in the bubble is blocked on a timer or a channel. Real work (CPU,
// file I/O, locks) takes no fake time however loaded the machine is, so
// a timer is told apart from work by what it is waiting for, not by how long
// it happens to take:
//
//   - Each operation (Send, Key, Do, ...) starts the commands it causes
//     concurrently, as Bubble Tea does, and feeds their results back in, in
//     the order the commands were started.
//   - Fake time advances only while every command is waiting on a timer or
//     a channel. Commands that are still waiting once the operation's
//     budget (DefaultBudget of fake time) is spent are parked: toast and
//     refresh ticks, cursor blinks, a log stream waiting for its next line.
//   - Parked results are kept, not dropped. Advance moves the fake clock on
//     and delivers whatever finishes in that time.
//
// When an operation returns the model has settled, so assertions on the
// screen or on model state need no polling or sleeps.
//
// The bubble's clock starts at the wall-clock time the driver is made, so
// fakes built before NewDriver agree with it. Calls that build commands (and
// so timers) must run through Do; commands built outside the bubble would
// wait on real timers. Anything blocking on a channel when the test ends,
// such as a log stream, must be cancelled from an OnClose hook.
type Driver struct {
	t      *testing.T
	budget time.Duration

	req   chan func()   // operations, run on the bubble's root goroutine
	done  chan struct{} // closed when the bubble has exited
	never chan struct{} // never closed; parks a leaked bubble without a deadlock panic

	// Owned by the bubble's root goroutine.
	m       tea.Model
	seq     uint64
	active  []*job
	parked  []*job
	notify  chan struct{}
	onClose []func()
}

type job struct {
	seq      uint64
	deadline time.Time
	out      chan []tea.Msg
}

// Option configures a Driver.
type Option func(*Driver)

// WithBudget sets the fake time one operation's commands get before the ones
// still waiting are parked.
func WithBudget(d time.Duration) Option { return func(dr *Driver) { dr.budget = d } }

// NewDriver starts m in a bubble, sends a w x h window size and runs Init.
func NewDriver(t *testing.T, m tea.Model, w, h int, opts ...Option) *Driver {
	t.Helper()
	d := &Driver{t: t, budget: DefaultBudget, m: m,
		req: make(chan func()), done: make(chan struct{}), never: make(chan struct{})}
	for _, o := range opts {
		o(d)
	}
	start := time.Now().Round(0) // wall clock only
	go func() {
		defer close(d.done)
		synctest.Test(t, func(*testing.T) {
			// Fake time starts at 2000-01-01; catch up with the wall clock so
			// data seeded from time.Now before the bubble lines up.
			time.Sleep(time.Until(start))
			d.notify = make(chan struct{}, 1)
			for f := range d.req {
				f()
			}
		})
	}()
	t.Cleanup(d.close)
	d.Send(tea.WindowSizeMsg{Width: w, Height: h})
	d.op(func() tea.Cmd { return d.m.Init() })
	return d
}

// run executes f on the bubble's root goroutine and waits for it.
func (d *Driver) run(f func()) {
	ack := make(chan struct{})
	d.req <- func() { defer close(ack); f() }
	wd := time.NewTimer(Watchdog)
	defer wd.Stop()
	select {
	case <-ack:
	case <-wd.C:
		// The bubble cannot settle: a command is blocked on something from
		// outside it (a channel or timer made outside the bubble, the
		// network, a process). Crash with every goroutine's stack.
		panic(fmt.Sprintf("uitest: %s: operation still running after %v of real time; "+
			"a command is blocked on a channel, timer or I/O from outside the bubble", d.t.Name(), Watchdog))
	}
}

// Watchdog is how long one operation may take in real time before the
// driver gives up and panics with goroutine stacks.
var Watchdog = 2 * time.Minute

// op runs f in the bubble, then settles the commands it returns.
func (d *Driver) op(f func() tea.Cmd) {
	d.t.Helper()
	var err error
	d.run(func() {
		deadline := time.Now().Add(d.budget)
		d.spawn(f(), deadline)
		err = d.settle()
	})
	if err != nil {
		d.t.Fatal(err)
	}
}

// Send delivers msg to the model and settles.
func (d *Driver) Send(msg tea.Msg) {
	d.t.Helper()
	d.op(func() tea.Cmd { return d.update(msg) })
}

// Do runs f inside the bubble and settles the command it returns. Use it for
// model methods that return commands, so their timers use the fake clock.
func (d *Driver) Do(f func() tea.Cmd) {
	d.t.Helper()
	d.op(f)
}

// Key sends one key per argument: a name ("enter", "ctrl+s", "space", ...)
// or a run of runes delivered as one key event, like a paste.
func (d *Driver) Key(keys ...string) {
	d.t.Helper()
	for _, k := range keys {
		d.Send(KeyMsg(k))
	}
}

// Type sends each rune of s as its own key event.
func (d *Driver) Type(s string) {
	d.t.Helper()
	for _, r := range s {
		if r == ' ' {
			d.Send(KeyMsg("space"))
			continue
		}
		d.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

// Advance moves the fake clock on by dur, delivering parked results and
// anything else that finishes in that time (ticks, toast dismissals).
func (d *Driver) Advance(dur time.Duration) {
	d.t.Helper()
	var err error
	d.run(func() {
		target := time.Now().Add(dur)
		for _, j := range d.parked {
			j.deadline = target
		}
		d.active = append(d.parked, d.active...)
		d.parked = nil
		err = d.settleUntil(target)
		if wait := time.Until(target); wait > 0 {
			time.Sleep(wait)
		}
	})
	if err != nil {
		d.t.Fatal(err)
	}
}

// View renders the model inside the bubble, so it sees the fake clock.
func (d *Driver) View() string {
	var out string
	d.run(func() { out = d.m.View() })
	return out
}

// Screen is View with ANSI escapes stripped.
func (d *Driver) Screen() string { return ansi.Strip(d.View()) }

// Want fails the test unless every sub is on screen.
func (d *Driver) Want(subs ...string) {
	d.t.Helper()
	out := d.Screen()
	for _, s := range subs {
		if !strings.Contains(out, s) {
			d.t.Fatalf("screen is missing %q:\n%s", s, out)
		}
	}
}

// WantNot fails the test if any sub is on screen.
func (d *Driver) WantNot(subs ...string) {
	d.t.Helper()
	out := d.Screen()
	for _, s := range subs {
		if strings.Contains(out, s) {
			d.t.Fatalf("screen should not contain %q:\n%s", s, out)
		}
	}
}

// Model returns the current model.
func (d *Driver) Model() tea.Model { return d.m }

// Parked reports how many commands are parked (waiting on timers or
// channels past their budget).
func (d *Driver) Parked() int {
	var n int
	d.run(func() { n = len(d.parked) })
	return n
}

// OnClose registers f to run inside the bubble when the test ends, before
// the driver waits for outstanding commands. Cancel log streams here.
func (d *Driver) OnClose(f func()) { d.onClose = append(d.onClose, f) }

func (d *Driver) update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	d.m, cmd = d.m.Update(msg)
	return cmd
}

// spawn starts cmd on its own goroutine with the given deadline.
func (d *Driver) spawn(cmd tea.Cmd, deadline time.Time) {
	if cmd == nil {
		return
	}
	d.seq++
	j := &job{seq: d.seq, deadline: deadline, out: make(chan []tea.Msg, 1)}
	d.active = append(d.active, j)
	go func() {
		j.out <- runCmd(cmd)
		select {
		case d.notify <- struct{}{}:
		default:
		}
	}()
}

// runCmd runs cmd, expanding a tea.Sequence in place, in order.
func runCmd(cmd tea.Cmd) []tea.Msg {
	msg := cmd()
	if cmds, ok := sequence(msg); ok {
		var out []tea.Msg
		for _, c := range cmds {
			if c != nil {
				out = append(out, runCmd(c)...)
			}
		}
		return out
	}
	return []tea.Msg{msg}
}

var cmdType = reflect.TypeOf((tea.Cmd)(nil))

// sequence recognises Bubble Tea's unexported sequenceMsg ([]tea.Cmd).
func sequence(msg tea.Msg) ([]tea.Cmd, bool) {
	if msg == nil {
		return nil, false
	}
	if _, ok := msg.(tea.BatchMsg); ok {
		return nil, false
	}
	v := reflect.ValueOf(msg)
	if v.Kind() != reflect.Slice || v.Type().Elem() != cmdType {
		return nil, false
	}
	out := make([]tea.Cmd, v.Len())
	for i := range out {
		out[i] = v.Index(i).Interface().(tea.Cmd)
	}
	return out, true
}

// maxDispatch guards against a command loop that never needs fake time.
const maxDispatch = 100000

// settle feeds results back until every active command has finished or
// passed its deadline (then it is parked).
func (d *Driver) settle() error { return d.settleUntil(time.Time{}) }

// settleUntil is settle, also parking anything still waiting at until (when
// set).
func (d *Driver) settleUntil(until time.Time) error {
	dispatched := 0
	for {
		synctest.Wait()
		// Collect finished commands in start order, so the result order does
		// not depend on goroutine scheduling.
		var ready []*job
		var msgs [][]tea.Msg
		rest := d.active[:0:0]
		for _, j := range d.active {
			select {
			case out := <-j.out:
				ready, msgs = append(ready, j), append(msgs, out)
			default:
				rest = append(rest, j)
			}
		}
		d.active = rest
		if len(ready) > 0 {
			for i, j := range ready {
				for _, msg := range msgs[i] {
					switch msg := msg.(type) {
					case nil, tea.QuitMsg:
					case tea.BatchMsg:
						for _, c := range msg {
							d.spawn(c, j.deadline)
						}
					default:
						dispatched++
						if dispatched > maxDispatch {
							return fmt.Errorf("uitest: more than %d messages in one operation; a command loop that never waits?", maxDispatch)
						}
						d.spawn(d.update(msg), j.deadline)
					}
				}
			}
			continue
		}
		// Nothing finished at this instant: everything left is waiting on a
		// timer or a channel. Park what is past its deadline, then let fake
		// time run to the next deadline or result.
		now := time.Now()
		var next time.Time
		rest = d.active[:0:0]
		for _, j := range d.active {
			if !now.Before(j.deadline) || (!until.IsZero() && !now.Before(until)) {
				d.parked = append(d.parked, j)
				continue
			}
			rest = append(rest, j)
			if next.IsZero() || j.deadline.Before(next) {
				next = j.deadline
			}
		}
		d.active = rest
		if len(d.active) == 0 {
			return nil
		}
		tm := time.NewTimer(next.Sub(now))
		select {
		case <-d.notify:
		case <-tm.C:
		}
		tm.Stop()
	}
}

// close runs the OnClose hooks, lets every outstanding command finish (fake
// time runs ahead so timers fire) and ends the bubble.
func (d *Driver) close() {
	res := make(chan int, 1)
	d.req <- func() {
		for _, f := range d.onClose {
			f()
		}
		pending := append(d.parked, d.active...)
		d.parked, d.active = nil, nil
		limit := time.Now().Add(30 * 24 * time.Hour)
		for {
			synctest.Wait()
			rest := pending[:0]
			for _, j := range pending {
				select {
				case <-j.out:
				default:
					rest = append(rest, j)
				}
			}
			pending = rest
			if len(pending) == 0 || !time.Now().Before(limit) {
				break
			}
			tm := time.NewTimer(time.Hour)
			select {
			case <-d.notify:
			case <-tm.C:
			}
			tm.Stop()
		}
		res <- len(pending)
		if len(pending) > 0 {
			// Something is blocked on a channel for good. Leave the bubble
			// waiting on a channel from outside it (so not a deadlock panic).
			<-d.never
		}
	}
	if n := <-res; n > 0 {
		d.t.Errorf("uitest: %d commands still blocked when the test ended; cancel streams in an OnClose hook", n)
		return
	}
	close(d.req)
	<-d.done
}

// KeyMsg builds the key event for a name ("enter", "esc", "ctrl+s", "space",
// "shift+tab", ...); any other string is sent as runes.
func KeyMsg(k string) tea.KeyMsg {
	if k == "space" {
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	}
	if kt, ok := keyNames[k]; ok {
		return tea.KeyMsg{Type: kt}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
}

var keyNames = func() map[string]tea.KeyType {
	m := map[string]tea.KeyType{}
	for _, kt := range []tea.KeyType{
		tea.KeyEnter, tea.KeyEsc, tea.KeyTab, tea.KeyShiftTab, tea.KeyBackspace, tea.KeyDelete,
		tea.KeyUp, tea.KeyDown, tea.KeyLeft, tea.KeyRight, tea.KeyHome, tea.KeyEnd,
		tea.KeyPgUp, tea.KeyPgDown, tea.KeyInsert,
		tea.KeyShiftUp, tea.KeyShiftDown, tea.KeyShiftLeft, tea.KeyShiftRight,
		tea.KeyCtrlUp, tea.KeyCtrlDown, tea.KeyCtrlLeft, tea.KeyCtrlRight,
		tea.KeyF1, tea.KeyF2, tea.KeyF3, tea.KeyF4, tea.KeyF5, tea.KeyF6,
		tea.KeyF7, tea.KeyF8, tea.KeyF9, tea.KeyF10, tea.KeyF11, tea.KeyF12,
	} {
		m[kt.String()] = kt
	}
	for c := 'a'; c <= 'z'; c++ {
		name := "ctrl+" + string(c)
		for kt := tea.KeyCtrlA; kt <= tea.KeyCtrlZ; kt++ {
			if kt.String() == name {
				m[name] = kt
			}
		}
	}
	m["escape"] = tea.KeyEsc
	return m
}()
