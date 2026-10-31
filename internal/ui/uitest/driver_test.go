package uitest

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// probe is a tiny model: keys trigger commands of different kinds and every
// result is logged.
type probe struct {
	log   []string
	block chan struct{} // a stream that never sends until closed
	start time.Time
}

type note string

func (p *probe) Init() tea.Cmd {
	p.start = time.Now()
	p.block = make(chan struct{}) // inside the bubble, so blocking on it is durable
	return func() tea.Msg { return note("init") }
}

func (p *probe) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case note:
		p.log = append(p.log, string(msg))
	case tea.KeyMsg:
		switch msg.String() {
		case "w": // slow real work: CPU, no timers
			return p, func() tea.Msg {
				n := 0
				for i := 0; i < 3e7; i++ {
					n += i % 7
				}
				return note(fmt.Sprintf("work %d", n%2))
			}
		case "p": // work that polls with short sleeps (like a health check)
			return p, func() tea.Msg {
				for i := 0; i < 3; i++ {
					time.Sleep(500 * time.Millisecond)
				}
				return note("polled")
			}
		case "t": // a toast-style timer
			return p, tea.Tick(4*time.Second, func(time.Time) tea.Msg { return note("dismiss") })
		case "s": // a stream waiting on a channel
			return p, func() tea.Msg { <-p.block; return note("stream") }
		case "b":
			return p, tea.Batch(
				func() tea.Msg { time.Sleep(10 * time.Millisecond); return note("b-slow") },
				func() tea.Msg { return note("b1") },
				func() tea.Msg { return note("b2") },
			)
		case "q":
			return p, tea.Sequence(
				func() tea.Msg { time.Sleep(10 * time.Millisecond); return note("q1") },
				func() tea.Msg { return note("q2") },
			)
		case "l": // an animation loop (spinner, cursor blink)
			return p, p.spin()
		}
	case spinMsg:
		p.log = append(p.log, "spin")
		return p, p.spin()
	}
	return p, nil
}

type spinMsg struct{}

func (p *probe) spin() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return spinMsg{} })
}

func (p *probe) View() string { return strings.Join(p.log, ",") }

func (p *probe) count(s string) int {
	n := 0
	for _, l := range p.log {
		if l == s {
			n++
		}
	}
	return n
}

func newProbe(t *testing.T) (*Driver, *probe) {
	p := &probe{}
	d := NewDriver(t, p, 80, 24)
	return d, p
}

func TestDriverRealWorkIsNeverParked(t *testing.T) {
	d, p := newProbe(t)
	d.Key("w")
	if p.count("work 0")+p.count("work 1") != 1 {
		t.Fatalf("slow CPU work must be delivered by the time Key returns: %v", p.log)
	}
	d.Key("p")
	if p.count("polled") != 1 {
		t.Fatalf("work that sleeps within the budget must be delivered: %v", p.log)
	}
}

func TestDriverParksTimersAndAdvanceDelivers(t *testing.T) {
	d, p := newProbe(t)
	d.Key("t")
	if p.count("dismiss") != 0 || d.Parked() != 1 {
		t.Fatalf("a 4s tick should be parked: %v parked=%d", p.log, d.Parked())
	}
	d.Advance(time.Second)
	if p.count("dismiss") != 0 {
		t.Fatal("tick fired early")
	}
	d.Advance(2 * time.Second)
	if p.count("dismiss") != 1 || d.Parked() != 0 {
		t.Fatalf("tick should fire once the fake clock passes it: %v", p.log)
	}
}

func TestDriverOrdersResultsByStart(t *testing.T) {
	d, p := newProbe(t)
	d.Key("b", "q")
	got := strings.Join(p.log, ",")
	if got != "init,b1,b2,b-slow,q1,q2" {
		t.Fatalf("order %s", got)
	}
}

func TestDriverBoundsAnimationLoops(t *testing.T) {
	d, p := newProbe(t)
	d.Key("l")
	n := p.count("spin")
	if n == 0 || n > int(DefaultBudget/(100*time.Millisecond)) {
		t.Fatalf("spin loop should run within the budget, ran %d", n)
	}
	d.Key("x")
	if p.count("spin") != n {
		t.Fatal("a parked loop must not run on unrelated keys")
	}
}

func TestDriverClockStartsNow(t *testing.T) {
	before := time.Now()
	_, p := newProbe(t)
	if p.start.Before(before.Add(-time.Second)) || p.start.After(time.Now().Add(time.Minute)) {
		t.Fatalf("bubble clock %v should start near the wall clock %v", p.start, before)
	}
}

func TestDriverOnCloseUnblocksStreams(t *testing.T) {
	d, p := newProbe(t)
	d.Key("s")
	if d.Parked() != 1 {
		t.Fatalf("stream should be parked, got %d", d.Parked())
	}
	d.OnClose(func() { close(p.block) })
}

func TestKeyMsg(t *testing.T) {
	for in, want := range map[string]string{
		"enter": "enter", "esc": "esc", "ctrl+s": "ctrl+s", "space": " ", "shift+tab": "shift+tab",
		"up": "up", "x": "x", "abc": "abc",
	} {
		if got := KeyMsg(in).String(); got != want {
			t.Errorf("KeyMsg(%q) = %q, want %q", in, got, want)
		}
	}
}
